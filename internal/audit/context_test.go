package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestContextKeepsQualificationAndSelectsOnlyMatchedFacts(t *testing.T) {
	r := inspectRepo(t)
	data := domainObservations()
	data["domains"] = append(data["domains"].([]any), map[string]any{"name": "other.example.net", "expires_at": "2099-01-01"})
	inspectEvidence(t, r, "aliyun", "home", data, now.Add(-20*24*time.Hour), "succeeded")
	inspectEvidence(t, r, "dns", "public", dnsObservations(), now.Add(-time.Hour), "succeeded")
	age := 7
	opts := InspectOptions{Now: now, MaxAgeDays: &age, Keys: []string{"Domain/example.com"}}
	got, err := Context(context.Background(), r, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := inspectDomain(t, r, &age)
	if !reflect.DeepEqual(got.Inspection, want) {
		t.Fatal("context changed current inspection semantics")
	}
	encoded, _ := json.Marshal(got.Observations)
	if strings.Contains(string(encoded), "other.example.net") || strings.Contains(string(encoded), "2099-01-01") {
		t.Fatal("context included another asset")
	}
	matched, stale := false, false
	for _, e := range got.Observations {
		if e.Plugin == "aliyun" && e.Match == "matched" && len(e.Values) == 1 {
			matched = true
		}
		for _, c := range got.Inspection.Coverage.Checks {
			if c.ID == e.Check && c.Code == "stale" {
				stale = true
			}
		}
	}
	if !matched || !stale || got.Inspection.OK {
		t.Fatal("saved facts were hidden or promoted to current success")
	}
}

func TestContextMultipleAssetsKeepIdentityAndOneHopScope(t *testing.T) {
	r := repo(t, "plugins:\n  ssh:\n    instances:\n      web: {host: 192.0.2.10}\n      db: {host: 192.0.2.20}\n", map[string]string{
		"inventory/servers/web.yaml":         "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: web}\nspec: {address: 192.0.2.10, account: home}\n",
		"inventory/servers/db.yaml":          "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: db}\nspec: {address: 192.0.2.20}\n",
		"inventory/accounts/home.yaml":       "apiVersion: infrafile/v1alpha1\nkind: Account\nmetadata: {name: home}\nspec: {provider: example, secrets: {token: 'env://EXAMPLE_TOKEN'}}\n",
		"inventory/accounts/unrelated.yaml":  "apiVersion: infrafile/v1alpha1\nkind: Account\nmetadata: {name: unrelated, description: unrelated-private-field}\nspec: {provider: example}\n",
		"inventory/apps/blog.yaml":           "apiVersion: infrafile/v1alpha1\nkind: App\nmetadata: {name: blog}\nspec: {deployments: [{server: web}]}\n",
		"inventory/domains/example.com.yaml": "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec: {bindings: [{name: '@', app: blog}]}\n",
	})
	for instance, host := range map[string]string{"web": "192.0.2.10", "db": "192.0.2.20"} {
		inspectEvidence(t, r, "ssh", instance, map[string]any{"host": host, "system": "Linux", "release": "example", "architecture": "arm64"}, now.Add(-time.Hour), "succeeded")
	}
	age := 7
	opts := InspectOptions{Now: now, MaxAgeDays: &age, Keys: []string{"Server/web", "Server/db"}}
	got, err := Context(context.Background(), r, opts)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := Inspect(context.Background(), r, opts)
	if err != nil || !reflect.DeepEqual(expected, got.Inspection) {
		t.Fatal("context expanded inspection scope")
	}
	if len(got.Records) != 4 || len(got.NextActions) != 2 {
		t.Fatal("missing one-hop records or actions", len(got.Records), len(got.NextActions))
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "env://") || strings.Contains(string(b), "unrelated-private-field") || strings.Contains(string(b), "Domain/example.com") {
		t.Fatal("secrets or a second hop escaped selection")
	}
	matched := map[string]bool{}
	for _, e := range got.Observations {
		if e.Service != "host" {
			continue
		}
		if e.Match != "matched" || len(e.Values) != 1 {
			t.Fatal("SSH fact missing", e)
		}
		host := e.Values[0].(map[string]any)["host"]
		if e.Target == "Server/web" && host != "192.0.2.10" || e.Target == "Server/db" && host != "192.0.2.20" {
			t.Fatal("borrowed another selected asset's facts")
		}
		matched[e.Target] = true
	}
	if len(matched) != 2 {
		t.Fatal("selected asset evidence missing")
	}
	for _, record := range r.Records {
		if record.Kind == "Account" && record.Name == "home" {
			if !strings.Contains(fmt.Sprint(record.Value), "env://EXAMPLE_TOKEN") {
				t.Fatal("redaction mutated original record")
			}
		}
	}
}

func TestContextActionsUseOnlyLocalSupportedBindings(t *testing.T) {
	records := map[string]string{}
	for _, name := range []string{"edge", "unbound", "archived", "external"} {
		archived := ""
		binding := ""
		provider := "cloudflare"
		if name == "archived" {
			archived = ", archived: {date: '2026-10-29', reason: retired}"
		}
		if name != "unbound" {
			p := "cloudflare"
			if name == "external" {
				p = "custom"
				provider = p
			}
			binding = ", control: {plugin: " + p + ", instance: home, zone_id: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}"
		}
		records["inventory/resources/"+name+".yaml"] = "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: " + name + archived + "}\nspec: {provider: " + provider + ", type: dns-zone" + binding + "}\n"
	}
	r := repo(t, "plugins:\n  cloudflare:\n    instances:\n      home: {}\n  custom:\n    instances:\n      home: {}\n", records)
	got, err := Context(context.Background(), r, InspectOptions{Now: now, Keys: []string{"Resource/edge", "Resource/unbound", "Resource/archived", "Resource/external"}})
	if err != nil {
		t.Fatal(err)
	}
	plans := 0
	for _, action := range got.NextActions {
		if action.Action == "inventory-edit" {
			continue
		}
		plans++
		if action.Target != "Resource/edge" || action.Capability == nil || action.Capability.Stages.Plan != "conditional" || len(action.Requires) == 0 {
			t.Fatal("unverified or unsupported plan advertised", action)
		}
		if strings.Contains(action.Interface, "apply") {
			t.Fatal("context offered cloud execution")
		}
	}
	if plans != 3 {
		t.Fatal("supported plan hints missing", plans)
	}
}

func TestContextBoundedWithoutPartialResults(t *testing.T) {
	r := inspectRepo(t)
	keys := make([]string, 65)
	for i := range keys {
		keys[i] = "Domain/example.com"
	}
	if result, err := Context(context.Background(), r, InspectOptions{Now: now, Keys: keys}); err == nil || result != nil {
		t.Fatal("unbounded selection accepted")
	}
	records := map[string]string{"inventory/accounts/home.yaml": "apiVersion: infrafile/v1alpha1\nkind: Account\nmetadata: {name: home}\nspec: {provider: example}\n"}
	for i := 0; i < 256; i++ {
		name := fmt.Sprintf("server-%03d", i)
		records["inventory/servers/"+name+".yaml"] = "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: " + name + "}\nspec: {address: 192.0.2.10, account: home}\n"
	}
	large := repo(t, "", records)
	if result, err := Context(context.Background(), large, InspectOptions{Now: now, Keys: []string{"Account/home"}}); err == nil || result != nil {
		t.Fatal("unbounded direct relations accepted")
	}
	description := strings.Repeat("x", 4<<20)
	large = repo(t, "", map[string]string{"inventory/servers/large.yaml": "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: large, description: '" + description + "'}\nspec: {address: 192.0.2.10}\n"})
	if result, err := Context(context.Background(), large, InspectOptions{Now: now, Keys: []string{"Server/large"}}); err == nil || result != nil {
		t.Fatal("oversized context exposed partial result")
	}
}

func TestContextRejectsAmbiguousAndMismatchedValues(t *testing.T) {
	r := inspectRepo(t)
	data := map[string]any{"domains": []any{map[string]any{"name": "example.com"}, map[string]any{"name": "example.com"}}}
	inspectEvidence(t, r, "aliyun", "home", data, now.Add(-time.Hour), "succeeded")
	dns := dnsObservations()
	dns["host"] = "other.example.net"
	inspectEvidence(t, r, "dns", "public", dns, now.Add(-time.Hour), "failed")
	age := 7
	got, err := Context(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: &age, Keys: []string{"Domain/example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Observations {
		if len(e.Values) != 0 {
			t.Fatal("ambiguous or mismatched source values exposed")
		}
		if e.Plugin == "aliyun" && e.Match != "ambiguous" {
			t.Fatal(e)
		}
		if e.Plugin == "dns" && e.Match != "identity_mismatch" {
			t.Fatal(e)
		}
	}
}

func TestContextDoesNotBorrowAnotherInstance(t *testing.T) {
	r := repo(t, strings.Replace(inspectConfig, "home: {services: [domains]}", "home: {services: [domains]}\n      other: {services: [domains]}", 1), map[string]string{
		"inventory/domains/example.com.yaml": "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec: {bindings: []}\n",
	})
	inspectEvidence(t, r, "aliyun", "other", domainObservations(), now.Add(-time.Hour), "succeeded")
	age := 7
	got, err := Context(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: &age, Keys: []string{"Domain/example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Observations) != 0 {
		t.Fatal("another instance supplied selected asset facts")
	}
	for _, keys := range [][]string{nil, {}, {"Domain/missing"}, {"Domain/example.com", "Domain/example.com"}} {
		if _, err := Context(context.Background(), r, InspectOptions{Now: now, Keys: keys}); err == nil {
			t.Fatal("invalid context selection accepted", keys)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Context(ctx, r, InspectOptions{Now: now, Keys: []string{"Domain/example.com"}}); err == nil {
		t.Fatal("cancelled context succeeded")
	}
}

func TestContextDNSFactsStayWithinWatchedZone(t *testing.T) {
	r := repo(t, "plugins:\n  cloudflare:\n    instances:\n      home: {}\naudit:\n  watch:\n    - {target: Domain/example.com, plugin: cloudflare, instance: home, service: zones}\n", map[string]string{
		"inventory/domains/example.com.yaml": "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec: {bindings: [{name: www, server: web}]}\n",
		"inventory/servers/web.yaml":         "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: web}\nspec: {address: 192.0.2.10}\n",
	})
	zone := func(name, address string) any {
		return map[string]any{"id": name, "name": name, "dns_records": []any{map[string]any{"name": "www.example.com", "type": "A", "content": address, "proxied": true}}}
	}
	inspectEvidence(t, r, "cloudflare", "home", map[string]any{"zones": []any{zone("example.com", "192.0.2.10"), zone("other.example.net", "192.0.2.99")}}, now.Add(-time.Hour), "succeeded")
	age := 7
	got, err := Context(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: &age, Keys: []string{"Domain/example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got.Observations)
	if strings.Contains(string(encoded), "192.0.2.99") || strings.Contains(string(encoded), "other.example.net") {
		t.Fatal("watched zone borrowed another zone's DNS facts")
	}
	if !strings.Contains(string(encoded), "192.0.2.10") {
		t.Fatal("matched DNS facts missing")
	}
}
