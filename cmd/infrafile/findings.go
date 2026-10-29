package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/audit"
	"github.com/spf13/cobra"
)

func newFindingsCmd(opts *globalOpts) *cobra.Command {
	group := &cobra.Command{Use: "findings", Short: "显式推进或只读查询本地问题历史", RunE: groupRunE}
	var maxAge, exported, before string
	var after uint64
	refresh := &cobra.Command{Use: "refresh [目录]", Short: "离线、锁内刷新完整台账的问题历史", Args: maxArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		days, err := strconv.Atoi(strings.TrimSuffix(maxAge, "d"))
		if !dayWindow.MatchString(maxAge) || err != nil {
			return usageError(cmd, "refresh 必须显式提供 --max-age，如 7d")
		}
		result, err := audit.RefreshHistory(cmd.Context(), dirArg(args), audit.InspectOptions{Now: opts.now, MaxAgeDays: &days})
		if err != nil {
			return err
		}
		return outputHistory(cmd, opts, result)
	}}
	refresh.Flags().StringVar(&maxAge, "max-age", "", "必须显式指定证据上限，如 7d")
	list := &cobra.Command{Use: "list [目录]", Short: "只读查询已保存的问题，不推进检查时间", Args: maxArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		h, err := audit.ReadHistory(cmd.Context(), dirArg(args))
		if err != nil {
			return err
		}
		if opts.json {
			return writeJSON(cmd.OutOrStdout(), struct {
				Version string                `json:"version"`
				Present bool                  `json:"present"`
				History *audit.FindingHistory `json:"history"`
			}{jsonVersion, h != nil, h})
		}
		if h == nil {
			fmt.Fprintln(cmd.OutOrStdout(), "尚无问题历史；缺少历史不表示已恢复，请显式 findings refresh")
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "最近显式检查 %s，问题 %d 条，变化游标 %d（已清理至 %d）\n", h.Data.CheckedAt, len(h.Data.Issues), h.Data.LastSequence, h.Data.PrunedThrough)
		for _, state := range h.Data.Issues {
			fmt.Fprintf(cmd.OutOrStdout(), "%s/%s  %s %s %s  %s / %s，发生 %d 次\n", state.Identity.Fingerprint, state.Identity.ScopeHash, state.Identity.Key.Rule, state.Identity.Key.Target, state.Identity.Key.Aspect, state.Lifecycle, state.Assessment, state.Occurrences)
		}
		return nil
	}}
	changes := &cobra.Command{Use: "changes [目录]", Short: "只读查询指定游标之后的变化", Args: maxArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		h, err := audit.ReadHistory(cmd.Context(), dirArg(args))
		if err != nil {
			return err
		}
		events := []audit.HistoryEvent{}
		var last, pruned uint64
		if h != nil {
			events, err = h.HistoryChanges(after)
			last, pruned = h.Data.LastSequence, h.Data.PrunedThrough
		} else if after != 0 {
			return fmt.Errorf("缺少历史，不能核实变化游标")
		}
		if err != nil {
			return err
		}
		if opts.json {
			return writeJSON(cmd.OutOrStdout(), struct {
				Version       string               `json:"version"`
				Present       bool                 `json:"present"`
				LastSequence  uint64               `json:"last_sequence"`
				PrunedThrough uint64               `json:"pruned_through"`
				Changes       []audit.HistoryEvent `json:"changes"`
			}{jsonVersion, h != nil, last, pruned, events})
		}
		if h == nil {
			fmt.Fprintln(cmd.OutOrStdout(), "尚无问题历史；未执行检查")
		}
		for _, event := range events {
			fmt.Fprintf(cmd.OutOrStdout(), "%d %s %s %s %s\n", event.Sequence, event.At, event.Type, event.Identity.Key.Target, event.Identity.Key.Aspect)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "变化 %d 条，当前游标 %d\n", len(events), last)
		return nil
	}}
	changes.Flags().Uint64Var(&after, "after", 0, "只读取此序号后的变化；已清理的游标拒绝")
	export := &cobra.Command{Use: "export [目录]", Short: "向标准输出完整导出已验证历史 JSON", Args: maxArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		data, err := audit.ExportHistory(cmd.Context(), dirArg(args))
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(data)
		return err
	}}
	prune := &cobra.Command{Use: "prune [目录]", Short: "核对完整导出后，清理旧恢复状态和旧变化；活动问题保留", Args: maxArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if exported == "" {
			return usageError(cmd, "清理前须提供当前完整导出的 --export 文件")
		}
		cutoff := opts.now.AddDate(0, 0, -audit.HistoryRetentionDays)
		if cmd.Flags().Changed("before") {
			var err error
			cutoff, err = time.Parse(time.RFC3339, before)
			if err != nil {
				return usageError(cmd, "--before 必须是 RFC3339 时间")
			}
		}
		result, err := audit.PruneHistory(cmd.Context(), dirArg(args), exported, cutoff, opts.now)
		if err != nil {
			return err
		}
		return outputHistory(cmd, opts, result)
	}}
	prune.Flags().StringVar(&exported, "export", "", "独立的当前完整导出副本，必须与原历史字节一致")
	prune.Flags().StringVar(&before, "before", "", "仅清理此时间前的数据，默认 180 天前；不自动清理")
	group.AddCommand(refresh, list, changes, export, prune)
	return group
}

func outputHistory(cmd *cobra.Command, opts *globalOpts, result *audit.HistoryRefresh) error {
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "已保存：%t；最近显式检查 %s；问题 %d 条；本次变化 %d 条；游标 %d\n", result.Written, result.History.Data.CheckedAt, len(result.History.Data.Issues), len(result.Changes), result.History.Data.LastSequence)
	for _, event := range result.Changes {
		fmt.Fprintf(cmd.OutOrStdout(), "%d %s %s %s\n", event.Sequence, event.Type, event.Identity.Key.Target, event.Identity.Key.Aspect)
	}
	return nil
}
