package main

import (
	"os"
	"strings"
	"testing"
)

func TestDNSControlExplicitRecords(t *testing.T) {
	emptyRepo(t)
	if err := os.MkdirAll("inventory/domains", 0755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "inventory/domains/example.com.yaml", `apiVersion: infrafile/v1alpha1
kind: Domain
metadata: {name: example.com}
spec:
  dns:
    provider: cloudflare-main
    records:
      - {name: "@", type: A, content: 192.0.2.10, ttl: 300, proxied: true}
      - {name: mail, type: AAAA, content: "2001:db8::1"}
      - {name: www, type: CNAME, content: example.com}
      - {name: "@", type: MX, content: mail.example.com, priority: 10}
      - {name: "@", type: TXT, content: 'hello "world"'}
      - {name: sub, type: NS, content: ns.example.net}
      - {name: "@", type: CAA, tag: issue, content: ca.example.net}
`)
	out := string(importCLI(t, 0, "dnscontrol"))
	for _, part := range []string{`NO_PURGE`, `NewDnsProvider("cloudflare-main")`, `A("@", "192.0.2.10", TTL(300), CF_PROXY_ON)`, `CNAME("www", "example.com.")`, `MX("@", 10, "mail.example.com.")`, `TXT("@", "hello \"world\"")`, `CAA("@", "issue", "ca.example.net")`} {
		if !strings.Contains(out, part) {
			t.Errorf("missing %s in %s", part, out)
		}
	}
	if _, err := os.Stat("dnsconfig.js"); !os.IsNotExist(err) {
		t.Fatal("generator wrote files")
	}
}

func TestDNSControlRejectsInvalidRecords(t *testing.T) {
	for _, records := range []string{
		`[{name: "@", type: A, content: not-an-ip}]`,
		`[{name: "@", type: AAAA, content: 192.0.2.1}]`,
		`[{name: "@", type: MX, content: mail.example.com}]`,
		`[{name: "@", type: TXT, content: test, priority: 5}]`,
		`[{name: "@", type: CAA, content: ca.example.net}]`,
		`[{name: "@", type: TXT, content: test, proxied: true}]`,
		`[{name: "@", type: NS, content: ns1.example.net, ttl: 300}]`,
		`[{name: "@", type: A, content: 192.0.2.1, ttl: 0}]`,
		`[{name: www, type: A, content: 192.0.2.1}, {name: WWW, type: CNAME, content: example.net}]`,
		`[{name: www, type: A, content: 192.0.2.1}, {name: WWW, type: A, content: 192.0.2.1}]`,
	} {
		t.Run(records, func(t *testing.T) {
			emptyRepo(t)
			if err := os.MkdirAll("inventory/domains", 0755); err != nil {
				t.Fatal(err)
			}
			writeImportFile(t, "inventory/domains/example.com.yaml", "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec:\n  dns:\n    provider: test\n    records: "+records+"\n")
			importCLI(t, 2, "dnscontrol")
		})
	}
}

func TestDNSControlSkipsArchivedAndImplicitBindings(t *testing.T) {
	emptyRepo(t)
	if err := os.MkdirAll("inventory/domains", 0755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "inventory/domains/example.com.yaml", `apiVersion: infrafile/v1alpha1
kind: Domain
metadata: {name: example.com, archived: {date: 2026-10-25, reason: retired}}
spec:
  dns: {provider: test, records: []}
`)
	writeImportFile(t, "inventory/domains/example.net.yaml", `apiVersion: infrafile/v1alpha1
kind: Domain
metadata: {name: example.net}
spec: {bindings: [{name: www}]}
`)
	out := string(importCLI(t, 0, "dnscontrol", "--json"))
	if !strings.Contains(out, `"domains": []`) || strings.Contains(out, "example.") {
		t.Fatal(out)
	}
}

func TestDNSControlApexNameserver(t *testing.T) {
	emptyRepo(t)
	if err := os.MkdirAll("inventory/domains", 0755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "inventory/domains/example.com.yaml", `apiVersion: infrafile/v1alpha1
kind: Domain
metadata: {name: example.com}
spec:
  dns:
    provider: test
    records: [{name: "@", type: NS, content: ns1.example.net}]
`)
	out := string(importCLI(t, 0, "dnscontrol"))
	if !strings.Contains(out, `NAMESERVER("ns1.example.net.")`) || strings.Contains(out, `NS("@"`) {
		t.Fatalf("invalid apex NS: %s", out)
	}
}
