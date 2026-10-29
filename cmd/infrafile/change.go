package main

import (
	"fmt"
	"strings"

	"github.com/infrafile/infrafile/internal/change"
	"github.com/spf13/cobra"
)

func newChangeCmd(opts *globalOpts) *cobra.Command {
	group := &cobra.Command{Use: "change", Short: "预览、提交或恢复本地台账变更（不 push）", RunE: groupRunE}
	var requestPath, draftPath, sourceFile, out, digest string
	preview := &cobra.Command{Use: "preview [目录]", Short: "完整校验候选，输出绑定 Git 基准的字节 diff", Args: maxArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if (requestPath == "") == (draftPath == "") || sourceFile != "" && draftPath == "" {
			return usageError(cmd, "必须二选一指定 --request 或 --draft；--source-file 仅用于 SSH 导入草稿")
		}
		dir := dirArg(args)
		var p *change.Preview
		var err error
		if draftPath != "" {
			p, err = change.FromDraft(cmd.Context(), dir, draftPath, sourceFile)
		} else {
			var request change.Request
			request, err = change.ReadRequest(cmd.Context(), requestPath)
			if err == nil {
				p, err = change.Prepare(cmd.Context(), dir, request, false)
			}
		}
		if err != nil {
			return err
		}
		if out != "" && p.OK {
			if err = p.Save(dir, out); err != nil {
				return err
			}
		}
		if opts.json {
			err = writeJSON(cmd.OutOrStdout(), p)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "校验通过：%t；基准 HEAD %s；摘要 %s\n", p.OK, p.Base.Head, p.Digest)
			for _, problem := range p.Problems {
				fmt.Fprintln(cmd.OutOrStdout(), problem)
			}
			for _, f := range p.Files {
				fmt.Fprintf(cmd.OutOrStdout(), "--- %s\n+++ %s\n", f.Path, f.Path)
				for _, side := range []struct{ prefix, content string }{{"-", f.Before}, {"+", f.After}} {
					for _, line := range strings.Split(strings.TrimSuffix(side.content, "\n"), "\n") {
						if side.content != "" {
							fmt.Fprintln(cmd.OutOrStdout(), side.prefix+line)
						}
					}
				}
			}
			if p.OK {
				fmt.Fprintln(cmd.OutOrStdout(), "完整记录会重新序列化为 YAML；变更文件内的注释和排版请在 diff 中核对。")
			}
		}
		if err != nil {
			return err
		}
		if !p.OK {
			return exitCode(1)
		}
		return nil
	}}
	preview.Flags().StringVar(&requestPath, "request", "", "v1 records 请求 JSON（完整记录替换）")
	preview.Flags().StringVar(&draftPath, "draft", "", "经人工编辑的 ImportDraft JSON")
	preview.Flags().StringVar(&sourceFile, "source-file", "", "显式选择 SSH 草稿原来源，不从草稿打开任意路径")
	preview.Flags().StringVar(&out, "out", "", "保存到新的审阅文件（0600，不覆盖）")
	apply := &cobra.Command{Use: "apply <预览文件> [目录]", Short: "按已审阅摘要提交，仅处理本次记录；不 push", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
		if digest == "" {
			return usageError(cmd, "必须提供已审阅预览的 --digest")
		}
		p, err := change.ReadPreview(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if p.Digest != digest {
			return fmt.Errorf("预览摘要与 --digest 不一致")
		}
		result, err := change.Apply(cmd.Context(), dirArg(args[1:]), p)
		if err != nil {
			return err
		}
		return outputChange(cmd, opts, result)
	}}
	apply.Flags().StringVar(&digest, "digest", "", "审阅时的 SHA-256 摘要")
	status := &cobra.Command{Use: "status [目录]", Short: "只读检查活动事务与恢复位置", Args: maxArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		result, err := change.Status(cmd.Context(), dirArg(args))
		if err != nil {
			return err
		}
		return outputChange(cmd, opts, result)
	}}
	recover := &cobra.Command{Use: "recover <事务 ID> [目录]", Short: "恢复仍属于该事务的文件，保留外部修改", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
		result, err := change.Recover(cmd.Context(), dirArg(args[1:]), args[0])
		if err != nil {
			return err
		}
		return outputChange(cmd, opts, result)
	}}
	group.AddCommand(preview, apply, status, recover)
	return group
}

func outputChange(cmd *cobra.Command, opts *globalOpts, result change.Result) error {
	if opts.json {
		if err := writeJSON(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s：%s\n", result.Status, result.Code)
		if result.Transaction != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "事务 %s\n", result.Transaction)
		}
		if result.Commit != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "提交 %s\n", result.Commit)
		}
	}
	if result.Status == "rejected" || result.Status == "recovery_required" {
		return exitCode(1)
	}
	return nil
}
