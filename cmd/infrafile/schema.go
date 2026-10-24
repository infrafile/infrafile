package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/spf13/cobra"
)

func newSchemaCmd(opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "JSON Schema 相关命令",
		Args:  cobra.ArbitraryArgs,
		RunE:  groupRunE,
	}
	cmd.AddCommand(newSchemaExportCmd(opts))
	return cmd
}

func newSchemaExportCmd(opts *globalOpts) *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "导出 JSON Schema",
		Long: `导出每种记录的 JSON Schema，供编辑器补全和外部工具使用。
不带 -o 时把全部 schema 合成一个 JSON 输出到标准输出（无论有没有 --json）；
带 -o 时写成目录里的一组文件，文件之间用相对 $ref 引用。`,
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if outDir == "" {
				bundle, err := ledger.Bundle()
				if err != nil {
					return err
				}
				return writeJSON(out, struct {
					Version string          `json:"version"`
					Schemas json.RawMessage `json:"schemas"`
				}{jsonVersion, bundle})
			}

			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return err
			}
			files, names := ledger.SchemaFiles()
			for _, name := range names {
				if err := os.WriteFile(filepath.Join(outDir, name), files[name], 0o644); err != nil {
					return err
				}
			}
			if opts.json {
				return writeJSON(out, struct {
					Version string   `json:"version"`
					Dir     string   `json:"dir"`
					Written []string `json:"written"`
				}{jsonVersion, outDir, names})
			}
			fmt.Fprintf(out, "已把 %d 个 schema 写入 %s\n", len(names), outDir)
			return nil
		},
	}
	cmd.Flags().StringVarP(&outDir, "output", "o", "", "写入的目录")
	return cmd
}
