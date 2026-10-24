// Command infrafile 是 git 后端的基础设施资产台账工具。
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// 由 goreleaser 通过 -ldflags 注入。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// jsonVersion 是 --json 输出结构的版本，破坏性变更时递增，见 docs/json/。
const jsonVersion = "v1"

// exitCode 让命令以指定退出码结束；需要的说明已经由命令自己打印。
// 0 表示成功，1 表示发现了问题，2 表示用法错误或命令无法执行。
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("退出码 %d", int(e)) }

// globalOpts 是所有命令共用的选项。
type globalOpts struct {
	json bool
}

const usageTemplate = `用法:{{if not .HasAvailableSubCommands}}
  {{.UseLine}}{{if .HasAvailableFlags}} [选项]{{end}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} <命令> [参数]{{end}}{{if .HasAvailableSubCommands}}

命令:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

选项:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

全局选项:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

用 "{{.CommandPath}} <命令> --help" 查看命令的详细说明。{{end}}
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	root := newRoot()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	var code exitCode
	switch {
	case err == nil:
		return 0
	case errors.As(err, &code):
		return int(code)
	default:
		fmt.Fprintf(stderr, "infrafile: %v\n", err)
		return 2
	}
}

func newRoot() *cobra.Command {
	opts := &globalOpts{}
	root := &cobra.Command{
		Use:           "infrafile",
		Short:         "git 后端的基础设施资产台账",
		Long:          "infrafile —— git 后端的基础设施资产台账",
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		Args:          cobra.ArbitraryArgs,
		RunE:          groupRunE,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetVersionTemplate("infrafile {{.Version}}\n")
	root.SetUsageTemplate(usageTemplate)
	root.SetHelpTemplate("{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}\n\n{{end}}{{.UsageString}}")
	root.CompletionOptions.DisableDefaultCmd = true
	root.Flags().Bool("version", false, "打印版本信息")
	root.PersistentFlags().BoolP("help", "h", false, "打印帮助")
	root.PersistentFlags().BoolVar(&opts.json, "json", false, "以 JSON 输出，结构见 docs/json/")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError(cmd, err.Error())
	})

	root.AddCommand(
		newVersionCmd(opts),
		newValidateCmd(opts),
		newInitCmd(opts),
		newSchemaCmd(opts),
	)
	root.SetHelpCommand(&cobra.Command{
		Use:   "help [命令]",
		Short: "打印某个命令的帮助",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, rest, err := root.Find(args)
			if err != nil || len(rest) > 0 {
				return usageError(root, fmt.Sprintf("未知命令 %q", args[0]))
			}
			return target.Help()
		},
	})
	disableFlagsInUseLine(root)
	return root
}

// disableFlagsInUseLine 去掉 cobra 在用法行末尾加的英文 [flags]，由模板改写成 [选项]。
func disableFlagsInUseLine(cmd *cobra.Command) {
	cmd.DisableFlagsInUseLine = true
	for _, c := range cmd.Commands() {
		disableFlagsInUseLine(c)
	}
}

// groupRunE 是只有子命令的命令的 RunE：没有参数时打印用法，否则报未知命令。
func groupRunE(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(cmd.ErrOrStderr(), cmd.UsageString())
		return exitCode(2)
	}
	return usageError(cmd, fmt.Sprintf("未知命令 %q", args[0]))
}

// usageError 打印错误和用法，以退出码 2 结束。
func usageError(cmd *cobra.Command, msg string) error {
	fmt.Fprintf(cmd.ErrOrStderr(), "infrafile: %s\n\n%s", msg, cmd.UsageString())
	return exitCode(2)
}

// maxArgs 限制位置参数个数，错误信息用中文。
func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n && n == 0 {
			return usageError(cmd, "不接受参数")
		}
		if len(args) > n {
			return usageError(cmd, fmt.Sprintf("最多接受 %d 个参数，收到 %d 个", n, len(args)))
		}
		return nil
	}
}

// dirArg 返回可选的目录参数，默认是当前目录。
func dirArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return "."
}

// writeJSON 输出 --json 的结果。
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func newVersionCmd(opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本信息",
		Args:  maxArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.json {
				type build struct {
					Version string `json:"version"`
					Commit  string `json:"commit"`
					Date    string `json:"date"`
				}
				return writeJSON(cmd.OutOrStdout(), struct {
					Version string `json:"version"`
					Build   build  `json:"build"`
				}{jsonVersion, build{version, commit, date}})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "infrafile %s (commit %s, built %s)\n", version, commit, date)
			return nil
		},
	}
}
