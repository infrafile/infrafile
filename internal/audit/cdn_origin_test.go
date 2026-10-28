package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCDNOriginEvidence(t *testing.T) {
	for _, name := range []string{"equal", "hosts", "port", "protocol", "unknown-port", "unknown-protocol", "conditional", "missing-origin", "invalid-host", "no-watch", "disabled", "no-age", "unverified", "stale", "future", "boundary", "zero-age", "huge-age", "other-account", "other-id", "multi-area", "archived", "waived", "changed-expectation"} {
		t.Run(name, func(t *testing.T) {
			config := `plugins:
  aliyun:
    instances:
      home: {services: [cdn], collect_origin: true}
      other: {services: [cdn], collect_origin: true}
audit:
  watch:
    - {target: Resource/edge, plugin: aliyun, instance: home, service: cdn}
`
			metadata := "name: edge"
			want, unknown := false, false
			port, protocol, host, scope := 8443, "https", "ORIGIN.EXAMPLE.COM.", "global"
			stamp, age, verified, instance, id := now, days(7), true, "home", "same"
			switch name {
			case "hosts", "waived", "boundary", "zero-age", "huge-age":
				host, want = "changed.example.net", true
			case "port":
				port, want = 443, true
			case "protocol":
				protocol, want = "http", true
			case "unknown-port":
				port, unknown = 0, true
			case "unknown-protocol":
				protocol, unknown = "", true
			case "conditional":
				scope, unknown = "conditional", true
			case "missing-origin":
				unknown = true
			case "invalid-host":
				host, unknown = "https://user:private@origin.example.com", true
			case "no-watch":
				config, unknown = strings.Split(config, "audit:")[0], true
			case "disabled":
				config, unknown = strings.ReplaceAll(config, "collect_origin: true", "collect_origin: false"), true
			case "no-age":
				age, unknown = nil, true
			case "unverified":
				verified, unknown = false, true
			case "stale":
				stamp, unknown = now.Add(-7*24*time.Hour-time.Second), true
			case "future":
				stamp, unknown = now.Add(time.Second), true
			case "other-account":
				instance, unknown = "other", true
			case "other-id":
				id, unknown = "different", true
			case "multi-area":
				unknown = true
			case "archived":
				metadata += ", archived: {date: 2026-10-28, reason: retired}"
				config = strings.Split(config, "audit:")[0]
			case "changed-expectation":
				want = true
			}
			if name == "boundary" {
				stamp = now.Add(-7 * 24 * time.Hour)
			}
			if name == "zero-age" {
				age = days(0)
			}
			if name == "huge-age" {
				age = days(int(^uint(0) >> 1))
			}
			if name == "waived" {
				metadata += ", waivers: [{rule: cdn_drift, reason: planned-migration}]"
			}
			expected := "{hosts: ['origin.example.com', '::ffff:192.0.2.1', 'ORIGIN.EXAMPLE.COM.'], port: 8443, protocol: https}"
			if name == "changed-expectation" {
				expected = strings.ReplaceAll(expected, "8443", "443")
			}
			r := repo(t, config, map[string]string{"inventory/resources/edge.yaml": "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {" + metadata + "}\nspec: {provider: aliyun, type: cdn, resource_id: same, endpoint: static.example.com, origin: " + expected + "}\n"})
			sources := []any{map[string]any{"host": host, "port": port, "protocol": protocol}, map[string]any{"host": "192.0.2.1", "port": port, "protocol": protocol}, map[string]any{"host": host, "port": port, "protocol": protocol}}
			item := map[string]any{"name": "static.example.com", "id": id, "status": "online", "origin": map[string]any{"scope": scope, "sources": sources}}
			if name == "missing-origin" {
				delete(item, "origin")
			}
			items := []any{item}
			if name == "multi-area" {
				items = append(items, map[string]any{"name": "static.example.com", "id": id, "status": "online", "area": "overseas", "origin": item["origin"]})
			}
			in := &Input{Repo: r, Now: now, MaxAge: age, Snapshots: []Snapshot{{Plugin: "aliyun", Instance: instance, File: "state/aliyun/" + instance + ".yaml", ObservedAt: &stamp, verified: verified, Spec: map[string]any{"cdn": items}}}}
			if err := Prepare(context.Background(), in, []Rule{cdnDrift}); err != nil {
				t.Fatal(err)
			}
			f, n := Run(in, []Rule{cdnDrift})
			if (len(f) == 1) != want || len(f) > 1 || (len(n) == 1) != unknown || len(n) > 1 {
				t.Fatalf("findings=%+v notices=%+v", f, n)
			}
			if unknown && n[0].Code != "cdn_origin_unknown" {
				t.Fatal(n)
			}
			if want {
				if f[0].Target != "Resource/edge" || f[0].Evidence["aspect"] != "origin" || (f[0].Waiver != nil) != (name == "waived") {
					t.Fatal(f)
				}
				data, _ := json.Marshal(f)
				if strings.Contains(string(data), "private") || !strings.Contains(string(data), "differences") {
					t.Fatal(string(data))
				}
			}
		})
	}
}

func TestCDNOriginAcrossProvidersAndIDFallback(t *testing.T) {
	for _, provider := range []string{"aliyun", "tencent", "baidu", "ctyun", "cloudflare"} {
		t.Run(provider, func(t *testing.T) {
			service, typ := "cdn", "cdn"
			if provider == "cloudflare" {
				service, typ = "zones", "dns-zone"
			}
			config := fmt.Sprintf("plugins:\n  %s:\n    instances:\n      home: {collect_origin: true}\naudit:\n  watch:\n    - {target: Resource/renamed, plugin: %s, instance: home, service: %s}\n", provider, provider, service)
			r := repo(t, config, map[string]string{"inventory/resources/renamed.yaml": fmt.Sprintf("apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: renamed}\nspec: {provider: %s, type: %s, resource_id: ledger-id, endpoint: example.com, origin: {hosts: [origin.example.com]}}\n", provider, typ)})
			item := map[string]any{"name": "EXAMPLE.COM.", "status": "online", "origin": map[string]any{"scope": "global", "sources": []any{map[string]any{"host": "changed.example.net"}}}}
			if provider == "ctyun" {
				item["status"] = "4"
			}
			if provider == "cloudflare" {
				item["id"] = "ledger-id"
				item["dns_records"] = []any{}
			}
			in := &Input{Repo: r, Now: now, MaxAge: days(1), Snapshots: []Snapshot{{Plugin: provider, Instance: "home", File: "state/" + provider + "/home.yaml", ObservedAt: &now, verified: true, Spec: map[string]any{service: []any{item}}}}}
			if err := Prepare(context.Background(), in, []Rule{cdnDrift}); err != nil {
				t.Fatal(err)
			}
			f, n := Run(in, []Rule{cdnDrift})
			if len(f) != 1 || len(n) != 0 {
				t.Fatalf("%+v %+v", f, n)
			}
		})
	}
}
