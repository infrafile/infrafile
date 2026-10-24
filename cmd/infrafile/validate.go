package main

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
	"github.com/spf13/cobra"
)

func newValidateCmd(opts *globalOpts) *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "validate [目录]",
		Short: "校验数据仓",
		Long: `校验数据仓（默认是当前目录）：结构、外键、文件名、归档引用和疑似明文凭据。
带 --base 时还会与这个 git 引用对比，报告被删除的记录；下线请改用 metadata.archived。

退出码：0 没有问题，1 发现问题，2 用法错误或无法校验。`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := dirArg(args)
			res, err := validate.Run(dir, validate.Options{Base: base})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if opts.json {
				err = writeJSON(out, struct {
					Version  string           `json:"version"`
					OK       bool             `json:"ok"`
					Records  int              `json:"records"`
					Problems []ledger.Problem `json:"problems"`
				}{jsonVersion, len(res.Problems) == 0, res.Records, res.Problems})
				if err != nil {
					return err
				}
			} else {
				for _, p := range res.Problems {
					fmt.Fprintln(out, formatProblem(dir, p))
				}
				if len(res.Problems) == 0 {
					fmt.Fprintf(out, "通过：%d 条记录，没有发现问题\n", res.Records)
				} else {
					fmt.Fprintf(out, "发现 %d 个问题（共检查 %d 条记录）\n", len(res.Problems), res.Records)
				}
			}
			if len(res.Problems) > 0 {
				return exitCode(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "与这个 git 引用对比，报告被删除的记录")
	return cmd
}

// formatProblem 输出成 文件:行: 字段: 消息 [代码]，便于编辑器跳转。
func formatProblem(dir string, p ledger.Problem) string {
	s := filepath.Join(dir, filepath.FromSlash(p.File))
	if p.Line > 0 {
		s += ":" + strconv.Itoa(p.Line)
	}
	s += ": "
	if p.Path != "" {
		s += p.Path + ": "
	}
	return s + p.Message + " [" + p.Code + "]"
}
