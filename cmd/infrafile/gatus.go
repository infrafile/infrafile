package main

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

type gatusEndpoint struct {
	Name       string   `yaml:"name"`
	Group      string   `yaml:"group"`
	URL        string   `yaml:"url"`
	Interval   string   `yaml:"interval"`
	Conditions []string `yaml:"conditions"`
}

func newGatusCmd(opts *globalOpts) *cobra.Command {
	return &cobra.Command{Use: "gatus [目录]", Short: "从显式 monitor 绑定生成 Gatus HTTPS 监控配置", Args: maxArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := loadQueryRepo(cmd.Context(), dirArg(args))
		if err != nil {
			return err
		}
		config, names, err := renderGatus(repo)
		if err != nil {
			return err
		}
		if opts.json {
			return writeJSON(cmd.OutOrStdout(), struct {
				Version   string   `json:"version"`
				Config    string   `json:"config"`
				Endpoints []string `json:"endpoints"`
			}{jsonVersion, config, names})
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), config)
		return err
	}}
}

func renderGatus(repo *ledger.Repo) (string, []string, error) {
	archivedServers := map[string]bool{}
	for _, r := range repo.Records {
		if r.Kind == "Server" && r.Archived() {
			archivedServers[r.Name] = true
		}
	}
	endpoints := []gatusEndpoint{}
	seen := map[string]bool{}
	for _, r := range repo.Records {
		if r.Kind != "Domain" || r.Archived() {
			continue
		}
		spec := r.Value.(map[string]any)["spec"].(map[string]any)
		bindings, _ := spec["bindings"].([]any)
		for _, item := range bindings {
			binding := item.(map[string]any)
			monitor, ok := binding["monitor"].(map[string]any)
			if !ok {
				continue
			}
			server, _ := binding["server"].(string)
			if archivedServers[server] {
				continue
			}
			name := strings.ToLower(binding["name"].(string))
			host := r.Name
			if name != "@" {
				host = name + "." + host
			}
			if !dnsZoneName(host) {
				return "", nil, fmt.Errorf("Domain/%s 的 monitor 主机名无效", r.Name)
			}
			path, interval, status := "/", "1m", any(200)
			if v, ok := monitor["path"].(string); ok {
				path = v
			}
			if v, ok := monitor["interval"].(string); ok {
				interval = v
			}
			if v, ok := monitor["status"]; ok {
				status = v
			}
			parsed, err := url.ParseRequestURI(path)
			if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "#") || parsed.IsAbs() {
				return "", nil, fmt.Errorf("Domain/%s 的 monitor.path 必须是以 / 开始且无片段的请求路径", r.Name)
			}
			if duration, err := time.ParseDuration(interval); err != nil || duration <= 0 {
				return "", nil, fmt.Errorf("Domain/%s 的 monitor.interval 必须是正时长，如 1m", r.Name)
			}
			if seen[host] {
				return "", nil, fmt.Errorf("Domain/%s 存在重复的 monitor 主机名 %s", r.Name, host)
			}
			seen[host] = true
			endpoints = append(endpoints, gatusEndpoint{host, r.Name, "https://" + host + path, interval, []string{fmt.Sprintf("[STATUS] == %v", status), "[CERTIFICATE_EXPIRATION] > 240h"}})
		}
	}
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Group != endpoints[j].Group {
			return endpoints[i].Group < endpoints[j].Group
		}
		return endpoints[i].Name < endpoints[j].Name
	})
	names := []string{}
	for _, e := range endpoints {
		names = append(names, e.Name)
	}
	data, err := yaml.Marshal(struct {
		Endpoints []gatusEndpoint `yaml:"endpoints"`
	}{endpoints})
	return string(data), names, err
}
