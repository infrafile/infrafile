package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/control"
	"github.com/spf13/cobra"
)

func newPlanCmd(opts *globalOpts) *cobra.Command {
	root := &cobra.Command{Use: "plan", Short: "生成或审阅受控写计划", RunE: groupRunE}
	for _, operation := range []string{"cdn-pause", "cdn-resume", "cdn-origin"} {
		var dir, output, host string
		var timeout time.Duration
		cmd := &cobra.Command{Use: operation + " Resource/<名称>", Short: "生成 " + operation + " 计划（只读远端）", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				return usageError(cmd, "--timeout 必须大于 0")
			}
			parameters := map[string]any{}
			if operation == "cdn-origin" {
				parameters["host"] = host
			}
			repo, err := loadQueryRepo(cmd.Context(), dir)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			p, err := control.Generate(ctx, repo, args[0], operation, parameters, opts.now)
			if err != nil {
				return err
			}
			data, err := p.Encode(ctx)
			if err != nil {
				return err
			}
			if output != "" {
				f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return err
				}
				_, err = f.Write(data)
				closeErr := f.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
				if !opts.json {
					_, err = fmt.Fprintf(cmd.OutOrStdout(), "计划已保存到 %s；请先用 infrafile plan show 审阅\n", output)
					return err
				}
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		}}
		cmd.Flags().StringVar(&dir, "dir", ".", "数据仓目录")
		cmd.Flags().StringVarP(&output, "output", "o", "", "计划文件（不覆盖已有文件；省略时输出 JSON）")
		cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "插件总时限")
		if operation == "cdn-origin" {
			cmd.Flags().StringVar(&host, "host", "", "新的回源主机名（不修改 Host header / SNI）")
		}
		root.AddCommand(cmd)
	}
	root.AddCommand(&cobra.Command{Use: "show <计划文件>", Short: "校验并渲染计划内容", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		p, err := control.Read(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if opts.json {
			data, err := p.Encode(cmd.Context())
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		}
		a := p.Actions[0]
		if p.Version == "v2" {
			fmt.Fprintf(cmd.OutOrStdout(), "审阅摘要：%s\n有效至：%s（30 分钟）\n", p.Digest, p.ExpiresAt)
			if p.Scope == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "作用范围：此旧v2计划未保存关联范围，需另行核对；真实场景请重新生成计划")
			} else {
				level := map[string]string{"zone": "整个 Cloudflare zone", "origin_rule": "已有 Origin Rule 的回源 host", "plugin_target": "外部插件目标，实际作用范围未知"}[p.Scope.Level]
				fmt.Fprintf(cmd.OutOrStdout(), "作用范围：%s；zone_id=%q\n", level, a.Binding.ZoneID)
				if p.Scope.ZoneName != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "观测 zone 名称：%s\n", p.Scope.ZoneName)
				}
				if p.Scope.Level == "origin_rule" {
					fmt.Fprintf(cmd.OutOrStdout(), "规则：ruleset_id=%q rule_id=%q；匹配表达式=%q\n", a.Binding.RulesetID, a.Binding.RuleID, p.Scope.RuleExpression)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "已登记的可能关联（不代表确定受影响）：")
				if len(p.Scope.Related) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "  未找到已登记关联；不表示没有影响")
				}
				for _, asset := range p.Scope.Related {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s [%s]\n", asset.Key, strings.Join(asset.Via, ", "))
				}
				fmt.Fprintln(cmd.OutOrStdout(), "未知影响：云端资产/DNS委派及台账外关联未完整核对，传递依赖、执行权限/套餐和实际流量/源站/TLS/业务效果未验收；详见JSON scope.unknown")
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "计划 %s · %s\n目标：%s\n插件：%s/%s\n动作：%s\n观测哈希：%s\n变更前：\n", p.Version, p.GeneratedAt, a.Target, a.Binding.Plugin, a.Binding.Instance, a.Operation, a.BeforeHash)
		if err := writeJSON(cmd.OutOrStdout(), a.Before); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "变更后：")
		return writeJSON(cmd.OutOrStdout(), a.After)
	}})
	return root
}

func newApplyCmd(opts *globalOpts) *cobra.Command {
	var dir string
	var reviewed, authorizationRef, actor string
	var timeout time.Duration
	cmd := &cobra.Command{Use: "apply <计划文件>", Short: "回读确认后执行计划，并再次回读验证", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if timeout <= 0 {
			return usageError(cmd, "--timeout 必须大于 0")
		}
		p, err := control.Read(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		repo, err := loadQueryRepo(cmd.Context(), dir)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		var r control.Result
		if p.Version == "v2" {
			r, err = control.Execute(ctx, repo, p, control.ExecutionOptions{ReviewedDigest: reviewed, AuthorizationRef: authorizationRef, Actor: actor, Now: opts.now})
		} else {
			if reviewed != "" || authorizationRef != "" || actor != "" {
				return usageError(cmd, "旧 v1 计划不支持新执行前提；请先显式启用新存储并重新生成 v2 计划")
			}
			r, err = control.Apply(ctx, repo, p)
		}
		if err != nil {
			return err
		}
		if opts.json {
			err = writeJSON(cmd.OutOrStdout(), r)
		} else {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s：%s %s\n", r.Target, r.Status, r.Message)
		}
		if err != nil {
			return err
		}
		if !r.OK {
			return exitCode(1)
		}
		return nil
	}}
	cmd.Flags().StringVar(&dir, "dir", ".", "数据仓目录")
	cmd.Flags().StringVar(&reviewed, "reviewed-digest", "", "已审阅的 v2 计划完整摘要")
	cmd.Flags().StringVar(&authorizationRef, "authorization-ref", "", "适用授权的来源引用（不填凭据）")
	cmd.Flags().StringVar(&actor, "actor", "", "本次执行主体说明（不构成身份认证）")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "插件握手、回读与执行的总时限")
	return cmd
}

func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usageError(cmd, fmt.Sprintf("需要 %d 个参数", n))
		}
		return nil
	}
}
