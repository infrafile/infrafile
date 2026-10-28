package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/snapshot"
)

const watchedConfig = `apiVersion: infrafile/v1alpha1
kind: Config
plugins:
  cloudflare:
    instances:
      home: {api_token: 'env://INFRAFILE_TEST_PRESENCE_TOKEN'}
audit:
  watch:
    - {target: Resource/edge, plugin: cloudflare, instance: home, service: zones}
`
const watchedResource = "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: edge}\nspec: {provider: cloudflare, type: dns-zone, resource_id: zone, endpoint: example.com}\n"

func goldenMissing(mode string) func(*testing.T) {
	return func(t *testing.T) {
		present := mode != "unknown"
		missingRepo(t, func(r *http.Request) (string, int) { return missingResponse(r, present), 200 })
		start := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
		missingCLI(t, start, 0, "sync", "--json")
		present = false
		missingCLI(t, start.Add(time.Hour), 0, "sync", "--json")
		if mode == "waived" {
			writeImportFile(t, "inventory/resources/edge.yaml", strings.Replace(watchedResource, "name: edge}", "name: edge, waivers: [{rule: missing, reason: 核对权限期间暂缓处理}]}", 1))
		}
	}
}

func missingRepo(t *testing.T, reply func(*http.Request) (string, int)) {
	t.Helper()
	emptyRepo(t)
	t.Setenv("INFRAFILE_TEST_PRESENCE_TOKEN", "synthetic-presence-token")
	writeImportFile(t, "infrafile.yaml", watchedConfig)
	if err := os.MkdirAll("inventory/resources", 0755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "inventory/resources/edge.yaml", watchedResource)
	old := http.DefaultTransport
	http.DefaultTransport = cloudReplay(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Host != "api.cloudflare.com" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Host)
		}
		body, status := reply(r)
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
}

func missingResponse(r *http.Request, present bool) string {
	result, count := "[]", 0
	if r.URL.Path == "/client/v4/zones" && present {
		result = `[{"id":"zone","name":"example.com"}]`
		count = 1
	}
	return fmt.Sprintf(`{"success":true,"result":%s,"result_info":{"page":1,"total_count":%d}}`, result, count)
}

func missingCLI(t *testing.T, at time.Time, want int, args ...string) map[string]any {
	t.Helper()
	var out, stderr bytes.Buffer
	if code := runAt(args, &out, &stderr, at); code != want {
		t.Fatalf("%v: exit=%d %s %s", args, code, &out, &stderr)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMissingSyncLifecycleAndMCP(t *testing.T) {
	mode := "empty"
	missingRepo(t, func(r *http.Request) (string, int) {
		switch mode {
		case "forbidden":
			return "synthetic-private-provider-error", 403
		case "timeout":
			<-r.Context().Done()
			return "", 408
		}
		return missingResponse(r, mode == "present"), 200
	})
	start := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	auditAt := start.Add(2 * time.Hour)
	args := []string{"audit", "--rule", "missing", "--max-age", "7d", "--json"}
	var positive []byte
	for i, step := range []struct {
		mode     string
		findings int
		unknown  bool
	}{
		{"empty", 0, true}, {"present", 0, false}, {"empty", 1, false}, {"forbidden", 0, true}, {"timeout", 0, true}, {"present", 0, false}, {"empty", 1, false},
	} {
		mode = step.mode
		want := 0
		if mode == "forbidden" || mode == "timeout" {
			want = 1
		}
		before, _ := os.ReadFile("state/cloudflare/home.yaml")
		missingCLI(t, start.Add(time.Duration(i)*time.Minute), want, "sync", "--timeout", "100ms", "--json")
		base, _ := os.ReadFile("state/" + snapshot.PresenceFile)
		if mode == "present" {
			positive = base
		} else if !bytes.Equal(base, positive) {
			t.Fatal("absence or failure advanced positive baseline")
		}
		if want == 1 {
			after, _ := os.ReadFile("state/cloudflare/home.yaml")
			if !bytes.Equal(before, after) {
				t.Fatal("failed sync replaced snapshot")
			}
		}
		got := missingCLI(t, auditAt, step.findings, args...)
		if len(got["findings"].([]any)) != step.findings {
			t.Fatalf("%s findings: %v", mode, got)
		}
		unknown := false
		for _, n := range got["notices"].([]any) {
			if n.(map[string]any)["code"] == "missing_unknown" {
				unknown = true
			}
		}
		if unknown != step.unknown {
			t.Fatalf("%s unknown: %v", mode, got)
		}
		if step.findings == 1 {
			before, _ := os.ReadFile("state/" + snapshot.PresenceFile)
			mcp := callMCP(t, mcpSession(t, "."), "audit", map[string]any{"rules": []string{"missing"}, "max_age_days": 7}, false)
			if !reflect.DeepEqual(mcp, got) {
				t.Fatalf("MCP/CLI mismatch: %v %v", mcp, got)
			}
			after, _ := os.ReadFile("state/" + snapshot.PresenceFile)
			if !bytes.Equal(before, after) {
				t.Fatal("audit changed baseline")
			}
		}
	}
	// 豁免只作用于已声明的目标，过期后恢复发现。
	for _, tc := range []struct {
		target, until string
		exit          int
		waived        bool
	}{
		{"other", "2026-12-02", 1, false}, {"edge", "2026-12-02", 0, true}, {"edge", "2026-11-30", 1, false},
	} {
		writeImportFile(t, "inventory/resources/edge.yaml", watchedResource)
		asset := strings.Replace(watchedResource, "name: edge}", "name: "+tc.target+", waivers: [{rule: missing, reason: scope-under-review, until: '"+tc.until+"'}]}", 1)
		writeImportFile(t, "inventory/resources/"+tc.target+".yaml", asset)
		got := missingCLI(t, auditAt, tc.exit, args...)
		f := got["findings"].([]any)[0].(map[string]any)
		if (f["waiver"] != nil) != tc.waived {
			t.Fatalf("waiver scope: %v", got)
		}
		mcp := callMCP(t, mcpSession(t, "."), "audit", map[string]any{"rules": []string{"missing"}, "max_age_days": 7}, false)
		if !reflect.DeepEqual(mcp, got) {
			t.Fatal("waiver MCP/CLI mismatch")
		}
	}
	writeImportFile(t, "inventory/resources/edge.yaml", watchedResource)
	// 配置变化后即使采集失败也清除旧绑定；恢复配置不能复活旧基线。
	writeImportFile(t, "infrafile.yaml", strings.Replace(watchedConfig, "home: {api_token:", "home: {account_id: another-account, api_token:", 1))
	missingCLI(t, auditAt, 0, args...)
	mode = "forbidden"
	missingCLI(t, start.Add(10*time.Minute), 1, "sync", "--json")
	writeImportFile(t, "infrafile.yaml", watchedConfig)
	mode = "empty"
	missingCLI(t, start.Add(11*time.Minute), 0, "sync", "--json")
	got := missingCLI(t, auditAt, 0, args...)
	if len(got["findings"].([]any)) != 0 {
		t.Fatal("restored old baseline")
	}
	// 自然身份变化也失效；备注不改变匹配身份。
	mode = "present"
	missingCLI(t, start.Add(12*time.Minute), 0, "sync", "--json")
	mode = "empty"
	missingCLI(t, start.Add(13*time.Minute), 0, "sync", "--json")
	writeImportFile(t, "inventory/resources/edge.yaml", strings.Replace(watchedResource, "name: edge}", "name: edge, description: harmless-note}", 1))
	missingCLI(t, auditAt, 1, args...)
	writeImportFile(t, "inventory/resources/edge.yaml", strings.Replace(watchedResource, "resource_id: zone", "resource_id: new-zone", 1))
	missingCLI(t, auditAt, 0, args...)
	// 移除全部插件后 sync 仍清除不再声明的基线。
	writeImportFile(t, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\n")
	missingCLI(t, start.Add(14*time.Minute), 0, "sync", "--json")
	base, err := snapshot.ReadPresence(context.Background(), ".")
	if err != nil || base == nil || len(base.Spec) != 0 {
		t.Fatalf("removed watch retained baseline: %+v %v", base, err)
	}
}

func TestPresenceWriteFailureDoesNotQualify(t *testing.T) {
	blocked := false
	missingRepo(t, func(r *http.Request) (string, int) {
		if !blocked {
			blocked = true
			if err := os.Mkdir("state/"+snapshot.PresenceFile, 0755); err != nil {
				t.Fatal(err)
			}
		}
		return missingResponse(r, true), 200
	})
	at := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	got := missingCLI(t, at, 1, "sync", "--json")
	if !strings.Contains(fmt.Sprint(got), "write") {
		t.Fatal(got)
	}
	status, err := snapshot.ReadStatus(context.Background(), ".")
	if err != nil || status.Spec["cloudflare"]["home"].Status != "failed" || status.Spec["cloudflare"]["home"].Code != "write" {
		t.Fatalf("write failure receipt: %+v %v", status, err)
	}
	if err := os.Remove("state/" + snapshot.PresenceFile); err != nil {
		t.Fatal(err)
	}
	got = missingCLI(t, at.Add(time.Hour), 0, "audit", "--rule", "missing", "--max-age", "7d", "--json")
	if len(got["findings"].([]any)) != 0 {
		t.Fatal("failed baseline write qualified missing")
	}
	writeImportFile(t, filepath.Join("state", snapshot.PresenceFile), "password: synthetic-private-presence-value\n")
	var out, stderr bytes.Buffer
	if code := runAt([]string{"audit", "--rule", "missing", "--max-age", "7d", "--json"}, &out, &stderr, at); code != 2 || strings.Contains(stderr.String(), "synthetic-private") {
		t.Fatalf("corrupt baseline: %d %s %s", code, &out, &stderr)
	}
	callMCP(t, mcpSession(t, "."), "audit", map[string]any{"rules": []string{"missing"}, "max_age_days": 7}, true)
}
