package main

import (
	"errors"
	"fmt"
	"sort"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/secret"
	"github.com/spf13/cobra"
)

func newSecretCmd(opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{Use: "secret", Short: "检查凭据引用", RunE: groupRunE}
	cmd.AddCommand(&cobra.Command{
		Use:   "check [目录]",
		Short: "检查全部 spec.secrets 引用，不请求凭据值",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := dirArg(args)
			repo, err := loadQueryRepo(cmd.Context(), dir)
			if err != nil {
				return err
			}
			resolver := secret.Resolver{Dir: dir}
			checked := 0
			problems := []ledger.Problem{}
			for _, r := range repo.Records {
				spec := r.Value.(map[string]any)["spec"].(map[string]any)
				refs, _ := spec["secrets"].(map[string]any)
				keys := make([]string, 0, len(refs))
				for key := range refs {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					checked++
					if err := resolver.Check(cmd.Context(), refs[key].(string)); err != nil {
						var failure *secret.Error
						if !errors.As(err, &failure) {
							return err
						}
						path := ledger.JoinKey("spec.secrets", key)
						problems = append(problems, ledger.Problem{Code: failure.Code, File: r.File, Line: r.Line(path), Path: path, Message: failure.Message})
					}
				}
			}
			ledger.SortProblems(problems)
			out := cmd.OutOrStdout()
			if opts.json {
				if err := writeJSON(out, struct {
					Version  string           `json:"version"`
					OK       bool             `json:"ok"`
					Checked  int              `json:"checked"`
					Problems []ledger.Problem `json:"problems"`
				}{jsonVersion, len(problems) == 0, checked, problems}); err != nil {
					return err
				}
			} else {
				for _, p := range problems {
					fmt.Fprintln(out, formatProblem(dir, p))
				}
				fmt.Fprintf(out, "检查 %d 个凭据引用，发现 %d 个问题（未请求凭据值）\n", checked, len(problems))
			}
			if len(problems) > 0 {
				return exitCode(1)
			}
			return nil
		},
	})
	return cmd
}
