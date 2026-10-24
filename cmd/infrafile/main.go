// Command infrafile 是 git 后端的基础设施资产台账工具。
package main

import (
	"fmt"
	"io"
	"os"
)

// 由 goreleaser 通过 -ldflags 注入。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `infrafile —— git 后端的基础设施资产台账

用法:
  infrafile <命令> [参数]

命令:
  version   打印版本信息
  help      打印本帮助

项目处于早期开发阶段，尚不可用。
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintf(stdout, "infrafile %s (commit %s, built %s)\n", version, commit, date)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "infrafile: 未知命令 %q\n\n%s", args[0], usage)
		return 2
	}
}
