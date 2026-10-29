package audit

import (
	"context"
	"encoding/json"
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
