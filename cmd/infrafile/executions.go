package main

import (
	"fmt"

	"github.com/infrafile/infrafile/internal/control"
	"github.com/spf13/cobra"
)

func newExecutionsCmd(opts *globalOpts) *cobra.Command {
	var dir, key string
	root := &cobra.Command{Use: "executions", Short: "只读查询已尝试执行的计划与回执", RunE: groupRunE}
	root.PersistentFlags().StringVar(&dir, "dir", ".", "数据仓目录")
	list := &cobra.Command{Use: "list", Short: "列出本地执行回执", Args: exactArgs(0), RunE: func(cmd *cobra.Command, args []string) error {
		value, err := control.ListExecutions(cmd.Context(), dir, key)
		if err != nil {
			return err
		}
		if opts.json {
			return writeJSON(cmd.OutOrStdout(), value)
		}
		if len(value.Executions) == 0 {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "尚无本范围的执行回执；缺少记录不表示云端没有变更")
			return err
		}
		for _, r := range value.Executions {
			if _, err = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  %s  %s\n", r.ID, r.Target, r.Operation, r.Status, r.UpdatedAt); err != nil {
				return err
			}
		}
		return nil
	}}
	list.Flags().StringVar(&key, "key", "", "限定 Resource 主键")
	root.AddCommand(list, &cobra.Command{Use: "show <回执 ID>", Short: "读取完整计划、写前记录和执行结果", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := control.ReadReceipt(cmd.Context(), dir, args[0])
		if err != nil {
			return err
		}
		if r == nil {
			return fmt.Errorf("没有此执行回执")
		}
		return writeJSON(cmd.OutOrStdout(), r)
	}})
	return root
}
