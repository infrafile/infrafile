package audit

import (
	"context"
	"net/netip"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
)

func (i *inspector) services(p, name string) []string {
	for _, req := range observation.Requirements() {
		if req.Plugin != p {
			continue
		}
		if configured, ok := i.config[p][name]["services"].([]any); ok {
			services := []string{}
			for _, service := range configured {
				if s, ok := service.(string); ok && contains(req.Services, s) {
					services = append(services, s)
				}
			}
			return services
		}
		return req.Services
	}
	return nil
}

func (i *inspector) instanceChecks(ctx context.Context) error {
	for p, instances := range i.config {
		for name := range instances {
			if err := ctx.Err(); err != nil {
				return err
			}
			code, message := i.qualify(p, name)
			s := i.snaps[p+"/"+name]
			i.add(CheckScope{"stale_state", p + "/" + name, p, name, "", "freshness"}, code, message, s)
			services := i.services(p, name)
			if len(services) == 0 {
				if p != "dns" && p != "tls" {
					i.add(CheckScope{"coverage", p + "/" + name, p, name, "", "checks"}, "unsupported", "实例没有已实现的资产清单检查契约", s)
				}
				continue
			}
			for _, service := range services {
				complete := s.Spec[service] != nil
				// Mapping validates scalar SSH/WHOIS contracts as well as cloud collections.
				if s.Spec[service] != nil {
					_, _, err := observation.Map(p, name, s.File, s.Spec)
					if err != nil {
						return err
					}
				}
				c, m := code, message
				if c == "checked" && !complete {
					c, m = "incomplete", "快照未返回声明服务的完整集合"
				}
				for _, rule := range []string{"orphan", "zombie"} {
					i.add(CheckScope{rule, p + "/" + name, p, name, service, "inventory"}, c, m, s)
				}
			}
		}
	}
	return nil
}

func (i *inspector) assetChecks(r *ledger.Record) error {
	target := r.Kind + "/" + r.Name
	if r.Archived() {
		found := false
		for p, instances := range i.config {
			for name := range instances {
				for _, service := range i.services(p, name) {
					matches := r.Kind == "Domain" && (service == "domains" || service == "zones" || p == "whois") || r.Kind == "Resource" && (service == "cdn" || service == "zones") || r.Kind == "Server" && p == "ssh"
					if !matches {
						continue
					}
					found = true
					code, message := i.qualify(p, name)
					s := i.snaps[p+"/"+name]
					if code == "checked" && s.Spec[service] == nil {
						code, message = "incomplete", "归档检查缺少完整服务集合"
					}
					i.add(CheckScope{"zombie", target, p, name, service, "inventory"}, code, message, s)
				}
			}
		}
		if !found {
			i.add(CheckScope{"zombie", target, "", "", "", "inventory"}, "not_configured", "归档对象没有可用的资产清单检查范围")
		}
		return nil
	}
	spec, _ := field(r.Value, "spec").(map[string]any)
	switch r.Kind {
	case "Domain", "Resource":
		w, ok := i.watches[target]
		if !ok {
			message := "未声明 audit.watch；不能从账号或 provider 推断检查实例"
			count := 0
			for p, instances := range i.config {
				for name := range instances {
					for _, service := range i.services(p, name) {
						if r.Kind == "Domain" && (service == "domains" || service == "zones") || r.Kind == "Resource" && spec["provider"] == p && (service == "cdn" || service == "zones") {
							count++
						}
					}
				}
			}
			code := "not_configured"
			if count > 1 {
				code = "ambiguous_scope"
				message = "存在多个可能的账号/实例；必须显式声明 audit.watch，未自动合并"
			}
			provider, _ := spec["provider"].(string)
			if r.Kind == "Resource" && (!contains([]string{"aliyun", "tencent", "baidu", "ctyun", "cloudflare"}, provider) || !(spec["type"] == "cdn" || spec["provider"] == "cloudflare" && spec["type"] == "dns-zone")) {
				code, message = "unsupported", "此类 Resource 没有 missing 检查契约；原运维流程继续负责"
			}
			i.add(CheckScope{"missing", target, "", "", "", "presence"}, code, message)
		} else {
			s := i.snaps[w.Plugin+"/"+w.Instance]
			_, complete, err := observation.WatchRecords(w.Plugin, w.Instance, w.Service, s.Spec)
			if err != nil {
				return err
			}
			code, message := i.qualify(w.Plugin, w.Instance)
			if code == "checked" {
				code, message = missingQualification(i.in, w, s, complete, i.baselines[target])
			}
			i.add(CheckScope{"missing", target, w.Plugin, w.Instance, w.Service, "presence"}, code, message, s)
		}
		if r.Kind == "Domain" {
			return i.bindingChecks(r)
		}
		if spec["type"] == "cdn" || spec["provider"] == "cloudflare" && spec["type"] == "dns-zone" {
			return i.cdnChecks(r)
		}
	case "Server":
		found := false
		address, _ := spec["address"].(string)
		for name, cfg := range i.config["ssh"] {
			host, _ := cfg["host"].(string)
			if normalizedHost(host) != normalizedHost(address) {
				continue
			}
			found = true
			s := i.snaps["ssh/"+name]
			code, message := i.qualify("ssh", name)
			if code == "checked" {
				mapped, _, err := observation.Map("ssh", name, s.File, s.Spec)
				if err != nil {
					return err
				}
				if len(mapped) != 1 || observation.IdentityOf("Server", "", mapped[0].Spec) != observation.IdentityOf("Server", r.Name, spec) {
					code, message = "identity_mismatch", "SSH 观测身份与显式 host 配置不一致"
				}
			}
			i.add(CheckScope{"coverage", target, "ssh", name, "host", "ssh"}, code, message, s)
		}
		if !found {
			i.add(CheckScope{"coverage", target, "", "", "", "ssh"}, "not_configured", "没有与 Server.address 明确匹配的 SSH 实例；不代表应用健康")
		}
	default:
		i.add(CheckScope{"coverage", target, "", "", "", "checks"}, "unsupported", "此类资产没有内置健康检查契约；相关旧流程继续负责，不能据关系存在推断健康")
	}
	return nil
}

func (i *inspector) cdnChecks(r *ledger.Record) error {
	target := "Resource/" + r.Name
	spec, _ := field(r.Value, "spec").(map[string]any)
	w, ok := i.watches[target]
	aspects := []string{"status", "origin"}
	if expected, ok := spec["origin"].(map[string]any); ok {
		aspects = []string{"status", "origin/hosts"}
		for _, field := range []string{"port", "protocol"} {
			if expected[field] != nil {
				aspects = append(aspects, "origin/"+field)
			}
		}
	}
	for _, aspect := range aspects {
		scope := CheckScope{"cdn_drift", target, w.Plugin, w.Instance, w.Service, aspect}
		if !ok {
			i.add(scope, "not_configured", "CDN 检查需要 audit.watch 显式绑定实例")
			continue
		}
		s := i.snaps[w.Plugin+"/"+w.Instance]
		code, message := i.qualify(w.Plugin, w.Instance)
		if aspect != "status" {
			if spec["origin"] == nil {
				i.add(scope, "not_configured", "未声明期望回源；不从观测推断 desired")
				continue
			}
			if w.Config["collect_origin"] != true {
				i.add(scope, "not_configured", "实例尚未显式启用 collect_origin")
				continue
			}
			if code == "checked" {
				if !i.in.cdnOriginCoverage[target][strings.TrimPrefix(aspect, "origin/")] {
					code, message = "observation_unknown", "回源观测未明确返回此期望方面，或未唯一匹配全局范围；不推断默认值"
				}
			}
		} else {
			states, supported := cdnStates[w.Plugin]
			if !supported {
				i.add(scope, "unsupported", "此厂商/产品没有停用状态对照规则", s)
				continue
			}
			if code == "checked" {
				items, _ := s.Spec[w.Service].([]any)
				matches := 0
				for _, item := range items {
					identities, _, err := observation.WatchRecords(w.Plugin, w.Instance, w.Service, map[string]any{w.Service: []any{item}})
					if err != nil {
						return err
					}
					if !w.Seen(identities) {
						continue
					}
					matches++
					status, _ := field(item, "status").(string)
					if _, known := states[status]; !known {
						code, message = "observation_unknown", "CDN 状态未纳入明确映射"
					}
				}
				if matches == 0 {
					code, message = "not_observed", "未找到匹配此目标的状态观测"
				}
			}
		}
		i.add(scope, code, message, s)
	}
	return nil
}

func (i *inspector) bindingChecks(r *ledger.Record) error {
	bindings, _ := field(field(r.Value, "spec"), "bindings").([]any)
	target := "Domain/" + r.Name
	if len(bindings) == 0 {
		i.add(CheckScope{"origin_exposure", target, "", "", "", "bindings"}, "missing_relationship", "没有明确绑定关系，不能确定要比较的源站")
		return nil
	}
	for _, binding := range bindings {
		name, _ := field(binding, "name").(string)
		host := originHostname(name, r.Name)
		server, _ := field(binding, "server").(string)
		app, _ := field(binding, "app").(string)
		resource, _ := field(binding, "resource").(string)
		aspect := "binding/server/" + server + "/app/" + app + "/resource/" + resource + "/" + host
		scope := CheckScope{Rule: "origin_exposure", Target: target, Aspect: aspect}
		if field(binding, "resource") != nil {
			i.add(scope, "unsupported", "Resource 绑定未展开为 Server 源站；保留原 CDN/路由检查职责")
			continue
		}
		servers := []string{}
		if server != "" {
			servers = append(servers, server)
		} else if app != "" {
			for _, a := range i.in.Repo.Records {
				if a.Kind == "App" && a.Name == app && !a.Archived() {
					deployments, _ := field(field(a.Value, "spec"), "deployments").([]any)
					for _, d := range deployments {
						if name, ok := field(d, "server").(string); ok {
							servers = append(servers, name)
						}
					}
				}
			}
		}
		if len(servers) == 0 {
			i.add(scope, "missing_relationship", "绑定没有可比较的在用 Server 部署")
			continue
		}
		valid := true
		for _, name := range servers {
			found := false
			for _, s := range i.in.Repo.Records {
				if s.Kind == "Server" && s.Name == name && !s.Archived() {
					found = true
					raw, _ := field(field(s.Value, "spec"), "address").(string)
					ip, err := netip.ParseAddr(raw)
					valid = valid && err == nil && ip.Zone() == "" && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
				}
			}
			valid = valid && found
		}
		if !valid {
			i.add(scope, "address_unknown", "绑定的某个 Server 没有可比较的公网 IP；不解析主机名或缩小部署分母")
			continue
		}
		found := false
		for name, cfg := range i.config["dns"] {
			configured, _ := cfg["host"].(string)
			if normalizedHost(configured) != host {
				continue
			}
			found = true
			c := scope
			c.Plugin, c.Instance, c.Service = "dns", name, "addresses"
			s := i.snaps["dns/"+name]
			code, message := i.qualify("dns", name)
			if code == "checked" {
				observed, _ := s.Spec["host"].(string)
				if normalizedHost(observed) != host {
					code, message = "identity_mismatch", "DNS 快照 host 与显式实例配置不一致"
				}
			}
			i.add(c, code, message, s)
		}
		if w, ok := i.watches[target]; ok && w.Plugin == "cloudflare" {
			found = true
			c := scope
			c.Plugin, c.Instance, c.Service = w.Plugin, w.Instance, "zones.dns_records"
			s := i.snaps[w.Plugin+"/"+w.Instance]
			code, message := i.qualify(w.Plugin, w.Instance)
			if code == "checked" {
				records, err := observation.AddressRecords(w.Plugin, s.Spec)
				if err != nil {
					return err
				}
				matched := false
				for _, record := range records {
					matched = matched || record.Host == host && record.Proxied != nil
				}
				if !matched {
					code, message = "observation_unknown", "没有此绑定明确的地址与代理标记；不跟踪 CNAME"
				}
			}
			i.add(c, code, message, s)
		}
		if !found {
			i.add(scope, "not_configured", "未配置此 hostname 的 DNS 实例或明确绑定的 Cloudflare 观测")
		}
	}
	return nil
}
