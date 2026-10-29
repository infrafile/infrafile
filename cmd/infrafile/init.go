package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/spf13/cobra"
)

const configTemplate = "apiVersion: " + ledger.APIVersion + "\nkind: Config\n"

func newInitCmd(opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "init [目录]",
		Short: "创建数据仓骨架",
		Long: `在目录（默认是当前目录）里创建数据仓骨架：infrafile.yaml 和 inventory/ 下每种记录的目录。
目录里已经有 infrafile.yaml 时不做任何改动。`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := dirArg(args)
			created, err := initRepo(dir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if opts.json {
				return writeJSON(out, struct {
					Version string   `json:"version"`
					Dir     string   `json:"dir"`
					Created []string `json:"created"`
				}{jsonVersion, dir, created})
			}
			fmt.Fprintf(out, "已在 %s 创建数据仓：\n", dir)
			for _, f := range created {
				fmt.Fprintf(out, "  %s\n", f)
			}
			fmt.Fprintf(out, "下一步：在 inventory/<类型>/ 下添加记录，再运行 infrafile validate %s\n", dir)
			return nil
		},
	}
}

// initRepo 创建骨架，返回新建的文件（相对 dir、以 / 分隔）。
func initRepo(dir string) ([]string, error) {
	config := filepath.Join(dir, ledger.ConfigFile)
	if _, err := os.Stat(config); err == nil {
		return nil, fmt.Errorf("%s 已存在，%s 已经是数据仓", config, dir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	files := []string{ledger.ConfigFile, ".gitignore"}
	for _, k := range ledger.Kinds {
		files = append(files, path.Join(ledger.InventoryDir, k.Dir, ".gitkeep"))
	}
	created := []string{}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return created, err
		}
		content := ""
		if f == ledger.ConfigFile {
			content = configTemplate
		}
		if f == ".gitignore" {
			content = "/.infrafile/\n"
		}
		fh, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue // 已有的 .gitkeep 不动
		}
		if err != nil {
			return created, err
		}
		_, err = fh.WriteString(content)
		if cerr := fh.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return created, err
		}
		created = append(created, f)
	}
	return created, nil
}
