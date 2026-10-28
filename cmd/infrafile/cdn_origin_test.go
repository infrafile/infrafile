package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCDNOriginSyncAuditAndMCP(t *testing.T) {
	mode := "equal"
	missingRepo(t, func(r *http.Request) (string, int) {
		if mode == "limit" && r.URL.Path == "/client/v4/zones" {
			items := strings.TrimSuffix(strings.Repeat(`{"id":"zone","name":"example.com"},`, 201), ",")
			return `{"success":true,"result":[` + items + `],"result_info":{"page":1,"total_count":201}}`, 200
		}
		if !strings.HasSuffix(r.URL.Path, "/entrypoint") {
			return missingResponse(r, true), 200
		}
		switch mode {
		case "forbidden":
			return "synthetic-private-error", 403
		case "timeout":
			<-r.Context().Done()
			return "", 408
		}
		host := "origin.example.com"
		if mode == "drift" {
			host = "changed.example.net"
		}
		return fmt.Sprintf(`{"success":true,"result":{"kind":"zone","phase":"http_request_origin","rules":[{"enabled":true,"action":"route","expression":"true","action_parameters":{"origin":{"host":%q,"port":8443},"host_header":"not-an-origin.example.net"}}]}}`, host), 200
	})
	config := strings.Replace(watchedConfig, "home: {api_token:", "home: {collect_origin: true, api_token:", 1)
	resource := strings.Replace(watchedResource, "endpoint: example.com}", "endpoint: example.com, origin: {hosts: [origin.example.com], port: 8443}}", 1)
	writeImportFile(t, "infrafile.yaml", config)
	writeImportFile(t, "inventory/resources/edge.yaml", resource)
	start := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	auditAt := start.Add(2 * time.Hour)
	args := []string{"audit", "--rule", "cdn_drift", "--max-age", "7d", "--json"}
	for i, step := range []struct {
		mode     string
		findings int
		unknown  bool
	}{{"equal", 0, false}, {"drift", 1, false}, {"forbidden", 0, true}, {"timeout", 0, true}, {"limit", 0, true}, {"drift", 1, false}} {
		mode = step.mode
		before, _ := os.ReadFile("state/cloudflare/home.yaml")
		exit := 0
		if mode == "forbidden" || mode == "timeout" || mode == "limit" {
			exit = 1
		}
		missingCLI(t, start.Add(time.Duration(i)*time.Minute), exit, "sync", "--timeout", "100ms", "--json")
		after, _ := os.ReadFile("state/cloudflare/home.yaml")
		if exit == 1 && !bytes.Equal(before, after) {
			t.Fatal("detail failure replaced old snapshot")
		}
		got := missingCLI(t, auditAt, step.findings, args...)
		unknown := false
		for _, n := range got["notices"].([]any) {
			unknown = unknown || n.(map[string]any)["code"] == "cdn_origin_unknown"
		}
		if len(got["findings"].([]any)) != step.findings || unknown != step.unknown {
			t.Fatalf("%s: %v", mode, got)
		}
		mcp := callMCP(t, mcpSession(t, "."), "audit", map[string]any{"rules": []string{"cdn_drift"}, "max_age_days": 7}, false)
		if !reflect.DeepEqual(got, mcp) {
			t.Fatalf("CLI/MCP mismatch: %v %v", got, mcp)
		}
	}
	// 更改期望不必重新同步；配置、快照或回执失配必须停止判断。
	writeImportFile(t, "inventory/resources/edge.yaml", strings.Replace(resource, "hosts: [origin.example.com]", "hosts: [changed.example.net]", 1))
	missingCLI(t, auditAt, 0, args...)
	writeImportFile(t, "inventory/resources/edge.yaml", resource)
	writeImportFile(t, "infrafile.yaml", strings.Replace(config, "collect_origin: true", "account_id: other, collect_origin: true", 1))
	got := missingCLI(t, auditAt, 0, args...)
	if !strings.Contains(fmt.Sprint(got), "cdn_origin_unknown") {
		t.Fatal(got)
	}
	writeImportFile(t, "infrafile.yaml", config)
	snap, _ := os.ReadFile("state/cloudflare/home.yaml")
	writeImportFile(t, "state/cloudflare/home.yaml", string(snap)+"# changed\n")
	got = missingCLI(t, auditAt, 0, args...)
	if !strings.Contains(fmt.Sprint(got), "cdn_origin_unknown") {
		t.Fatal(got)
	}
}

func goldenCDNOrigin(mode string) func(*testing.T) {
	return func(t *testing.T) {
		missingRepo(t, func(r *http.Request) (string, int) {
			if !strings.HasSuffix(r.URL.Path, "/entrypoint") {
				return missingResponse(r, true), 200
			}
			host := "origin.example.com"
			if mode == "drift" {
				host = "changed.example.net"
			}
			return fmt.Sprintf(`{"success":true,"result":{"kind":"zone","phase":"http_request_origin","rules":[{"enabled":true,"action":"route","expression":"true","action_parameters":{"origin":{"host":%q,"port":8443}}}]}}`, host), 200
		})
		writeImportFile(t, "infrafile.yaml", strings.Replace(watchedConfig, "home: {api_token:", "home: {collect_origin: true, api_token:", 1))
		expected := "hosts: [origin.example.com], port: 8443"
		if mode == "unknown" {
			expected += ", protocol: https"
		}
		writeImportFile(t, "inventory/resources/edge.yaml", strings.Replace(watchedResource, "endpoint: example.com}", "endpoint: example.com, origin: {"+expected+"}}", 1))
		missingCLI(t, time.Date(2026, 12, 1, 11, 0, 0, 0, time.UTC), 0, "sync", "--json")
	}
}
