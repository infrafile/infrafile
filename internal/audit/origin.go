package audit

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
)

type originEvidence struct {
	record   observation.AddressRecord
	snapshot Snapshot
}

func prepareOrigin(ctx context.Context, in *Input, rule Rule) error {
	needed := map[string]bool{}
	for _, req := range rule.Observations {
		needed[req.Plugin] = true
	}
	in.origins = map[string][]originEvidence{}
	for _, s := range in.Snapshots {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !needed[s.Plugin] {
			continue
		}
		records, err := observation.AddressRecords(s.Plugin, s.Spec)
		if err != nil {
			return fmt.Errorf("%s: %w", s.File, err)
		}
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				return err
			}
			if s.Plugin == "cloudflare" && (record.Proxied == nil || *record.Proxied) {
				continue
			}
			in.origins[record.Host] = append(in.origins[record.Host], originEvidence{record, s})
		}
	}
	in.originFindings, in.originNotices = originFindings(in)
	return nil
}

func originHostname(name, domain string) string {
	if name == "@" {
		return strings.TrimSuffix(strings.ToLower(domain), ".")
	}
	return strings.ToLower(name + "." + strings.TrimSuffix(domain, "."))
}

func originFindings(in *Input) ([]Finding, []Notice) {
	servers := map[string]*ledger.Record{}
	apps := map[string]*ledger.Record{}
	for _, r := range in.Repo.Records {
		if r.Kind == "Server" && !r.Archived() {
			servers[r.Name] = r
		}
		if r.Kind == "App" && !r.Archived() {
			apps[r.Name] = r
		}
	}
	findings := []Finding{}
	notices := []Notice{}
	unknown := map[string]bool{}
	type key struct{ target, host, server, address, source, file, plugin, instance, typ string }
	seen := map[key]bool{}
	for _, r := range in.Repo.Records {
		if r.Kind != "Domain" || r.Archived() {
			continue
		}
		spec := field(r.Value, "spec")
		bindings, _ := field(spec, "bindings").([]any)
		for _, binding := range bindings {
			if field(binding, "resource") != nil {
				continue
			}
			name, _ := field(binding, "name").(string)
			host := originHostname(name, r.Name)
			serverName, _ := field(binding, "server").(string)
			appName, _ := field(binding, "app").(string)
			names := []string{serverName}
			fromApp := serverName == "" && appName != ""
			notice := func(message string) {
				key := r.Name + "\x00" + host + "\x00" + message
				if !unknown[key] {
					unknown[key] = true
					notices = append(notices, Notice{Code: "origin_exposure_unknown", Rule: "origin_exposure", Target: "Domain/" + r.Name, File: r.File, Message: host + "：" + message})
				}
			}
			if fromApp {
				app := apps[appName]
				if app == nil {
					continue
				}
				names = nil
				deployments, _ := field(field(app.Value, "spec"), "deployments").([]any)
				for _, deployment := range deployments {
					name, _ := field(deployment, "server").(string)
					if servers[name] != nil {
						names = append(names, name)
					}
				}
				if len(names) == 0 {
					notice("App/" + appName + " 没有在用 Server 部署，不能判断源站暴露")
				}
			}
			for _, serverName := range names {
				server := servers[serverName]
				if server == nil {
					continue
				}
				raw, _ := field(field(server.Value, "spec"), "address").(string)
				address, err := netip.ParseAddr(raw)
				if err != nil || address.Zone() != "" {
					if fromApp {
						notice("App/" + appName + " 的 Server/" + serverName + " 未声明明确 IP 地址，不能判断源站暴露")
					}
					continue
				}
				address = address.Unmap()
				if address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
					continue
				}
				add := func(record observation.AddressRecord, source, file, plugin, instance string, at *time.Time) {
					if record.Address != address {
						return
					}
					identity := key{r.Name, host, serverName, address.String(), source, file, plugin, instance, record.Type}
					if seen[identity] {
						return
					}
					seen[identity] = true
					var observedAt any
					if at != nil {
						observedAt = at.UTC().Format(time.RFC3339)
					}
					evidence := map[string]any{"hostname": host, "server": "Server/" + serverName, "address": address.String(), "record_type": record.Type, "source": source, "source_file": file, "observed_at": observedAt}
					if fromApp {
						evidence["app"] = "App/" + appName
					}
					if plugin != "" {
						evidence["plugin"], evidence["instance"] = plugin, instance
					}
					if record.Proxied != nil {
						evidence["proxied"] = *record.Proxied
					}
					findings = append(findings, Finding{Rule: "origin_exposure", Target: "Domain/" + r.Name, File: r.File, Message: fmt.Sprintf("%s 的 %s 地址 %s 直连 Server/%s（%s 证据）", host, record.Type, address, serverName, source), Evidence: evidence})
				}
				items, _ := field(field(spec, "dns"), "records").([]any)
				for _, item := range items {
					typ, _ := field(item, "type").(string)
					if typ != "A" && typ != "AAAA" {
						continue
					}
					owner, _ := field(item, "name").(string)
					if originHostname(owner, r.Name) != host {
						continue
					}
					proxied, isBool := field(item, "proxied").(bool)
					if proxied {
						continue
					}
					content, _ := field(item, "content").(string)
					addr, err := netip.ParseAddr(content)
					if err != nil {
						continue
					}
					record := observation.AddressRecord{Host: host, Type: typ, Address: addr.Unmap()}
					if isBool {
						record.Proxied = &proxied
					}
					add(record, "ledger", r.File, "", "", nil)
				}
				for _, e := range in.origins[host] {
					add(e.record, e.snapshot.Plugin, e.snapshot.File, e.snapshot.Plugin, e.snapshot.Instance, e.snapshot.ObservedAt)
				}
			}
		}
	}
	return findings, notices
}

var originExposure = Rule{
	ID: "origin_exposure", Description: "绑定 Server 或 App 部署的域名地址直连源站",
	Observations: []observation.Requirement{{Plugin: "cloudflare", Services: []string{"zones.dns_records"}}, {Plugin: "dns", Services: []string{"host", "addresses"}}},
	Ready:        func(*Input) error { return nil }, Eval: func(in *Input) []Finding { return in.originFindings },
}
