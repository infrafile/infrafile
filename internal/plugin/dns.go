package plugin

import (
	"context"
	"net"
	"regexp"
	"slices"
	"sort"
	"strings"
)

var hostnameLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if !hostnameLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func probeDNS(ctx context.Context, config map[string]any) (any, error) {
	var cfg struct {
		Host string `json:"host"`
	}
	if err := decodeConfig(config, &cfg); err != nil {
		return nil, err
	}
	if !validHost(cfg.Host) || net.ParseIP(cfg.Host) != nil {
		return nil, failure("config", "DNS host 必须是 ASCII 域名")
	}
	host := strings.TrimSuffix(strings.ToLower(cfg.Host), ".")
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host+".")
	if err != nil {
		return nil, failure("probe", "DNS 地址查询失败")
	}
	canonical, err := net.DefaultResolver.LookupCNAME(ctx, host+".")
	if err != nil {
		return nil, failure("probe", "DNS CNAME 查询失败")
	}
	ips := make([]string, 0, len(addresses))
	for _, addr := range addresses {
		ips = append(ips, addr.IP.String())
	}
	sort.Strings(ips)
	return map[string]any{"host": host, "addresses": slices.Compact(ips), "canonical_name": strings.TrimSuffix(canonical, ".")}, nil
}
