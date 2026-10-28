package audit

import (
	"context"
	"strings"
	"testing"
)

func originRun(t *testing.T, config string, files map[string]string) ([]Finding, []Notice) {
	t.Helper()
	in := &Input{Repo: repo(t, config, files), Now: now}
	var err error
	var notices []Notice
	in.Snapshots, notices, err = ReadSnapshots(context.Background(), in.Repo)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := Select([]string{"origin_exposure"}, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := Prepare(context.Background(), in, rules); err != nil {
		t.Fatal(err)
	}
	f, more := Run(in, rules)
	return f, append(notices, more...)
}

func originFiles(address, binding, dns string) map[string]string {
	return map[string]string{
		"inventory/servers/web.yaml":         "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: web}\nspec: {address: '" + address + "'}\n",
		"inventory/domains/example.com.yaml": "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec:\n  bindings: [" + binding + "]\n" + dns,
	}
}

func TestOriginExposureEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, address, binding, dns, plugin, spec string
		want                                      bool
		source                                    string
	}{
		{"ledger", "192.0.2.10", "{name: '@', server: web}", "  dns: {provider: example, records: [{name: '@', type: A, content: 192.0.2.10}]}\n", "", "", true, "ledger"},
		{"ledger_proxy", "192.0.2.10", "{name: '@', server: web}", "  dns: {provider: example, records: [{name: '@', type: A, content: 192.0.2.10, proxied: true}]}\n", "", "", false, ""},
		{"ledger_explicit_off", "192.0.2.10", "{name: www, server: web}", "  dns: {provider: example, records: [{name: WWW, type: A, content: 192.0.2.10, proxied: false}]}\n", "", "", true, "ledger"},
		{"cloudflare", "192.0.2.10", "{name: '@', server: web}", "", "cloudflare", "zones: [{name: example.com, id: zone, dns_records: [{name: EXAMPLE.COM., type: A, content: 192.0.2.10, proxied: false}]}]", true, "cloudflare"},
		{"cloudflare_proxy", "192.0.2.10", "{name: '@', server: web}", "", "cloudflare", "zones: [{name: example.com, id: zone, dns_records: [{name: example.com, type: A, content: 192.0.2.10, proxied: true}]}]", false, ""},
		{"cloudflare_unknown_proxy", "192.0.2.10", "{name: '@', server: web}", "", "cloudflare", "zones: [{name: example.com, id: zone, dns_records: [{name: example.com, type: A, content: 192.0.2.10}]}]", false, ""},
		{"dns", "192.0.2.10", "{name: blog, server: web}", "", "dns", "host: BLOG.EXAMPLE.COM., addresses: [192.0.2.10], canonical_name: edge.example.net", true, "dns"},
		{"different_address", "192.0.2.10", "{name: '@', server: web}", "", "dns", "host: example.com, addresses: [192.0.2.11]", false, ""},
		{"different_host", "192.0.2.10", "{name: '@', server: web}", "", "dns", "host: unrelated.example.net, addresses: [192.0.2.10]", false, ""},
		{"dns_ipv6", "2001:db8::10", "{name: '@', server: web}", "", "dns", "host: example.com, addresses: ['2001:0DB8:0:0:0:0:0:10']", true, "dns"},
		{"ledger_ipv6", "2001:db8::10", "{name: '@', server: web}", "  dns: {provider: example, records: [{name: '@', type: AAAA, content: '2001:0db8:0:0:0:0:0:10'}]}\n", "", "", true, "ledger"},
		{"cf_ipv6", "2001:db8::10", "{name: '@', server: web}", "", "cloudflare", "zones: [{name: example.com, dns_records: [{name: example.com, type: AAAA, content: '2001:0db8::10', proxied: false}]}]", true, "cloudflare"},
		{"private", "10.0.0.10", "{name: '@', server: web}", "", "dns", "host: example.com, addresses: [10.0.0.10]", false, ""},
		{"loopback", "127.0.0.1", "{name: '@', server: web}", "", "dns", "host: example.com, addresses: [127.0.0.1]", false, ""},
		{"link_local", "169.254.1.1", "{name: '@', server: web}", "", "dns", "host: example.com, addresses: [169.254.1.1]", false, ""},
		{"private_v6", "fd00::1", "{name: '@', server: web}", "", "dns", "host: example.com, addresses: ['fd00::1']", false, ""},
		{"loopback_v6", "::1", "{name: '@', server: web}", "", "dns", "host: example.com, addresses: ['::1']", false, ""},
		{"linklocal_v6", "fe80::1", "{name: '@', server: web}", "", "dns", "host: example.com, addresses: ['fe80::1']", false, ""},
		{"mapped_private", "::ffff:10.0.0.10", "{name: '@', server: web}", "", "dns", "host: example.com, addresses: [10.0.0.10]", false, ""},
		{"hostname_server", "origin.example.net", "{name: '@', server: web}", "", "dns", "host: example.com, addresses: [192.0.2.10]", false, ""},
		{"app_only", "192.0.2.10", "{name: '@', app: blog}", "", "dns", "host: example.com, addresses: [192.0.2.10]", false, ""},
		{"resource_binding", "192.0.2.10", "{name: '@', resource: cdn}", "", "dns", "host: example.com, addresses: [192.0.2.10]", false, ""},
		{"resource_precedence", "192.0.2.10", "{name: '@', resource: cdn, server: web}", "", "dns", "host: example.com, addresses: [192.0.2.10]", false, ""},
		{"cname_not_expanded", "192.0.2.10", "{name: '@', server: web}", "  dns: {provider: example, records: [{name: '@', type: CNAME, content: origin.example.net}]}\n", "", "", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := originFiles(tc.address, tc.binding, tc.dns)
			config := ""
			if tc.plugin != "" {
				config = "plugins: {" + tc.plugin + ": {instances: {home: {}}}}\n"
				files["state/"+tc.plugin+"/home.yaml"] = "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {" + tc.spec + "}\n"
			}
			f, n := originRun(t, config, files)
			if (len(f) == 1) != tc.want || len(f) > 1 || len(n) != 0 {
				t.Fatalf("发现=%+v 提示=%+v", f, n)
			}
			if tc.want && (f[0].Target != "Domain/example.com" || f[0].Evidence["source"] != tc.source || f[0].Evidence["observed_at"] != nil) {
				t.Fatal(f)
			}
		})
	}
}

func TestOriginExposureWaiverIsolation(t *testing.T) {
	files := originFiles("192.0.2.10", "{name: '@', server: web}", "  dns: {provider: example, records: [{name: '@', type: A, content: 192.0.2.10}]}\n")
	files["inventory/domains/other.example.net.yaml"] = strings.ReplaceAll(files["inventory/domains/example.com.yaml"], "example.com", "other.example.net")
	files["inventory/domains/example.com.yaml"] = strings.Replace(files["inventory/domains/example.com.yaml"], "metadata: {name: example.com}", "metadata: {name: example.com, waivers: [{rule: origin_exposure, reason: 迁移期间暂缓, until: 2026-12-31}]}", 1)
	f, n := originRun(t, "", files)
	if len(f) != 2 || f[0].Waiver == nil || f[1].Waiver != nil || len(n) != 0 {
		t.Fatalf("%+v %+v", f, n)
	}
}

func TestOriginAppDeployments(t *testing.T) {
	for _, tc := range []struct {
		name, binding, deployments, address string
		archiveApp, archiveServer           bool
		findings, unknown                   int
	}{
		{"all_deployments", "{name: '@', app: blog}", "[{server: web}, {server: v6}, {server: web}]", "192.0.2.10", false, false, 2, 0},
		{"partial_unknown", "{name: '@', app: blog}", "[{server: web}, {server: v6}]", "origin.example.net", false, false, 1, 1},
		{"no_deployments", "{name: '@', app: blog}", "[]", "192.0.2.10", false, false, 0, 1},
		{"mixed_archived", "{name: '@', app: blog}", "[{server: web}, {server: v6}]", "192.0.2.10", false, true, 1, 0},
		{"all_archived", "{name: '@', app: blog}", "[{server: web}]", "192.0.2.10", false, true, 0, 1},
		{"archived_app", "{name: '@', app: blog}", "[{server: web}]", "192.0.2.10", true, false, 0, 0},
		{"explicit_server", "{name: '@', app: blog, server: web}", "[{server: v6}]", "192.0.2.10", false, false, 1, 0},
		{"resource_precedence", "{name: '@', app: blog, server: web, resource: cdn}", "[{server: web}]", "192.0.2.10", false, false, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := originFiles(tc.address, tc.binding, "  dns: {provider: example, records: [{name: '@', type: A, content: 192.0.2.10}, {name: '@', type: AAAA, content: '2001:db8::10'}]}\n")
			files["inventory/servers/v6.yaml"] = "kind: Server\nmetadata: {name: v6}\nspec: {address: '2001:db8::10'}\n"
			files["inventory/apps/blog.yaml"] = "kind: App\nmetadata: {name: blog}\nspec: {deployments: " + tc.deployments + "}\n"
			if tc.archiveApp {
				files["inventory/apps/blog.yaml"] = strings.Replace(files["inventory/apps/blog.yaml"], "name: blog", "name: blog, archived: {date: 2026-11-01, reason: retired}", 1)
			}
			if tc.archiveServer {
				files["inventory/servers/web.yaml"] = strings.Replace(files["inventory/servers/web.yaml"], "name: web", "name: web, archived: {date: 2026-11-01, reason: retired}", 1)
			}
			f, n := originRun(t, "", files)
			if len(f) != tc.findings || len(n) != tc.unknown {
				t.Fatalf("findings=%+v notices=%+v", f, n)
			}
			for _, notice := range n {
				if notice.Code != "origin_exposure_unknown" || notice.Target != "Domain/example.com" {
					t.Fatal(notice)
				}
			}
		})
	}
}

func TestOriginAppSharedEvidenceAndWaivers(t *testing.T) {
	files := originFiles("192.0.2.10", "{name: '@', app: blog}, {name: '@', app: shared}", "  dns: {provider: example, records: [{name: '@', type: A, content: 192.0.2.10}]}\n")
	for _, name := range []string{"blog", "shared"} {
		files["inventory/apps/"+name+".yaml"] = "kind: App\nmetadata: {name: " + name + "}\nspec: {deployments: [{server: web}]}\n"
	}
	files["inventory/domains/other.example.net.yaml"] = strings.ReplaceAll(files["inventory/domains/example.com.yaml"], "example.com", "other.example.net")
	files["inventory/domains/example.com.yaml"] = strings.Replace(files["inventory/domains/example.com.yaml"], "name: example.com", "name: example.com, waivers: [{rule: origin_exposure, reason: 迁移期间暂缓}]", 1)
	files["state/dns/home.yaml"] = "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {host: example.com, addresses: [192.0.2.10, 192.0.2.10]}\n"
	files["state/cloudflare/home.yaml"] = "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {zones: [{name: example.com, dns_records: [{name: example.com, type: A, content: 192.0.2.10, proxied: false}]}]}\n"
	f, n := originRun(t, "plugins: {dns: {instances: {home: {}}}, cloudflare: {instances: {home: {}}}}\n", files)
	if len(f) != 4 || len(n) != 0 {
		t.Fatalf("findings=%+v notices=%+v", f, n)
	}
	for _, finding := range f {
		if finding.Evidence["app"] != "App/blog" || (finding.Waiver != nil) != (finding.Target == "Domain/example.com") {
			t.Fatal(finding)
		}
	}
}

func TestOriginExposureMultipleSourcesAndDisabled(t *testing.T) {
	files := originFiles("192.0.2.10", "{name: '@', server: web}", "  dns: {provider: example, records: [{name: '@', type: A, content: 192.0.2.10}]}\n")
	files["state/dns/home.yaml"] = "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {host: example.com, addresses: [192.0.2.10, 192.0.2.10]}\n"
	files["state/cloudflare/home.yaml"] = "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {zones: [{name: example.com, dns_records: [{name: example.com, type: A, content: 192.0.2.10, proxied: false}]}]}\n"
	files["state/dns/disabled.yaml"] = "broken: [\n"
	files["state/_sync.yaml"] = "apiVersion: infrafile/v1alpha1\nkind: Sync\nspec: {dns: {home: '2026-11-30T03:00:00Z'}, cloudflare: {home: '2026-11-30T03:00:00Z'}}\n"
	f, n := originRun(t, "plugins: {dns: {instances: {home: {}}}, cloudflare: {instances: {home: {}}}}\n", files)
	if len(f) != 3 || len(n) != 1 || n[0].Code != "instance_disabled" {
		t.Fatalf("%+v %+v", f, n)
	}
	for _, entry := range f {
		if entry.Evidence["source"] != "ledger" && entry.Evidence["observed_at"] != "2026-11-30T03:00:00Z" {
			t.Fatal(entry)
		}
	}
}

func TestOriginExposureArchivedAndExpired(t *testing.T) {
	for _, tc := range []struct {
		name, replace string
		archived      bool
	}{
		{"expired", "waivers: [{rule: origin_exposure, reason: 迁移窗口, until: 2026-11-30}]", false},
		{"archived", "archived: {date: 2026-11-01, reason: 已下线}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := originFiles("192.0.2.10", "{name: '@', server: web}", "  dns: {provider: example, records: [{name: '@', type: A, content: 192.0.2.10}]}\n")
			files["inventory/domains/example.com.yaml"] = strings.Replace(files["inventory/domains/example.com.yaml"], "metadata: {name: example.com}", "metadata: {name: example.com, "+tc.replace+"}", 1)
			f, n := originRun(t, "", files)
			if tc.archived {
				if len(f) != 0 {
					t.Fatal(f)
				}
				return
			}
			if len(f) != 1 || f[0].Waiver != nil || len(n) != 1 || n[0].Code != "waiver_expired" {
				t.Fatalf("%+v %+v", f, n)
			}
		})
	}
}

func TestOriginExposureMalformedObservation(t *testing.T) {
	in := &Input{Repo: repo(t, "", nil), Snapshots: []Snapshot{{Plugin: "dns", File: "state/dns/home.yaml", Spec: map[string]any{"host": "example.com", "addresses": []any{"invalid"}}}}}
	if err := Prepare(context.Background(), in, []Rule{originExposure}); err == nil {
		t.Fatal("invalid address accepted")
	}
	if err := Prepare(context.Background(), in, []Rule{orphan}); err != nil {
		t.Fatal("DNS incorrectly entered lifecycle", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Prepare(ctx, in, []Rule{originExposure}); err == nil {
		t.Fatal("cancelled preparation succeeded")
	}
}
