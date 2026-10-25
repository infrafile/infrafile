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
	return group
}

func newImportSourceCmd(opts *globalOpts, source string) *cobra.Command {
	var dir, file, output string
	var write bool
	short := "从 SSH config 生成 Server 草稿"
	if source == "state" {
		short = "从 state/ 快照生成资产草稿"
	}
	cmd := &cobra.Command{Use: source, Short: short, Args: maxArgs(0), RunE: func(cmd *cobra.Command, args []string) error {
		if write && output != "" {
			return usageError(cmd, "--write 与 --output 不能同时使用")
		}
		repo, err := loadQueryRepo(dir)
		if err != nil {
			return err
		}
		var entries []importer.Entry
		var notices []importer.Notice
		if source == "state" {
			entries, notices, err = importer.State(repo.Dir)
		} else {
			input := file
			if input == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				input = filepath.Join(home, ".ssh", "config")
			}
			entries, err = importer.SSHConfig(input)
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
	return cmd
}
