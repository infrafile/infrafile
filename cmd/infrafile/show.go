package main

import (
	"fmt"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

type edge struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	File string `json:"file"`
	Path string `json:"path"`
}

func newShowCmd(opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name|Kind/name> [目录]",
		Short: "查看单条记录及入边、出边（含归档记录）",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError(cmd, "需要记录名称；同名时用 Kind/name 指定类型")
			}
			return maxArgs(2)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := loadQueryRepo(dirArg(args[1:]))
			if err != nil {
				return err
			}
			var selected *ledger.Record
			var matches []string
			index := map[string]*ledger.Record{}
			for _, r := range repo.Records {
				key := r.Kind + "/" + r.Name
				index[key] = r
				if args[0] == r.Name || args[0] == key {
					selected = r
					matches = append(matches, key)
				}
			}
			if len(matches) == 0 {
				return fmt.Errorf("找不到记录 %q", args[0])
			}
			if len(matches) > 1 {
				return fmt.Errorf("名称 %q 匹配多条记录，请指定：%s", args[0], strings.Join(matches, "、"))
			}
			schemas, err := ledger.LoadSchemas()
			if err != nil {
				return err
			}
			incoming, outgoing := []edge{}, []edge{}
			for _, r := range repo.Records {
				for _, ref := range r.References(schemas) {
					if ref.Kind == selected.Kind && ref.Name == selected.Name {
						incoming = append(incoming, edge{r.Kind, r.Name, r.File, ref.Path})
					}
					if r == selected {
						target := index[ref.Kind+"/"+ref.Name]
						outgoing = append(outgoing, edge{target.Kind, target.Name, target.File, ref.Path})
					}
				}
			}
			out := cmd.OutOrStdout()
			if opts.json {
				return writeJSON(out, struct {
					Version  string `json:"version"`
					File     string `json:"file"`
					Record   any    `json:"record"`
					Incoming []edge `json:"incoming"`
					Outgoing []edge `json:"outgoing"`
				}{jsonVersion, selected.File, selected.Value, incoming, outgoing})
			}
			data, err := yaml.Marshal(selected.Value)
			if err != nil {
				return err
			}
			var buf strings.Builder
			fmt.Fprintf(&buf, "文件：%s\n%s", selected.File, data)
			for _, group := range []struct {
				name  string
				edges []edge
			}{{"入边（谁引用它）", incoming}, {"出边（它引用谁）", outgoing}} {
				fmt.Fprintf(&buf, "\n%s：%d\n", group.name, len(group.edges))
				for _, e := range group.edges {
					fmt.Fprintf(&buf, "  %s/%s  %s\n", e.Kind, e.Name, e.Path)
				}
			}
			_, err = fmt.Fprint(out, buf.String())
			return err
		},
	}
}
