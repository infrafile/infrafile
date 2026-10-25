package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/infrafile/infrafile/internal/secret"
	"github.com/spf13/cobra"
)

func newRunCmd(opts *globalOpts) *cobra.Command {
	var bindings []string
	var dir string
	cmd := &cobra.Command{
		Use:   "run --secret NAME=<ref> -- <命令> [参数...]",
		Short: "把凭据注入子进程环境，明文不落盘",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.ArgsLenAtDash() != 0 || len(args) == 0 || len(bindings) == 0 {
				return usageError(cmd, "需要 --secret NAME=<ref>，并在 -- 后指定命令")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			type binding struct{ name, ref string }
			parsed := make([]binding, 0, len(bindings))
			seen := map[string]bool{}
			for _, raw := range bindings {
				name, ref, ok := strings.Cut(raw, "=")
				if !ok || !secret.EnvName.MatchString(name) || ref == "" {
					return usageError(cmd, "--secret 必须是合法环境变量名=<凭据引用>")
				}
				key := name
				if runtime.GOOS == "windows" {
					key = strings.ToUpper(key)
				}
				if seen[key] {
					return usageError(cmd, "--secret 不能重复指定同一个环境变量")
				}
				seen[key] = true
				parsed = append(parsed, binding{name, ref})
			}
			resolver := secret.Resolver{Dir: dir}
			env := os.Environ()
			values := make([]string, 0, len(parsed))
			for _, b := range parsed {
				value, err := resolver.Resolve(cmd.Context(), b.ref)
				if err != nil {
					return fmt.Errorf("环境变量 %s：%w", b.name, err)
				}
				env = append(env, b.name+"="+value)
				values = append(values, value)
			}
			child := exec.CommandContext(cmd.Context(), args[0], args[1:]...)
			child.Env = env
			child.Stdin = cmd.InOrStdin()
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if opts.json {
				logs := &lockedWriter{out: stderr}
				stdout, stderr = logs, logs
			}
			maskedOut, maskedErr := secret.NewRedactor(stdout, values), secret.NewRedactor(stderr, values)
			child.Stdout, child.Stderr = maskedOut, maskedErr
			err := child.Run()
			outErr, errErr := maskedOut.Close(), maskedErr.Close()
			if outErr != nil || errErr != nil {
				return fmt.Errorf("无法写入子进程输出")
			}
			code := 0
			if err != nil {
				var failed *exec.ExitError
				if !errors.As(err, &failed) {
					return fmt.Errorf("子进程无法启动或通信失败")
				}
				code = failed.ExitCode()
				if code < 0 {
					code = 1
				}
			}
			if opts.json {
				if err := writeJSON(cmd.OutOrStdout(), struct {
					Version  string `json:"version"`
					ExitCode int    `json:"exit_code"`
				}{jsonVersion, code}); err != nil {
					return err
				}
			}
			if code != 0 {
				return exitCode(code)
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&bindings, "secret", nil, "子进程环境变量 NAME=<ref>，可重复指定")
	cmd.Flags().StringVar(&dir, "dir", ".", "SOPS 密文所在的数据仓根目录；不改变子进程工作目录")
	return cmd
}

// JSON 模式把两条子进程输出流送往同一个 writer。
type lockedWriter struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out.Write(p)
}
