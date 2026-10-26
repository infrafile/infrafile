package main

import (
	"context"
	"fmt"
	"os"
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
		r, err := control.Apply(ctx, repo, p)
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
