package main

import (
	"fmt"

	"github.com/infrafile/infrafile/internal/agent"
	"github.com/spf13/cobra"
)

func newAgentCmd(opts *globalOpts) *cobra.Command {
	root := &cobra.Command{Use: "agent", Short: "查看或安装与本版本配套的 Agent 指南", RunE: groupRunE}
	build := map[string]string{"version": version, "commit": commit}
	root.AddCommand(&cobra.Command{Use: "guide", Short: "打印内嵌指南", Args: maxArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		guide := agent.Guide(version, commit)
		if opts.json {
			return writeJSON(cmd.OutOrStdout(), map[string]any{"version": jsonVersion, "build": build, "guide": guide})
		}
		_, err := fmt.Fprint(cmd.OutOrStdout(), guide)
		return err
	}})
	var dir string
	install := &cobra.Command{Use: "install <codex|claude>", Short: "安装到项目的技能目录，保留已有不同内容", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		path, status, err := agent.Install(dir, args[0], agent.Guide(version, commit))
		if err != nil {
			return err
		}
		if opts.json {
			return writeJSON(cmd.OutOrStdout(), map[string]any{"version": jsonVersion, "build": build, "client": args[0], "path": path, "status": status})
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s：%s\n", path, status)
		return err
	}}
	install.Flags().StringVar(&dir, "dir", ".", "安装项目目录（无需是数据仓）")
	root.AddCommand(install)
	return root
}
