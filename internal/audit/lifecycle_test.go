package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/observation"
)

func lifecycleRun(t *testing.T, config string, files map[string]string) ([]Finding, []Notice) {
	t.Helper()
	in := &Input{Repo: repo(t, config, files), Now: now}
	snaps, notices, err := ReadSnapshots(context.Background(), in.Repo)
	if err != nil {
		t.Fatal(err)
	}
	in.Snapshots = snaps
	rules, err := Select([]string{"orphan", "zombie"}, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := Prepare(context.Background(), in, rules); err != nil {
		t.Fatal(err)
	}
	f, more := Run(in, rules)
	return f, append(notices, more...)
}

func TestLifecycleNaturalIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, plugin, spec, kind, recordName, recordSpec string
		archived, liveAlias                              bool
		want                                             string
	}{
		{name: "domain_live", plugin: "aliyun", spec: "domains: [{name: EXAMPLE.COM.}]", kind: "Domain", recordName: "example.com", recordSpec: "{}"},
		{name: "domain_archived", plugin: "tencent", spec: "domains: [{name: example.com}]", kind: "Domain", recordName: "example.com", recordSpec: "{}", archived: true, want: "zombie"},
		{name: "domain_orphan", plugin: "baidu", spec: "domains: [{name: example.net}]", kind: "Domain", recordName: "example.com", recordSpec: "{}", want: "orphan"},
		{name: "whois", plugin: "whois", spec: "found: true, domain: EXAMPLE.COM.", kind: "Domain", recordName: "example.com", recordSpec: "{}"},
		{name: "whois_not_found", plugin: "whois", spec: "found: false", kind: "Domain", recordName: "example.com", recordSpec: "{}", archived: true},
		{name: "renamed_id", plugin: "aliyun", spec: "cdn: [{name: new.example.com, id: same}]", kind: "Resource", recordName: "manually-renamed", recordSpec: "{provider: aliyun, type: cdn, resource_id: same, endpoint: old.example.com}"},
		{name: "different_id", plugin: "aliyun", spec: "cdn: [{name: same.example.com, id: new}]", kind: "Resource", recordName: "edge", recordSpec: "{provider: aliyun, type: cdn, resource_id: old, endpoint: same.example.com}", want: "orphan"},
		{name: "different_provider", plugin: "ctyun", spec: "cdn: [{name: same.example.com, id: same}]", kind: "Resource", recordName: "edge", recordSpec: "{provider: aliyun, type: cdn, resource_id: same}", want: "orphan"},
		{name: "no_id_fallback", plugin: "baidu", spec: "cdn: [{name: STATIC.EXAMPLE.COM.}]", kind: "Resource", recordName: "edge", recordSpec: "{provider: baidu, type: cdn, resource_id: ledger-id, endpoint: static.example.com}"},
		{name: "no_id_wrong_type", plugin: "baidu", spec: "cdn: [{name: static.example.com}]", kind: "Resource", recordName: "edge", recordSpec: "{provider: baidu, type: bucket, endpoint: static.example.com}", want: "orphan"},
		{name: "resource_zombie", plugin: "aliyun", spec: "cdn: [{name: static.example.com, id: same}]", kind: "Resource", recordName: "edge", recordSpec: "{provider: aliyun, type: cdn, resource_id: same}", archived: true, want: "zombie"},
		{name: "zone_domain_covers", plugin: "cloudflare", spec: "zones: [{name: EXAMPLE.COM., id: zone}]", kind: "Domain", recordName: "example.com", recordSpec: "{}"},
		{name: "zone_archived_domain", plugin: "cloudflare", spec: "zones: [{name: example.com, id: zone}]", kind: "Domain", recordName: "example.com", recordSpec: "{}", archived: true, want: "zombie"},
		{name: "zone_resource_covers", plugin: "cloudflare", spec: "zones: [{name: example.com, id: zone}]", kind: "Resource", recordName: "dns", recordSpec: "{provider: cloudflare, type: dns-zone, resource_id: zone}"},
		{name: "server_address", plugin: "ssh", spec: "host: '2001:db8::1', system: Linux, release: example", kind: "Server", recordName: "renamed", recordSpec: "{address: '2001:0db8:0:0:0:0:0:1'}"},
		{name: "server_zombie", plugin: "ssh", spec: "host: 192.0.2.10, system: Linux, release: example", kind: "Server", recordName: "old", recordSpec: "{address: 192.0.2.10}", archived: true, want: "zombie"},
		{name: "server_orphan", plugin: "ssh", spec: "host: 192.0.2.11, system: Linux, release: example", kind: "Server", recordName: "old", recordSpec: "{address: 192.0.2.10}", archived: true, want: "orphan"},
		{name: "active_alias_wins", plugin: "aliyun", spec: "cdn: [{name: static.example.com, id: same}]", kind: "Resource", recordName: "old", recordSpec: "{provider: aliyun, type: cdn, resource_id: same}", archived: true, liveAlias: true},
		{name: "dns_excluded", plugin: "dns", spec: "anything: ignored", kind: "Domain", recordName: "example.com", recordSpec: "{}", archived: true},
		{name: "tls_excluded", plugin: "tls", spec: "anything: ignored", kind: "Server", recordName: "old", recordSpec: "{address: 192.0.2.10}", archived: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := ""
			if tc.archived {
				archive = ", archived: {date: 2026-01-01, reason: 测试归档}"
			}
			files := map[string]string{
				"inventory/" + strings.ToLower(tc.kind) + "s/" + tc.recordName + ".yaml": "apiVersion: infrafile/v1alpha1\nkind: " + tc.kind + "\nmetadata: {name: " + tc.recordName + archive + "}\nspec: " + tc.recordSpec + "\n",
				"state/" + tc.plugin + "/home.yaml":                                      "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {" + tc.spec + "}\n",
			}
			if tc.liveAlias {
				files["inventory/resources/new.yaml"] = "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: new}\nspec: " + tc.recordSpec + "\n"
			}
			f, n := lifecycleRun(t, "plugins: {"+tc.plugin+": {instances: {home: {}}}}\n", files)
			if tc.want == "" {
				if len(f) != 0 {
					t.Fatalf("误报: %+v", f)
				}
				return
			}
			if len(f) != 1 || f[0].Rule != tc.want || len(n) != 0 {
				t.Fatalf("发现=%+v 提示=%+v", f, n)
			}
			if f[0].Evidence["observed_at"] != nil || f[0].Evidence["plugin"] != tc.plugin {
				t.Fatal(f)
			}
			if tc.want == "zombie" && f[0].Target != tc.kind+"/"+tc.recordName {
				t.Fatal(f)
			}
		})
	}
}

func TestLifecyclePreciseWaivers(t *testing.T) {
	for _, rule := range []string{"orphan", "zombie"} {
		t.Run(rule, func(t *testing.T) {
			files := map[string]string{}
			for _, instance := range []string{"home", "other"} {
				files["state/aliyun/"+instance+".yaml"] = "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: " + instance + "}\nspec: {domains: [{name: one.example.com}, {name: two.example.com}]}\n"
			}
			if rule == "zombie" {
				for _, name := range []string{"one.example.com", "two.example.com"} {
					files["inventory/domains/"+name+".yaml"] = "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: " + name + ", archived: {date: 2026-01-01, reason: 测试归档}}\nspec: {}\n"
				}
			}
			config := "plugins: {aliyun: {instances: {home: {}, other: {}}}}\naudit:\n  ignore:\n    - rule: " + rule + "\n      plugin: aliyun\n      instance: home\n      reason: 单条暂缓处理\n      match: {kind: Domain, name: ONE.EXAMPLE.COM.}\n"
			f, n := lifecycleRun(t, config, files)
			waived := 0
			for _, entry := range f {
				if entry.Waiver != nil {
					waived++
					if entry.plugin != "aliyun" || entry.instance != "home" || entry.identity.Name != "one.example.com" {
						t.Fatal(entry)
					}
				}
			}
			if len(f) != 4 || waived != 1 || len(n) != 0 {
				t.Fatalf("findings=%+v waived=%d notices=%+v", f, waived, n)
			}
		})
	}
}

func TestLifecycleArchivedRecordWaiver(t *testing.T) {
	f, n := lifecycleRun(t, "plugins: {ssh: {instances: {home: {}}}}\n", map[string]string{
		"inventory/servers/old.yaml": "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: old, archived: {date: 2026-01-01, reason: 测试归档}, waivers: [{rule: zombie, reason: 迁移窗口}]}\nspec: {address: 192.0.2.10}\n",
		"state/ssh/home.yaml":        "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {host: 192.0.2.10, system: Linux, release: example}\n",
	})
	if len(f) != 1 || f[0].Waiver == nil || f[0].Waiver.Source != "inventory/servers/old.yaml" || len(n) != 0 {
		t.Fatalf("%+v %+v", f, n)
	}
}

func TestLifecycleDisabledAndDuplicate(t *testing.T) {
	f, n := lifecycleRun(t, "plugins: {aliyun: {instances: {home: {}}}}\n", map[string]string{
		"state/aliyun/home.yaml":     "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {domains: [{name: example.org}, {name: EXAMPLE.ORG.}]}\n",
		"state/aliyun/disabled.yaml": "broken: [\n",
	})
	if len(f) != 1 || f[0].Rule != "orphan" || len(n) != 1 || n[0].Code != "instance_disabled" {
		t.Fatalf("%+v %+v", f, n)
	}
}

func TestLifecycleResourceIgnore(t *testing.T) {
	for _, tc := range []struct {
		name, id, match string
		waived          int
	}{
		{"id", ", id: first", "{kind: Resource, provider: aliyun, resource_id: first}", 1},
		{"id_not_endpoint", ", id: first", "{kind: Resource, provider: aliyun, type: cdn, endpoint: one.example.com}", 0},
		{"endpoint", "", "{kind: Resource, provider: aliyun, type: cdn, endpoint: ONE.EXAMPLE.COM.}", 1},
		{"other_provider", ", id: first", "{kind: Resource, provider: tencent, resource_id: first}", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := "plugins: {aliyun: {instances: {home: {}}}}\naudit:\n  ignore:\n    - rule: orphan\n      plugin: aliyun\n      instance: home\n      reason: 单个演示资源\n      match: " + tc.match + "\n"
			f, n := lifecycleRun(t, config, map[string]string{"state/aliyun/home.yaml": "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {cdn: [{name: one.example.com" + tc.id + "}, {name: two.example.com, id: second}]}\n"})
			waived := 0
			for _, entry := range f {
				if entry.Waiver != nil {
					waived++
				}
			}
			if len(f) != 2 || waived != tc.waived {
				t.Fatalf("%+v %+v", f, n)
			}
			if waived == 0 && (len(n) != 1 || n[0].Code != "waiver_unused") {
				t.Fatal(n)
			}
		})
	}
}

func TestLifecycleMalformedAndCancelled(t *testing.T) {
	in := &Input{Repo: repo(t, "", nil), Snapshots: []Snapshot{{Plugin: "aliyun", File: "state/aliyun/home.yaml", Spec: map[string]any{"cdn": "broken"}}}}
	if err := Prepare(context.Background(), in, []Rule{orphan}); err == nil {
		t.Fatal("invalid observation accepted")
	}
	if err := Prepare(context.Background(), in, []Rule{staleState}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Prepare(ctx, in, []Rule{zombie}); err == nil {
		t.Fatal("cancelled preparation succeeded")
	}
	if len(orphan.Observations) != len(observation.Requirements()) || len(zombie.Observations) == 0 {
		t.Fatal("missing observation requirements")
	}
}
