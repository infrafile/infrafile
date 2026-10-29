package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/secret"
	"github.com/infrafile/infrafile/protocol"
)

type DoctorCheck struct {
	Check       string   `json:"check"`
	Subject     string   `json:"subject,omitempty"`
	Plugin      string   `json:"plugin,omitempty"`
	Instance    string   `json:"instance,omitempty"`
	Status      string   `json:"status"`
	Code        string   `json:"code"`
	Message     string   `json:"message"`
	Services    []string `json:"services,omitempty"`
	Permissions []string `json:"required_permissions,omitempty"`
}

type doctorBudgetKey struct{}

// Doctor requires a validated repo. Only the explicitly selected builtin may run online.
func Doctor(ctx context.Context, repo *ledger.Repo, selected, instance string, online bool, timeout time.Duration) ([]DoctorCheck, error) {
	if timeout <= 0 || timeout > 2*time.Minute || (selected == "") != (instance == "") || online && selected == "" {
		return nil, fmt.Errorf("在线预检需要指定插件和实例；时限须在 0 到 2 分钟之间")
	}
	plugins, _ := repo.Config.Value.(map[string]any)["plugins"].(map[string]any)
	names := sortedKeys(plugins)
	if selected != "" {
		p, ok := plugins[selected].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("插件未启用")
		}
		if _, ok := p["instances"].(map[string]any)[instance]; !ok {
			return nil, fmt.Errorf("实例未启用")
		}
		names = []string{selected}
	}
	checks := []DoctorCheck{}
	resolver := secret.Resolver{Dir: repo.Dir}
	if selected == "" {
		for _, record := range repo.Records {
			spec := record.Value.(map[string]any)["spec"].(map[string]any)
			refs, _ := spec["secrets"].(map[string]any)
			for _, key := range sortedKeys(refs) {
				check := DoctorCheck{Check: "credential-reference", Subject: record.Kind + "/" + record.Name, Status: "passed", Code: "offline-ready", Message: "引用可定位；未请求凭据值"}
				checkReference(ctx, resolver, refs[key].(string), &check)
				checks = append(checks, check)
			}
		}
	}
	for _, name := range names {
		instances := plugins[name].(map[string]any)["instances"].(map[string]any)
		keys := sortedKeys(instances)
		if selected != "" {
			keys = []string{instance}
		}
		for _, key := range keys {
			check := DoctorCheck{Check: "instance", Plugin: name, Instance: key, Status: "passed", Code: "offline-ready", Message: "配置和引用通过；未验证连接、权限或业务健康"}
			cfg := instances[key].(map[string]any)
			refs, services, err := doctorConfig(name, cfg)
			check.Services = services
			capability, builtin := builtinCapability(name)
			for _, service := range capability.Services {
				if service.Action == "" && slices.Contains(services, service.Service) {
					check.Permissions = append(check.Permissions, service.Permissions...)
				}
			}
			if err != nil {
				check.Status, check.Code, check.Message = "failed", "invalid-config", "插件配置无效；请检查第一方插件文档"
			} else if !builtin && name != "example" {
				check.Status, check.Code, check.Message = "unknown", "external-unverified", "外部插件配置与只读能力未知；预检不启动外部程序"
				if online {
					check.Status, check.Code, check.Message = "failed", "unsupported", "外部插件没有受信任的只读预检契约，未启动程序"
				}
				if _, err := exec.LookPath(name); err != nil {
					check.Status, check.Code, check.Message = "failed", "missing-executable", "找不到启用的插件程序"
				}
			} else {
				for _, ref := range refs {
					candidate := check
					checkReference(ctx, resolver, ref, &candidate)
					if online && candidate.Code == "online-required" {
						continue
					}
					if candidate.Status == "failed" || check.Status == "passed" {
						check = candidate
					}
				}
				if name == "ssh" {
					if _, err := exec.LookPath("ssh"); err != nil {
						check.Status, check.Code, check.Message = "failed", "missing-executable", "找不到 SSH 程序"
					}
				}
				if online && check.Status == "passed" {
					checkOnline(ctx, repo.Dir, cfg, timeout, &check)
				}
			}
			if ctx.Err() != nil {
				check.Status, check.Code, check.Message = "unknown", "cancelled-or-timeout", "预检已取消或超时"
			}
			checks = append(checks, check)
		}
	}
	return checks, nil
}

func checkReference(ctx context.Context, resolver secret.Resolver, ref string, check *DoctorCheck) {
	err := resolver.CheckOffline(ctx, ref)
	if err == nil {
		return
	}
	check.Status, check.Code, check.Message = "unknown", "reference-unavailable", "无法确认引用可访问性；未请求凭据值"
	var e *secret.Error
	if errors.As(err, &e) {
		switch e.Code {
		case "not-found":
			check.Status, check.Code, check.Message = "failed", "missing-reference", "凭据引用不存在"
		case "invalid-ref", "invalid-file", "invalid-value", "unsupported":
			check.Status, check.Code, check.Message = "failed", "invalid-reference", "凭据引用或加密格式不受支持"
		case "online-required":
			check.Code, check.Message = "online-required", "引用格式有效；字段可访问性需要显式在线检查"
		}
	}
}

func checkOnline(parent context.Context, dir string, cfg map[string]any, timeout time.Duration, check *DoctorCheck) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	budget := 20
	ctx = context.WithValue(ctx, doctorBudgetKey{}, &budget)
	client, err := Open(ctx, check.Plugin, dir)
	if err == nil {
		defer client.Close()
		var data json.RawMessage
		data, err = Sync(ctx, client, protocol.SyncParams{Instance: check.Instance, Config: cfg})
		if err == nil && check.Plugin == "tls" {
			var result struct {
				Verified bool `json:"verified"`
			}
			if json.Unmarshal(data, &result) != nil || !result.Verified {
				err = failure("verification", "TLS 信任验证未通过")
			}
		}
	}
	check.Status, check.Code, check.Message = "passed", "read-succeeded", "已配置的只读采集完成；不证明完整权限、写权限或业务健康"
	if err == nil && ctx.Err() == nil {
		return
	}
	check.Status, check.Code, check.Message = "unknown", "unknown", "只读检查未完成；未保留或输出远端错误原文"
	var e *Error
	if ctx.Err() != nil {
		check.Code, check.Message = "cancelled-or-timeout", "在线检查超时或已取消"
	} else if errors.As(err, &e) {
		switch {
		case e.HTTPStatus == 401:
			check.Status, check.Code, check.Message = "failed", "unauthorized", "远端返回 HTTP 401"
		case e.HTTPStatus == 403:
			check.Status, check.Code, check.Message = "failed", "permission-or-feature-denied", "远端返回 HTTP 403；无法区分权限、功能或访问策略限制"
		case e.Unreachable:
			check.Status, check.Code, check.Message = "failed", "unreachable", "网络或名称解析不可达"
		case e.Code == "credential":
			check.Code, check.Message = "credential-unavailable", "凭据无法解析；未判断为云端认证失败"
		case e.Code == "budget":
			check.Code, check.Message = "request-budget", "已达到每实例 20 个 HTTP 请求上限"
		case e.Code == "verification":
			check.Status, check.Code, check.Message = "failed", "verification-failed", "连接完成但 TLS 信任验证未通过"
		}
	}
}

func doctorConfig(name string, config map[string]any) ([]string, []string, error) {
	switch name {
	case "aliyun", "baidu", "tencent", "ctyun":
		defaults := []string{"domains", "cdn"}
		if name == "ctyun" {
			defaults = []string{"cdn"}
		}
		cfg, err := parseCloudConfig(name, config, defaults)
		refs := []string{cfg.ID, cfg.Secret}
		if cfg.Token != "" {
			refs = append(refs, cfg.Token)
		}
		services := cfg.Services
		if cfg.CollectOrigin {
			services = append(slices.Clone(services), "cdn-origin")
		}
		return refs, services, err
	case "cloudflare":
		var cfg cfConfig
		err := decodeConfig(config, &cfg)
		services := []string{"zones", "dns"}
		if cfg.CollectOrigin {
			services = append(services, "cdn-origin")
		}
		return []string{cfg.APIToken}, services, err
	case "dns":
		_, err := parseDNSConfig(config)
		return nil, []string{"dns"}, err
	case "tls":
		_, err := parseTLSConfig(config)
		return nil, []string{"tls"}, err
	case "ssh":
		_, err := parseSSHConfig(config)
		return nil, []string{"host"}, err
	case "whois":
		_, err := parseWHOISConfig(config)
		return nil, []string{"found"}, err
	case "example":
		return nil, nil, decodeConfig(config, &struct{}{})
	default:
		return nil, nil, nil
	}
}
