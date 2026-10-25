package plugin

import (
	"context"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
)

func probeWHOIS(ctx context.Context, config map[string]any) (any, error) {
	var cfg struct {
		Domain string `json:"domain"`
		Server string `json:"server"`
	}
	if err := decodeConfig(config, &cfg); err != nil {
		return nil, err
	}
	if !validHost(cfg.Domain) || net.ParseIP(cfg.Domain) != nil {
		return nil, failure("config", "WHOIS domain 必须是 ASCII 域名")
	}
	domain := strings.TrimSuffix(strings.ToLower(cfg.Domain), ".")
	server := cfg.Server
	if server == "" {
		root, err := whoisQuery(ctx, "whois.iana.org:43", domain)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(root, "\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok && (strings.TrimSpace(key) == "refer" || strings.TrimSpace(key) == "whois") {
				server = strings.TrimSpace(value)
				break
			}
		}
		if server == "" {
			return nil, failure("probe", "IANA 未提供该域名的 WHOIS 服务")
		}
	}
	host, port, err := net.SplitHostPort(server)
	if err != nil {
		host, port = server, "43"
	}
	portNumber, err := strconv.Atoi(port)
	if !validHost(host) || err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, failure("config", "WHOIS server 必须是主机名或主机名:端口")
	}
	server = net.JoinHostPort(host, port)
	text, err := whoisQuery(ctx, server, domain)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"domain": domain, "server": server, "found": true}
	for _, marker := range []string{"no match for", "not found", "no entries found", "no data found"} {
		if strings.Contains(strings.ToLower(text), marker) {
			result["found"] = false
			return result, nil
		}
	}
	name := ""
	nameservers, statuses := []string{}, []string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "domain name", "domain":
			name = strings.TrimSuffix(strings.ToLower(value), ".")
		case "registrar":
			result["registrar"] = value
		case "name server", "nserver":
			nameservers = append(nameservers, strings.TrimSuffix(strings.ToLower(strings.Fields(value)[0]), "."))
		case "domain status", "status":
			statuses = append(statuses, strings.Fields(value)[0])
		case "registry expiry date", "registrar registration expiration date", "expiration date", "expiration time", "paid-till", "expire date":
			parsed := false
			for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05", "02-Jan-2006", "2006.01.02 15:04:05"} {
				if date, err := time.Parse(layout, value); err == nil {
					result["expires_at"] = date.Format("2006-01-02")
					parsed = true
					break
				}
			}
			if !parsed {
				return nil, failure("protocol", "WHOIS 到期日期格式无法识别")
			}
		}
	}
	if name != domain {
		return nil, failure("protocol", "WHOIS 响应未包含匹配的域名")
	}
	slices.Sort(nameservers)
	slices.Sort(statuses)
	result["nameservers"], result["statuses"] = slices.Compact(nameservers), slices.Compact(statuses)
	return result, nil
}

func whoisQuery(ctx context.Context, address, query string) (string, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", failure("probe", "WHOIS 连接失败")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	if _, err := io.WriteString(conn, query+"\r\n"); err != nil {
		return "", failure("probe", "WHOIS 查询失败")
	}
	data, err := io.ReadAll(io.LimitReader(conn, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return "", failure("probe", "WHOIS 响应读取失败或超过 1 MiB")
	}
	return string(data), nil
}
