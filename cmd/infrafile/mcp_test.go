package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpSession(t *testing.T, dir string) *mcp.ClientSession {
	return mcpSessionTimeout(t, dir, 30*time.Second)
}

func mcpSessionTimeout(t *testing.T, dir string, timeout time.Duration) *mcp.ClientSession {
	t.Helper()
	s, err := newMCPServer(dir, timeout, func() time.Time { return time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "infrafile-test", Version: "v1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		ss.Close()
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close(); ss.Close(); cancel() })
	return cs
}

func TestMCPTimeoutAndCancellation(t *testing.T) {
	controlRepo(t)
	t.Setenv("INFRAFILE_CONTROL_CASE", "timeout")
	s := mcpSessionTimeout(t, ".", 500*time.Millisecond)
	got := callMCP(t, s, "plan", map[string]any{"key": "Resource/edge", "action": "cdn-pause"}, true)
	if got["error"].(map[string]any)["code"] != "cancelled" {
		t.Fatal(got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "infrafile_plan", Arguments: map[string]any{"key": "Resource/edge", "action": "cdn-pause"}})
	if err == nil {
		t.Fatal("cancelled call succeeded")
	}
	callMCP(t, s, "list", map[string]any{}, false)
}

func TestMCPStdio(t *testing.T) {
	dir, err := filepath.Abs(homelab)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "mcp", "--dir", dir, "--json")
	cmd.Env = append(os.Environ(), "INFRAFILE_MCP_HELPER=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "v1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v %s", err, &stderr)
	}
	defer s.Close()
	list, err := s.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 6 {
		t.Fatalf("list: %v %v", list, err)
	}
	callMCP(t, s, "show", map[string]any{"key": "Server/web1"}, false)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %s", &stderr)
	}
}

func TestMCPOriginKeysAndUniqueLiveSelection(t *testing.T) {
	controlRepo(t)
	data, _ := os.ReadFile("inventory/resources/edge.yaml")
	edge := strings.Replace(string(data), "metadata: {name: edge}", "metadata: {name: edge, labels: {env: test}}", 1)
	edge = strings.Replace(edge, "zone_id: example-zone", "zone_id: example-zone, ruleset_id: example-ruleset, rule_id: example-rule", 1)
	writeImportFile(t, "inventory/resources/edge.yaml", edge)
	for _, dir := range []string{"servers", "domains"} {
		if err := os.MkdirAll("inventory/"+dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeImportFile(t, "inventory/servers/origin.yaml", "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: origin}\nspec: {provider: example, address: server.example.net}\n")
	writeImportFile(t, "inventory/resources/origin.yaml", "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: origin}\nspec: {provider: example, type: origin, endpoint: resource.example.net}\n")
	writeImportFile(t, "inventory/domains/example.net.yaml", "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.net}\nspec: {}\n")
	s := mcpSession(t, ".")
	for key, host := range map[string]string{"Server/origin": "server.example.net", "Resource/origin": "resource.example.net", "Domain/example.net": "example.net"} {
		got := callMCP(t, s, "plan", map[string]any{"labels": map[string]string{"env": "test"}, "action": "cdn-origin", "origin_key": key}, false)
		action := got["plan"].(map[string]any)["actions"].([]any)[0].(map[string]any)
		if action["parameters"].(map[string]any)["host"] != host {
			t.Fatal(got)
		}
		encoded, _ := json.Marshal(got["plan"])
		writeImportFile(t, "review.json", string(encoded))
		importCLI(t, 0, "plan", "show", "review.json")
	}
	for _, address := range []string{"192.0.2.10", "https://origin.example.net"} {
		writeImportFile(t, "inventory/servers/origin.yaml", fmt.Sprintf("apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: origin}\nspec: {provider: example, address: %s}\n", address))
		callMCP(t, s, "plan", map[string]any{"key": "Resource/edge", "action": "cdn-origin", "origin_key": "Server/origin"}, true)
	}
	writeImportFile(t, "inventory/servers/origin.yaml", "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: origin, archived: {date: '2026-01-01', reason: retired}}\nspec: {provider: example, address: origin.example.net}\n")
	callMCP(t, s, "plan", map[string]any{"key": "Resource/edge", "action": "cdn-origin", "origin_key": "Server/origin"}, true)
	callMCP(t, s, "plan", map[string]any{"key": "Resource/edge", "action": "cdn-origin", "origin_key": "Domain/missing.net"}, true)
	writeImportFile(t, "inventory/resources/other.yaml", strings.Replace(edge, "name: edge", "name: other", 1))
	for _, tool := range []string{"show", "plan"} {
		args := map[string]any{"labels": map[string]string{"env": "test"}}
		if tool == "plan" {
			args["action"] = "cdn-pause"
		}
		callMCP(t, s, tool, args, true)
	}
	writeImportFile(t, "inventory/resources/edge.yaml", strings.Replace(edge, "name: edge,", "name: edge, archived: {date: '2026-01-01', reason: retired},", 1))
	callMCP(t, s, "show", map[string]any{"key": "Resource/edge"}, false)
	callMCP(t, s, "plan", map[string]any{"key": "Resource/edge", "action": "cdn-pause"}, true)
	if _, err := os.Stat("changed"); !os.IsNotExist(err) {
		t.Fatal("plan performed control")
	}
}

func TestMCPCloudPlanRejectsCredentialReflection(t *testing.T) {
	emptyRepo(t)
	t.Setenv("MCP_CF_TOKEN", "synthetic-mcp-private-value")
	writeImportFile(t, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n  cloudflare:\n    instances:\n      home: {api_token: 'env://MCP_CF_TOKEN'}\n")
	if err := os.MkdirAll("inventory/resources", 0755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "inventory/resources/edge.yaml", "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: edge}\nspec:\n  provider: cloudflare\n  type: cdn\n  control: {plugin: cloudflare, instance: home, zone_id: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}\n")
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = cloudReplay(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-mcp-private-value" {
			t.Error("unexpected cloud request")
		}
		body := `{"success":true,"result":{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"example.com","paused":false,"description":"synthetic-mcp-private-value"}}`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	s := mcpSession(t, ".")
	got := callMCP(t, s, "plan", map[string]any{"key": "Resource/edge", "action": "cdn-pause"}, true)
	data, _ := json.Marshal(got)
	if bytes.Contains(data, []byte("synthetic-mcp-private-value")) {
		t.Fatal("reflected credential returned")
	}
}

func callMCP(t *testing.T, s *mcp.ClientSession, tool string, args any, wantError bool) map[string]any {
	t.Helper()
	r, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "infrafile_" + tool, Arguments: args})
	if err != nil || r.IsError != wantError {
		t.Fatalf("%s: %#v %v", tool, r, err)
	}
	data, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		t.Fatalf("invalid structured content: %s", data)
	}
	var textValue any
	if len(r.Content) != 1 || json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &textValue) != nil || !reflect.DeepEqual(textValue, value) {
		t.Fatal("text and structured content differ")
	}
	return value
}

func TestMCPRejectsRawTargetsAndUnknownArguments(t *testing.T) {
	controlRepo(t)
	s := mcpSession(t, ".")
	for _, key := range []string{"192.0.2.10", "example.com", "https://example.com", "../secrets", "env://PRIVATE", "Resource/missing", "Server/web1"} {
		callMCP(t, s, "show", map[string]any{"key": key}, true)
		callMCP(t, s, "plan", map[string]any{"key": key, "action": "cdn-pause"}, true)
	}
	for _, arg := range []string{"host", "address", "path", "dir", "command", "secret"} {
		got := callMCP(t, s, "plan", map[string]any{"key": "Resource/edge", "action": "cdn-pause", arg: "synthetic-mcp-private-value"}, true)
		data, _ := json.Marshal(got)
		if bytes.Contains(data, []byte("synthetic-mcp-private-value")) {
			t.Fatal("argument echoed in failure")
		}
	}
	for _, host := range []string{"192.0.2.10", "origin.example.net", "https://origin.example.net"} {
		callMCP(t, s, "plan", map[string]any{"key": "Resource/edge", "action": "cdn-origin", "origin_key": host}, true)
	}
	callMCP(t, s, "show", map[string]any{"labels": map[string]string{}}, true)
	callMCP(t, s, "show", map[string]any{"key": "Resource/edge", "labels": map[string]string{"env": "test"}}, true)
	callMCP(t, s, "show", json.RawMessage(`{"key":"Resource/missing","key":"Resource/edge"}`), true)
	if _, err := os.Stat("changed"); !os.IsNotExist(err) {
		t.Fatal("rejected request changed remote state")
	}
}

func TestMCPAllToolsExcludeCredentialsAndDoNotWrite(t *testing.T) {
	controlRepo(t)
	t.Setenv("MCP_PRIVATE_VALUE", "synthetic-mcp-private-value")
	data, _ := os.ReadFile("inventory/resources/edge.yaml")
	writeImportFile(t, "inventory/resources/edge.yaml", string(data)+"  secrets:\n    password: env://MCP_PRIVATE_VALUE\n    token: sops://missing.enc.yaml#token\n    other: op://Example/Item/password\n")
	before := mcpFiles(t)
	s := mcpSession(t, ".")
	listed, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 6 {
		t.Fatalf("tools: %v", listed.Tools)
	}
	args := map[string]any{"list": map[string]any{}, "show": map[string]any{"key": "Resource/edge"}, "expiry": map[string]any{}, "validate": map[string]any{}, "schema": map[string]any{}, "plan": map[string]any{"key": "Resource/edge", "action": "cdn-pause"}}
	for _, tool := range listed.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("not read-only: %s", tool.Name)
		}
		name := strings.TrimPrefix(tool.Name, "infrafile_")
		got := callMCP(t, s, name, args[name], false)
		encoded, _ := json.Marshal(got)
		if bytes.Contains(encoded, []byte("synthetic-mcp-private-value")) {
			t.Fatalf("secret returned by %s", name)
		}
		if name == "plan" && got["dry_run"] != true {
			t.Fatal("missing dry run marker")
		}
		if name == "schema" {
			bundle, _ := ledger.Bundle()
			var want any
			json.Unmarshal(bundle, &want)
			if !reflect.DeepEqual(got["schemas"], want) {
				t.Fatal("MCP schema differs from ledger")
			}
		}
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("MCP changed local files")
	}
	for _, tool := range []string{"apply", "sync", "run", "secret", "import"} {
		r, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "infrafile_" + tool, Arguments: map[string]any{}})
		if err == nil && (r == nil || !r.IsError) {
			t.Fatalf("write tool available: %s", tool)
		}
	}
}

func mcpFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestMCPLabelsFreshReadsAndExpiry(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	s := mcpSession(t, dir)
	got := callMCP(t, s, "list", map[string]any{"kind": "Server"}, false)
	if len(got["records"].([]any)) != 3 {
		t.Fatalf("servers: %v", got)
	}
	file := filepath.Join(dir, "inventory/servers/web1.yaml")
	data, _ := os.ReadFile(file)
	writeImportFile(t, file, strings.Replace(string(data), "  labels:", "  labels:\n    mcp-test: selected", 1))
	got = callMCP(t, s, "show", map[string]any{"labels": map[string]string{"mcp-test": "selected"}}, false)
	if got["record"].(map[string]any)["metadata"].(map[string]any)["name"] != "web1" {
		t.Fatal(got)
	}
	got = callMCP(t, s, "expiry", map[string]any{"kind": "Resource", "within_days": 0}, false)
	if len(got["entries"].([]any)) != 1 || got["ok"] != false {
		t.Fatal(got)
	}
	callMCP(t, s, "expiry", map[string]any{"within_days": -1}, true)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); callMCP(t, s, "show", map[string]any{"key": "Server/web1"}, false) }()
	}
	wg.Wait()
}

func TestMCPInvalidLedgerDoesNotEchoPlaintext(t *testing.T) {
	controlRepo(t)
	s := mcpSession(t, ".")
	data, _ := os.ReadFile("inventory/resources/edge.yaml")
	writeImportFile(t, "inventory/resources/edge.yaml", string(data)+"  password: synthetic-mcp-private-value\n")
	for _, tool := range []string{"list", "show", "expiry", "plan", "validate"} {
		args := map[string]any{}
		if tool == "show" {
			args["key"] = "Resource/edge"
		}
		if tool == "plan" {
			args["key"] = "Resource/edge"
			args["action"] = "cdn-pause"
		}
		got := callMCP(t, s, tool, args, tool != "validate")
		encoded, _ := json.Marshal(got)
		if bytes.Contains(encoded, []byte("synthetic-mcp-private-value")) {
			t.Fatalf("secret in %s", tool)
		}
		if tool == "validate" && got["ok"] != false {
			t.Fatal(got)
		}
	}
}

func TestMCPShowByPrimaryKey(t *testing.T) {
	s := mcpSession(t, homelab)
	result, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "infrafile_show", Arguments: map[string]any{"key": "Server/web1"}})
	if err != nil || result.IsError {
		t.Fatalf("show: %v %v", result, err)
	}
	data, _ := json.Marshal(result.StructuredContent)
	var got struct {
		Record struct {
			Kind string         `json:"kind"`
			Spec map[string]any `json:"spec"`
		} `json:"record"`
	}
	if json.Unmarshal(data, &got) != nil || got.Record.Kind != "Server" || got.Record.Spec["address"] != "203.0.113.10" {
		t.Fatalf("record: %s", data)
	}
	if _, ok := got.Record.Spec["secrets"]; ok {
		t.Fatal("MCP query exposed credential fields")
	}
}

func TestMCPGolden(t *testing.T) {
	path, err := filepath.Abs("testdata/golden/mcp.json")
	if err != nil {
		t.Fatal(err)
	}
	controlRepo(t)
	s := mcpSession(t, ".")
	list, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{"tools": list.Tools}
	for _, tool := range []string{"list", "show", "expiry", "validate", "plan"} {
		args := map[string]any{}
		if tool == "show" || tool == "plan" {
			args["key"] = "Resource/edge"
		}
		if tool == "plan" {
			args["action"] = "cdn-pause"
		}
		value[tool] = callMCP(t, s, tool, args, false)
	}
	value["error"] = callMCP(t, s, "show", map[string]any{"key": "192.0.2.1"}, true)
	got, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *update {
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("MCP contract differs from %s: %v", path, err)
	}
}
