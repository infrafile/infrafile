package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/control"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/protocol"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The previous release is supplied explicitly; ordinary tests never download or install it.
func TestV030Compatibility(t *testing.T) {
	previous := os.Getenv("INFRAFILE_PREVIOUS_BINARY")
	if previous == "" {
		t.Skip("set INFRAFILE_PREVIOUS_BINARY to the official v0.3.0 binary")
	}
	previous, err := filepath.Abs(previous)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	oldCLI := func(want int, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, previous, args...)
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != want || (code == 2 && out.Len() != 0) {
			t.Fatalf("previous %v: exit=%d stdout=%s stderr=%s", args, code, &out, &stderr)
		}
		return out.Bytes()
	}
	var release struct {
		Build struct{ Version, Commit string }
	}
	if json.Unmarshal(oldCLI(0, "version", "--json"), &release) != nil || release.Build.Version != "0.3.0" || !strings.HasPrefix(release.Build.Commit, "3e43bce") {
		t.Fatalf("expected official v0.3.0, got %+v", release)
	}
	t.Logf("previous binary: version=%s commit=%s", release.Build.Version, release.Build.Commit)
	goldenImport(t)
	oldCLI(0, "sync", "example", "--json")
	before := mcpFiles(t)
	decode := func(data []byte) map[string]any {
		t.Helper()
		var value map[string]any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	for _, args := range [][]string{
		{"validate", "--json"}, {"show", "Server/web1", "--json"},
		{"expiry", "--json"}, {"audit", "--max-age", "7d", "--json"},
	} {
		var out, stderr bytes.Buffer
		code := run(args, &out, &stderr)
		if code != 0 && code != 1 {
			t.Fatalf("current %v: %d %s", args, code, &stderr)
		}
		old, current := decode(oldCLI(code, args...)), decode(out.Bytes())
		if args[0] == "audit" {
			delete(old, "as_of")
			delete(current, "as_of")
		}
		if !reflect.DeepEqual(old, current) {
			t.Fatalf("CLI %v differs: previous=%v current=%v", args, old, current)
		}
	}
	currentBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	connect := func(binary string, helper bool) *mcp.ClientSession {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, "mcp", "--dir", ".")
		if helper {
			cmd.Env = append(os.Environ(), "INFRAFILE_MCP_HELPER=1")
		}
		s, err := mcp.NewClient(&mcp.Implementation{Name: "compatibility-test", Version: "v1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	oldMCP, currentMCP := connect(previous, false), connect(currentBinary, true)
	oldTools, err := oldMCP.ListTools(ctx, nil)
	if err != nil || len(oldTools.Tools) != 7 {
		t.Fatalf("previous tools: %v %v", oldTools, err)
	}
	currentTools, err := currentMCP.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string][]byte{}
	for _, tool := range currentTools.Tools {
		tools[tool.Name], _ = json.Marshal(tool)
	}
	for _, tool := range oldTools.Tools {
		data, _ := json.Marshal(tool)
		if !bytes.Equal(data, tools[tool.Name]) {
			t.Fatalf("old tool contract changed: %s", tool.Name)
		}
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list", map[string]any{}}, {"show", map[string]any{"key": "Server/web1"}},
		{"expiry", map[string]any{}}, {"audit", map[string]any{"max_age_days": 7}},
		{"validate", map[string]any{}}, {"schema", map[string]any{}},
		{"plan", map[string]any{"key": "Resource/missing", "action": "cdn-pause"}},
	} {
		old := callMCP(t, oldMCP, tc.name, tc.args, tc.name == "plan")
		current := callMCP(t, currentMCP, tc.name, tc.args, tc.name == "plan")
		if tc.name == "audit" {
			delete(old, "as_of")
			delete(current, "as_of")
		}
		if tc.name == "schema" {
			delete(current["schemas"].(map[string]any)["config.schema.json"].(map[string]any)["properties"].(map[string]any), "storageVersion")
		}
		if !reflect.DeepEqual(old, current) {
			t.Fatalf("MCP %s differs: previous=%v current=%v", tc.name, old, current)
		}
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("queries changed legacy files")
	}
	plan := &control.Plan{Version: "v1", GeneratedAt: "2026-10-28T00:00:00Z", Actions: []control.Action{{
		Target: "Resource/edge", Binding: control.Binding{Plugin: "cloudflare", Instance: "home", Target: protocol.Target{ZoneID: strings.Repeat("a", 32)}},
		RecordHash: strings.Repeat("b", 64), ConfigHash: strings.Repeat("c", 64), Operation: "cdn-pause", Parameters: map[string]any{},
		Before: map[string]any{"paused": false}, BeforeHash: plugin.Hash(map[string]any{"paused": false}), After: map[string]any{"paused": true},
	}}}
	data, err := plan.Encode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "plan.json", string(data))
	if !reflect.DeepEqual(decode(oldCLI(0, "plan", "show", "plan.json", "--json")), decode(importCLI(t, 0, "plan", "show", "plan.json", "--json"))) {
		t.Fatal("v1 plan no longer interoperates")
	}
	for _, invalid := range [][]byte{
		bytes.Replace(data, []byte(`"version": "v1"`), []byte(`"version": "v99"`), 1),
		bytes.Replace(data, []byte(`"version": "v1"`), []byte(`"version": "v1", "version": "v1"`), 1),
		bytes.Repeat([]byte(" "), (4<<20)+1),
	} {
		writeImportFile(t, "invalid-plan.json", string(invalid))
		oldCLI(2, "plan", "show", "invalid-plan.json", "--json")
		if len(importCLI(t, 2, "plan", "show", "invalid-plan.json", "--json")) != 0 {
			t.Fatal("invalid plan emitted output")
		}
	}
	config, err := os.ReadFile("infrafile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "infrafile.yaml", string(config)+"\nstorageVersion: v1\n")
	importCLI(t, 0, "validate", "--json")
	oldCLI(1, "validate", "--json")
	marked := mcpFiles(t)
	oldCLI(2, "show", "Server/web1", "--json")
	oldCLI(2, "sync", "example", "--json")
	callMCP(t, oldMCP, "show", map[string]any{"key": "Server/web1"}, true)
	callMCP(t, currentMCP, "show", map[string]any{"key": "Server/web1"}, false)
	if !reflect.DeepEqual(marked, mcpFiles(t)) {
		t.Fatal("rejected legacy write changed files")
	}
	writeImportFile(t, "infrafile.yaml", string(config))
	oldCLI(0, "validate", "--json")
}
