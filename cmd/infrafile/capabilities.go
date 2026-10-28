package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/spf13/cobra"
)

func capabilityResult() any {
	return struct {
		Version string `json:"version"`
		plugin.CapabilityCatalog
	}{jsonVersion, plugin.Capabilities()}
}

func newCapabilitiesCmd(opts *globalOpts) *cobra.Command {
	return &cobra.Command{Use: "capabilities", Short: "离线查看第一方插件的字段、动作和支持边界", Args: maxArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if opts.json {
			return writeJSON(cmd.OutOrStdout(), capabilityResult())
		}
		catalog := plugin.Capabilities()
		fmt.Fprintln(cmd.OutOrStdout(), "记录类型："+strings.Join(catalog.Kinds, ", "))
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "插件\t服务/动作\t登记\t观测\t比较\t计划\t执行\t回读")
		for _, p := range catalog.Plugins {
			for _, service := range p.Services {
				name := service.Service
				if service.Action != "" {
					name += "/" + service.Action
				}
				s := service.Stages
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", p.Plugin, name, s.Register, s.Observe, s.Compare, s.Plan, s.Execute, s.Readback)
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "conditional 表示有配置/范围前提；unsupported 表示未实现。详细字段、权限与限制见 --json。")
		fmt.Fprintln(cmd.OutOrStdout(), "目录不检查当前账号权限或现场健康；验证等级保守为 automated-only，外部插件细节为 unknown。")
		return nil
	}}
}
