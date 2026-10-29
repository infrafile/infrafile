package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/infrafile/infrafile/internal/audit"
	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
	"github.com/spf13/cobra"
)

func newInspectCmd(opts *globalOpts) *cobra.Command {
	var key, kind, maxAge string
	var labels map[string]string
	cmd := &cobra.Command{Use: "inspect [目录]", Short: "离线查看资产关系、执行、覆盖及风险", Args: maxArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var days *int
		if cmd.Flags().Changed("max-age") {
			n, err := strconv.Atoi(strings.TrimSuffix(maxAge, "d"))
			if !dayWindow.MatchString(maxAge) || err != nil {
				return usageError(cmd, "--max-age 必须是非负整数天数，如 7d")
			}
			days = &n
		}
		if key != "" && len(labels) > 0 {
			return usageError(cmd, "--key 和 --label 不能同时指定")
		}
		validKind := kind == ""
		for _, k := range ledger.Kinds {
			validKind = validKind || k.Name == kind
		}
		if !validKind {
			return usageError(cmd, "--kind 必须是六种台账类型之一")
		}
		repo, err := loadQueryRepo(cmd.Context(), dirArg(args))
		if err != nil {
			return err
		}
		matches := mcpSelect(repo, mcpArgs{Key: key, Kind: kind, Labels: labels}, false)
		var keys []string
		if key != "" || kind != "" || len(labels) > 0 {
			keys = []string{}
			for _, r := range matches {
				keys = append(keys, r.Kind+"/"+r.Name)
			}
		}
		if key != "" && len(matches) == 0 {
			return fmt.Errorf("未找到目标")
		}
		report, err := audit.Inspect(cmd.Context(), repo, audit.InspectOptions{Now: opts.now, MaxAgeDays: days, Keys: keys})
		if err != nil {
			return err
		}
		value, err := inspectionValue(report)
		if err != nil {
			return err
		}
		if !validate.SnapshotSafe(value) {
			return fmt.Errorf("结果包含敏感字段或疑似凭据，已拒绝输出")
		}
		if opts.json {
			return writeJSON(cmd.OutOrStdout(), report)
		}
		out := cmd.OutOrStdout()
		c := report.Coverage
		fmt.Fprintf(out, "截至 %s\n资产 %d 条，直接关系 %d 条\n", report.AsOf, len(report.Assets), len(report.Relations))
		for _, e := range report.Execution {
			fmt.Fprintf(out, "执行 %s/%s：%s [%s]\n", e.Plugin, e.Instance, e.Status, e.Code)
		}
		fmt.Fprintf(out, "覆盖：应检 %d，已检 %d，未知 %d，NA %d\n", c.Expected, c.Checked, c.Unknown, c.NotApplicable)
		for _, check := range c.Checks {
			fmt.Fprintf(out, "  %s %s %s：%s [%s] %s\n    check: %s\n", check.Rule, check.Target, check.Aspect, check.Status, check.Code, check.Message, check.ID)
			if n := check.NotApplicable; n != nil {
				fmt.Fprintf(out, "    NA：%s；负责人 %s；复核 %s\n", n.Reason, n.Owner, n.ReviewBy)
			}
		}
		fmt.Fprintf(out, "风险：确认 %d，历史/未知 %d，已豁免 %d\n", report.Risk.Confirmed, report.Risk.Unknown, report.Risk.Waived)
		for _, f := range report.Risk.Findings {
			fmt.Fprintf(out, "  %s %s [%s] %s\n", f.Rule, f.Target, f.Assessment, f.Message)
		}
		for _, n := range report.Notices {
			fmt.Fprintf(out, "提示 %s %s：%s\n", n.Code, n.Target, n.Message)
		}
		fmt.Fprintf(out, "全部通过：%t（命令成功只表示报告已生成）\n", report.OK)
		return nil
	}}
	cmd.Flags().StringVar(&key, "key", "", "已登记的 Kind/name")
	cmd.Flags().StringVar(&kind, "kind", "", "按资产类型筛选")
	cmd.Flags().StringToStringVar(&labels, "label", nil, "精确标签筛选，如 env=production；多个标签须全部满足")
	cmd.Flags().StringVar(&maxAge, "max-age", "", "证据上限，如 7d；未设置时不证明新鲜覆盖")
	return cmd
}

func inspectionValue(report *audit.Inspection) (map[string]any, error) {
	data, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	err = decoder.Decode(&value)
	return value, err
}
