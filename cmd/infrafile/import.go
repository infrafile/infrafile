package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/infrafile/infrafile/internal/importer"
	"github.com/spf13/cobra"
)

func newImportCmd(opts *globalOpts) *cobra.Command {
	group := &cobra.Command{Use: "import", Short: "从离线配置或观测生成记录草稿，只建不覆盖", RunE: groupRunE}
	for _, source := range []string{"ssh-config", "state"} {
		group.AddCommand(newImportSourceCmd(opts, source))
	}
	group.AddCommand(newImportReviewCmd(opts))
	return group
}

func newImportSourceCmd(opts *globalOpts, source string) *cobra.Command {
	var dir, file, output, draftPath string
	var write bool
	short := "从 SSH config 生成 Server 草稿"
	if source == "state" {
		short = "从 state/ 快照生成资产草稿"
	}
	cmd := &cobra.Command{Use: source, Short: short, Args: maxArgs(0), RunE: func(cmd *cobra.Command, args []string) error {
		if write && output != "" {
			return usageError(cmd, "--write 与 --output 不能同时使用")
		}
		if draftPath != "" && (write || output != "") {
			return usageError(cmd, "--draft 与 --write/--output 互斥")
		}
		repo, err := loadQueryRepo(cmd.Context(), dir)
		if err != nil {
			return err
		}
		if draftPath != "" {
			draft, review, err := importer.PrepareDraft(cmd.Context(), repo, source, file)
			if err != nil {
				return err
			}
			if err := draft.Save(cmd.Context(), repo.Dir, draftPath); err != nil {
				return err
			}
			return printImportReview(cmd, opts, review)
		}
		var entries []importer.Entry
		var notices []importer.Notice
		if source == "state" {
			entries, notices, err = importer.State(cmd.Context(), repo.Dir)
		} else {
			input := file
			if input == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				input = filepath.Join(home, ".ssh", "config")
			}
			entries, err = importer.SSHConfig(cmd.Context(), input)
		}
		if err != nil {
			return err
		}
		result, err := importer.Prepare(repo, entries, notices)
		if err != nil {
			return err
		}
		destination := ""
		if write {
			destination = filepath.Join(repo.Dir, "inventory")
		} else if output != "" {
			destination, err = importer.ReviewPath(repo.Dir, output)
			if err != nil {
				return err
			}
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		if destination != "" {
			if err := result.Publish(destination); err != nil {
				return err
			}
		}
		if opts.json {
			mode, directory := "stdout", ""
			if write {
				mode, directory = "inventory", filepath.Join(repo.Dir, "inventory")
			} else if output != "" {
				mode, directory = "review", output
			}
			return writeJSON(cmd.OutOrStdout(), struct {
				Version   string `json:"version"`
				Mode      string `json:"mode"`
				Directory string `json:"directory,omitempty"`
				*importer.Result
			}{jsonVersion, mode, directory, result})
		}
		if destination == "" {
			data, err := result.YAML()
			if err != nil {
				return err
			}
			if _, err = cmd.OutOrStdout().Write(data); err != nil {
				return err
			}
		}
		for _, entry := range result.Records {
			if destination != "" || entry.Status == "exists" {
				status := "已创建"
				if entry.Status == "exists" {
					status = "已存在，跳过"
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%s/%s：%s\n", entry.Kind, entry.Name, status)
			}
		}
		for _, notice := range result.Skipped {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s：跳过 [%s]\n", notice.Source, notice.Reason)
		}
		return nil
	}}
	cmd.Flags().StringVar(&dir, "dir", ".", "数据仓目录")
	if source == "ssh-config" {
		cmd.Flags().StringVar(&file, "file", "", "SSH config 路径（默认 ~/.ssh/config）")
	}
	cmd.Flags().BoolVar(&write, "write", false, "只创建 inventory/ 中不存在的记录")
	cmd.Flags().StringVarP(&output, "output", "o", "", "将草稿写到待审目录，只创建不存在的文件")
	cmd.Flags().StringVar(&draftPath, "draft", "", "保存带来源哈希的可编辑草稿；仅预览，不写 inventory")
	return cmd
}

func newImportReviewCmd(opts *globalOpts) *cobra.Command {
	var dir, file string
	cmd := &cobra.Command{Use: "review <草稿文件>", Short: "离线校验手改候选、来源版本、身份和完整台账引用", Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return usageError(cmd, "需要一个草稿文件")
		}
		return nil
	}, RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := loadQueryRepo(cmd.Context(), dir)
		if err != nil {
			return err
		}
		draft, err := importer.ReadDraft(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		review, err := importer.ReviewDraft(cmd.Context(), repo, draft, file)
		if err != nil {
			return err
		}
		return printImportReview(cmd, opts, review)
	}}
	cmd.Flags().StringVar(&dir, "dir", ".", "数据仓目录")
	cmd.Flags().StringVar(&file, "file", "", "SSH 来源；SSH 草稿必须显式选择，不读取草稿中的路径")
	return cmd
}

func printImportReview(cmd *cobra.Command, opts *globalOpts, review *importer.ReviewResult) error {
	if opts.json {
		if err := writeJSON(cmd.OutOrStdout(), review); err != nil {
			return err
		}
	} else {
		for _, entry := range review.Records {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s [%s] 匹配=%s 缺口=%v\n", entry.ID, entry.Key, entry.Status, entry.Match, entry.Gaps); err != nil {
				return err
			}
		}
		for _, problem := range review.Problems {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "拒绝："+problem); err != nil {
				return err
			}
		}
		for _, notice := range review.Skipped {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s：跳过 [%s]\n", notice.Source, notice.Reason); err != nil {
				return err
			}
		}
	}
	if !review.OK {
		return exitCode(1)
	}
	return nil
}
