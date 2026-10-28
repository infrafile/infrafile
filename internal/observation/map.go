// Package observation 将插件快照映射为资产字段，供离线导入与审计共用。
package observation

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Record 保留既有导入的记录名和字段；审计匹配另用自然身份，不依赖 Name。
type Record struct {
	Kind string
	Name string
	Spec map[string]any
}

type Notice struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

func record(kind, name string, spec map[string]any) Record { return Record{kind, name, spec} }

var unsafeName = regexp.MustCompile(`[^a-z0-9.-]+`)

// Map 使用既有 import state 的字段、校验及主键生成约定，不推断资产状态。
func Map(plugin, instance, source string, spec map[string]any) ([]Record, []Notice, error) {
	entries := []Record{}
	invalid := errors.New("观测字段缺失、类型错误或名称无效")
	addDomain := func(item map[string]any, registrar string) error {
		name, ok := domainName(item["name"])
		if !ok {
			return invalid
		}
		fields := map[string]any{}
		if registrar != "" {
			fields["registrar"] = registrar
		}
		if date, ok := item["expires_at"]; ok {
			if _, ok := date.(string); !ok {
				return invalid
			}
			if date != "" {
				fields["expires_at"] = date
			}
		}
		entries = append(entries, record("Domain", name, fields))
		return nil
	}
	addResource := func(item map[string]any, kind string) error {
		host, ok := item["name"].(string)
		if !ok {
			return invalid
		}
		name, ok := domainName(strings.TrimPrefix(host, "*."))
		if !ok {
			return invalid
		}
		if strings.HasPrefix(host, "*.") {
			name = "*." + name
		}
		fields := map[string]any{"provider": plugin, "type": kind, "endpoint": name}
		if id, ok := item["id"]; ok {
			if _, ok := id.(string); !ok {
				return invalid
			}
			if id != "" {
				fields["resource_id"] = id
			}
		}
		entries = append(entries, record("Resource", resourceName(plugin, instance, kind, name), fields))
		return nil
	}
	switch plugin {
	case "aliyun", "tencent", "baidu", "ctyun":
		found := false
		for _, service := range []string{"domains", "cdn"} {
			value, ok := spec[service]
			if !ok {
				continue
			}
			found = true
			if plugin == "ctyun" && service == "domains" {
				return nil, nil, errors.New("天翼注册域名观测暂不支持")
			}
			items, ok := value.([]any)
			if !ok {
				return nil, nil, invalid
			}
			for _, value := range items {
				item, ok := value.(map[string]any)
				if !ok {
					return nil, nil, invalid
				}
				var err error
				if service == "domains" {
					err = addDomain(item, plugin)
				} else {
					err = addResource(item, "cdn")
				}
				if err != nil {
					return nil, nil, err
				}
			}
		}
		if !found {
			return nil, nil, invalid
		}
	case "cloudflare":
		items, ok := spec["zones"].([]any)
		if !ok {
			return nil, nil, invalid
		}
		for _, value := range items {
			item, ok := value.(map[string]any)
			if !ok {
				return nil, nil, invalid
			}
			if err := addResource(item, "dns-zone"); err != nil {
				return nil, nil, err
			}
		}
	case "whois":
		found, ok := spec["found"].(bool)
		if !ok {
			return nil, nil, invalid
		}
		if !found {
			return entries, []Notice{{source, "not-found"}}, nil
		}
		registrar := ""
		if value, ok := spec["registrar"]; ok {
			registrar, ok = value.(string)
			if !ok {
				return nil, nil, invalid
			}
		}
		item := map[string]any{"name": spec["domain"]}
		if date, ok := spec["expires_at"]; ok {
			item["expires_at"] = date
		}
		if err := addDomain(item, registrar); err != nil {
			return nil, nil, err
		}
	case "ssh":
		host, ok := spec["host"].(string)
		if !ok || host == "" || strings.ContainsAny(host, "\r\n\x00") {
			return nil, nil, invalid
		}
		fields := map[string]any{"address": host}
		system, ok := spec["system"].(string)
		if !ok || system == "" {
			return nil, nil, invalid
		}
		release, ok := spec["release"].(string)
		if !ok || release == "" {
			return nil, nil, invalid
		}
		fields["hardware"] = map[string]any{"os": system + " " + release}
		entries = append(entries, record("Server", instance, fields))
	case "dns", "tls":
		return entries, []Notice{{source, "observation-only"}}, nil
	default:
		return entries, []Notice{{source, "unsupported-plugin"}}, nil
	}
	return entries, nil, nil
}

func domainName(value any) (string, bool) {
	name, ok := value.(string)
	if !ok {
		return "", false
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if len(name) == 0 || len(name) > 253 {
		return "", false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", false
			}
		}
	}
	return name, true
}

func resourceName(provider, instance, kind, name string) string {
	parts := []string{provider, instance, kind, name}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	slug := strings.Trim(unsafeName.ReplaceAllString(strings.Join(parts, "-"), "-"), ".-")
	if len(slug) > 241 {
		slug = strings.TrimRight(slug[:241], ".-")
	}
	return fmt.Sprintf("%s-%x", slug, digest[:4])
}
