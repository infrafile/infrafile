package importer

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
	"github.com/infrafile/infrafile/protocol"
)

var pluginName = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
var unsafeName = regexp.MustCompile(`[^a-z0-9.-]+`)

func State(dir string) ([]Entry, []Notice, error) {
	repo, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, errors.New("无法打开数据仓")
	}
	defer repo.Close()
	info, err := repo.Lstat("state")
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, []Notice{}, nil
	}
	if err != nil || !info.IsDir() {
		return nil, nil, errors.New("state 必须是目录，不能是符号链接")
	}
	root, err := repo.OpenRoot("state")
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	if _, err := root.Lstat(".sync.lock"); !errors.Is(err, os.ErrNotExist) {
		return nil, nil, errors.New("state 正在同步或锁仍存在，请在同步结束后导入")
	}
	plugins, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, nil, errors.New("无法列出 state")
	}
	entries, notices := []Entry{}, []Notice{}
	for _, p := range plugins {
		if strings.HasPrefix(p.Name(), ".") || p.Name() == "_sync.yaml" {
			continue
		}
		if !p.IsDir() || !pluginName.MatchString(p.Name()) || len(p.Name()) > 64 {
			return nil, nil, errors.New("state 插件路径必须是合法名称的目录")
		}
		files, err := fs.ReadDir(root.FS(), p.Name())
		if err != nil {
			return nil, nil, errors.New("无法列出插件快照")
		}
		for _, f := range files {
			if strings.HasPrefix(f.Name(), ".") {
				continue
			}
			if !strings.HasSuffix(f.Name(), ".yaml") {
				return nil, nil, errors.New("快照必须使用 .yaml 扩展名")
			}
			rel := p.Name() + "/" + f.Name()
			info, err := root.Lstat(rel)
			if err != nil || !info.Mode().IsRegular() {
				return nil, nil, errors.New("快照必须是普通文件，不能是符号链接")
			}
			data, err := root.ReadFile(rel)
			if err != nil {
				return nil, nil, errors.New("无法读取快照")
			}
			source := "state/" + rel
			doc, problems := ledger.ParseDocument(source, data)
			if doc == nil || len(problems) != 0 {
				return nil, nil, fmt.Errorf("%s: 快照 YAML 格式错误", source)
			}
			if !validate.SnapshotSafe(doc.Value) {
				return nil, nil, errors.New("快照含敏感字段或疑似凭据，已拒绝导入")
			}
			raw, err := json.Marshal(doc.Value)
			if err != nil || protocol.Validate("snapshot", raw) != nil {
				return nil, nil, fmt.Errorf("%s: 快照信封不符合协议", source)
			}
			value := doc.Value.(map[string]any)
			instance := value["metadata"].(map[string]any)["name"].(string)
			if f.Name() != instance+".yaml" {
				return nil, nil, errors.New("快照实例名与文件名不符")
			}
			mapped, skipped, err := mapState(p.Name(), instance, source, value["spec"].(map[string]any))
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %s", source, err)
			}
			entries = append(entries, mapped...)
			notices = append(notices, skipped...)
		}
	}
	return entries, notices, nil
}

func mapState(plugin, instance, source string, spec map[string]any) ([]Entry, []Notice, error) {
	entries := []Entry{}
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
		entries = append(entries, draft(source, "Domain", name, fields))
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
		entries = append(entries, draft(source, "Resource", resourceName(plugin, instance, kind, name), fields))
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
		entries = append(entries, draft(source, "Server", instance, fields))
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
	if len(slug) > 244 {
		slug = strings.TrimRight(slug[:244], ".-")
	}
	return fmt.Sprintf("%s-%x", slug, digest[:4])
}
