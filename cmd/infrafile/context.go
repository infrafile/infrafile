package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/spf13/cobra"
)

func newContextCmd(opts *globalOpts) *cobra.Command {
	var key, kind, maxAge string
	var labels map[string]string
	cmd := &cobra.Command{Use: "context [目录]", Short: "只读获取限定资产、直接关系、证据资格与条件动作提示", Args: maxArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if (key == "") == (len(labels) == 0) {
			return usageError(cmd, "必须二选一提供 --key 或非空 --label")
		}
		if _, ok := ledger.KindByName(kind); kind != "" && !ok {
			return usageError(cmd, "--kind 必须是六种台账类型之一")
		}
		var days *int
		if cmd.Flags().Changed("max-age") {
			n, err := strconv.Atoi(strings.TrimSuffix(maxAge, "d"))
			if !dayWindow.MatchString(maxAge) || err != nil {
				return usageError(cmd, "--max-age 必须是非负整数天数，如 7d")
			}
			days = &n
		}
		value, err := mcpCall(cmd.Context(), dirArg(args), "context", mcpArgs{Key: key, Kind: kind, Labels: labels, MaxAgeDays: days}, opts.now)
		if err != nil {
			return err
		}
		if opts.json {
			return writeJSON(cmd.OutOrStdout(), value)
		}
		report := value.(map[string]any)
		inspection := report["inspection"].(map[string]any)
		coverage := inspection["coverage"].(map[string]any)
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "截至 %s\n选定资产 %d 条；含直接关系的记录 %d 条\n", inspection["as_of"], len(inspection["assets"].([]any)), len(report["records"].([]any)))
		for _, v := range report["records"].([]any) {
			r := v.(map[string]any)
			scope := "关联"
			if r["selected"] == true {
				scope = "选定"
			}
			fmt.Fprintf(out, "%s %s（%s）\n", scope, r["key"], r["file"])
		}
		fmt.Fprintf(out, "覆盖：应检 %v，已检 %v，未知 %v，NA %v\n", coverage["expected"], coverage["checked"], coverage["unknown"], coverage["not_applicable"])
		for _, v := range report["next_actions"].([]any) {
			a := v.(map[string]any)
			fmt.Fprintf(out, "条件动作 %s：%s，经 %s\n", a["target"], a["action"], a["interface"])
		}
		fmt.Fprintln(out, "动作提示不代表授权或已执行；证据时间、缺口、已保存历史、完整登记字段与动作前提见 --json。")
		return nil
	}}
	cmd.Flags().StringVar(&key, "key", "", "已登记的 Kind/name")
	cmd.Flags().StringVar(&kind, "kind", "", "在所选主键或标签内限定资产类型")
	cmd.Flags().StringToStringVar(&labels, "label", nil, "非空精确标签；全部满足，最多 64 条选定记录")
	cmd.Flags().StringVar(&maxAge, "max-age", "", "证据上限，如 7d；缺省不证明新鲜覆盖")
	return cmd
}
