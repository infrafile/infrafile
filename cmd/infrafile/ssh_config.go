package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type sshHost struct {
	Name     string `json:"name"`
	HostName string `json:"hostname"`
	User     string `json:"user,omitempty"`
	Port     int    `json:"port,omitempty"`
}

var (
	sshAddress = regexp.MustCompile(`^[A-Za-z0-9:][A-Za-z0-9.:-]*$`)
	sshUser    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@-]*\$?$`)
)

func newSSHConfigCmd(opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh-config [目录]",
		Short: "从在用 Server 记录生成 SSH config 片段",
		Long: `从在用 Server 记录生成 SSH config 片段，只输出到标准输出。
未填写的 user、port 不输出，由 SSH 自身配置决定。`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := loadQueryRepo(dirArg(args))
			if err != nil {
				return err
			}
			hosts := []sshHost{}
			for _, r := range repo.Records {
				if r.Kind != "Server" || r.Archived() {
					continue
				}
				spec := r.Value.(map[string]any)["spec"].(map[string]any)
				host := sshHost{Name: r.Name, HostName: spec["address"].(string)}
				// 仅输出字面量，拒绝换行、引号和 SSH 的 token 展开。
				if !sshAddress.MatchString(host.HostName) {
					return fmt.Errorf("%s: spec.address 不能安全写入 SSH config，请使用字面量 IP 或主机名", r.File)
				}
				ssh, _ := spec["ssh"].(map[string]any)
				host.User, _ = ssh["user"].(string)
				if _, exists := ssh["user"]; exists && !sshUser.MatchString(host.User) {
					return fmt.Errorf("%s: spec.ssh.user 不能安全写入 SSH config，请使用非空的字面量用户名", r.File)
				}
				switch port := ssh["port"].(type) {
				case int:
					host.Port = port
				case float64:
					host.Port = int(port) // schema 已保证是 1–65535 之间的整数
				}
				hosts = append(hosts, host)
			}
			sort.Slice(hosts, func(i, j int) bool { return hosts[i].Name < hosts[j].Name })
			out := cmd.OutOrStdout()
			if opts.json {
				return writeJSON(out, struct {
					Version string    `json:"version"`
					Hosts   []sshHost `json:"hosts"`
				}{jsonVersion, hosts})
			}
			var buf strings.Builder
			for i, host := range hosts {
				if i > 0 {
					buf.WriteByte('\n')
				}
				fmt.Fprintf(&buf, "Host %s\n  HostName %s\n", host.Name, host.HostName)
				if host.User != "" {
					fmt.Fprintf(&buf, "  User %s\n", host.User)
				}
				if host.Port != 0 {
					fmt.Fprintf(&buf, "  Port %d\n", host.Port)
				}
			}
			_, err = fmt.Fprint(out, buf.String())
			return err
		},
	}
}
