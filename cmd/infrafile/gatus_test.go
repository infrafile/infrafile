package main

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/ledger"
	"go.yaml.in/yaml/v3"
)

func gatusRepo(t *testing.T, bindings string) {
	t.Helper()
	emptyRepo(t)
	if err := os.MkdirAll("inventory/domains", 0755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "inventory/domains/example.com.yaml", "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec:\n  bindings: "+bindings+"\n")
}

func TestGatusExplicitEndpoints(t *testing.T) {
	gatusRepo(t, `[{name: wiki, monitor: {path: '/health?full=true', interval: 5m, status: 204}}, {name: unmonitored}, {name: '@', monitor: {}}]`)
	out := importCLI(t, 0, "gatus")
	var got struct {
		Endpoints []gatusEndpoint `yaml:"endpoints"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	want := []gatusEndpoint{
		{"example.com", "example.com", "https://example.com/", "1m", []string{"[STATUS] == 200", "[CERTIFICATE_EXPIRATION] > 240h"}},
		{"wiki.example.com", "example.com", "https://wiki.example.com/health?full=true", "5m", []string{"[STATUS] == 204", "[CERTIFICATE_EXPIRATION] > 240h"}},
	}
	if !reflect.DeepEqual(got.Endpoints, want) || string(out) != string(importCLI(t, 0, "gatus")) {
		t.Fatalf("got %+v", got)
	}
	var report struct {
		Config    string   `json:"config"`
		Endpoints []string `json:"endpoints"`
	}
	if err := json.Unmarshal(importCLI(t, 0, "gatus", "--json"), &report); err != nil || report.Config != string(out) || !reflect.DeepEqual(report.Endpoints, []string{"example.com", "wiki.example.com"}) {
		t.Fatalf("%+v %v", report, err)
	}
	files, _ := os.ReadDir(".")
	if len(files) != 2 || strings.Contains(string(out), "DOMAIN_EXPIRATION") {
		t.Fatal("generator wrote files or added WHOIS")
	}
}

func TestGatusRejectsInvalidMonitor(t *testing.T) {
	for _, bindings := range []string{
		`[{name: '@', monitor: {interval: 0s}}]`,
		`[{name: '@', monitor: {interval: -1m}}]`,
		`[{name: '@', monitor: {interval: 7d}}]`,
		`[{name: '@', monitor: {path: '//other.example.net/'}}]`,
		`[{name: '@', monitor: {path: '/#fragment'}}]`,
		`[{name: '@', monitor: {path: "/bad\npath"}}]`,
		`[{name: '@', monitor: {path: '/bad%escape'}}]`,
		`[{name: '@', monitor: {status: 600}}]`,
		`[{name: '@', monitor: {unknown: true}}]`,
		`[{name: '*', monitor: {}}]`,
		`[{name: '@', monitor: {}}, {name: '@', monitor: {}}]`,
	} {
		t.Run(bindings, func(t *testing.T) {
			gatusRepo(t, bindings)
			if out := importCLI(t, 2, "gatus"); len(out) != 0 {
				t.Fatalf("partial output: %s", out)
			}
		})
	}
}

func TestGatusSkipsArchived(t *testing.T) {
	gatusRepo(t, `[{name: '@', server: old, monitor: {}}]`)
	if err := os.MkdirAll("inventory/servers", 0755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "inventory/servers/old.yaml", "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: old, archived: {date: 2026-10-28, reason: retired}}\nspec: {address: 192.0.2.10}\n")
	writeImportFile(t, "inventory/domains/example.net.yaml", "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.net, archived: {date: 2026-10-28, reason: retired}}\nspec: {bindings: [{name: '@', monitor: {}}]}\n")
	repo, _, err := ledger.Load(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	if out, _, err := renderGatus(repo); err != nil || out != "endpoints: []\n" {
		t.Fatalf("archived target: %s %v", out, err)
	}
	// 在用记录引用归档 Server 会被完整校验拦下；生成器仍防御性跳过归档目标。
	importCLI(t, 2, "gatus")
	writeImportFile(t, "inventory/domains/example.com.yaml", "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec: {}\n")
	if out := string(importCLI(t, 0, "gatus")); out != "endpoints: []\n" {
		t.Fatal(out)
	}
}
