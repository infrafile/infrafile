package main

import (
	"fmt"
	"time"

	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/spf13/cobra"
)

func newSyncCmd(opts *globalOpts) *cobra.Command {
	var dir string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "sync [插件]",
		Short: "同步已启用插件的观测快照到 state/",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				return usageError(cmd, "--timeout 必须大于 0")
			}
			repo, err := loadQueryRepo(dir)
			if err != nil {
				return err
			}
			selected := ""
			if len(args) == 1 {
				selected = args[0]
			}
			results, err := plugin.Run(cmd.Context(), repo, selected, timeout, opts.now)
			if err != nil {
				return err
			}
			ok := true
			for _, r := range results {
				ok = ok && r.OK
			}
			if opts.json {
				err = writeJSON(cmd.OutOrStdout(), struct {
					Version string          `json:"version"`
					OK      bool            `json:"ok"`
					Results []plugin.Result `json:"results"`
				}{jsonVersion, ok, results})
				if err != nil {
					return err
				}
			} else {
				for _, r := range results {
					status := "无变化"
					if r.Changed {
						status = "已更新"
					}
					if r.OK {
						fmt.Fprintf(cmd.OutOrStdout(), "%s/%s：%s → %s\n", r.Plugin, r.Instance, status, r.File)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "%s/%s：失败 [%s] %s\n", r.Plugin, r.Instance, r.Code, r.Message)
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "同步 %d 个实例\n", len(results))
			}
			if !ok {
				return exitCode(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "数据仓目录")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "每个实例的握手与同步总时限")
	return cmd
}
