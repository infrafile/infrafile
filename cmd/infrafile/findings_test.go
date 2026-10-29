package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func findingsCLI(t *testing.T, at time.Time, args ...string) (int, []byte) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := runAt(args, &out, &stderr, at)
	if code != 0 && code != 1 {
		t.Fatalf("%v: %d %s", args, code, &stderr)
	}
	return code, out.Bytes()
}

func TestFindingsCLIAndReadOnlyMCP(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "infrafile.yaml")
	b, _ := os.ReadFile(config)
	if err := os.WriteFile(config, append(b, []byte("\nstorageVersion: v1\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	at := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	_, data := findingsCLI(t, at, "findings", "refresh", "--max-age", "45d", "--json")
	var first struct {
		Written bool  `json:"written"`
		Changes []any `json:"changes"`
	}
	if json.Unmarshal(data, &first) != nil || !first.Written || len(first.Changes) == 0 {
		t.Fatal("refresh did not persist problems")
	}
	before := mcpFiles(t)
	_, data = findingsCLI(t, at, "findings", "refresh", "--max-age", "45d", "--json")
	var second struct {
		Written bool  `json:"written"`
		Changes []any `json:"changes"`
	}
	json.Unmarshal(data, &second)
	if second.Written || len(second.Changes) != 0 {
		t.Fatal("repeat appended duplicate changes")
	}
	_, listed := findingsCLI(t, at.Add(time.Hour), "findings", "list", "--json")
	var list struct {
		Present bool           `json:"present"`
		History map[string]any `json:"history"`
	}
	json.Unmarshal(listed, &list)
	if !list.Present || list.History["data"].(map[string]any)["checked_at"] != at.Format(time.RFC3339) {
		t.Fatal("query advanced time")
	}
	_, changes := findingsCLI(t, at, "findings", "changes", "--after", "0", "--json")
	var events struct {
		Changes []any `json:"changes"`
	}
	json.Unmarshal(changes, &events)
	if len(events.Changes) != len(first.Changes) {
		t.Fatal("cursor lost events")
	}
	_, exported := findingsCLI(t, at, "findings", "export")
	stored, err := os.ReadFile("state/.findings.json")
	if err != nil || !bytes.Equal(exported, stored) {
		t.Fatal("export is not byte-exact")
	}
	session := mcpSession(t, ".")
	for _, command := range []string{"audit", "inspect"} {
		_, cliBytes := findingsCLI(t, at, command, "--max-age", "45d", "--json")
		var cli map[string]any
		json.Unmarshal(cliBytes, &cli)
		if cli["history"] == nil {
			t.Fatalf("%s omitted saved history", command)
		}
		mcp := callMCP(t, session, command, map[string]any{"max_age_days": 45}, false)
		if !reflect.DeepEqual(cli, mcp) {
			t.Fatalf("%s CLI/MCP history differs", command)
		}
	}
	_, selected := findingsCLI(t, at, "inspect", "--key", "Server/web1", "--max-age", "45d", "--json")
	var view map[string]any
	json.Unmarshal(selected, &view)
	for _, raw := range view["history"].(map[string]any)["issues"].([]any) {
		key := raw.(map[string]any)["identity"].(map[string]any)["key"].(map[string]any)
		if key["target"] != "Server/web1" {
			t.Fatal("selected view leaked unrelated history")
		}
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("read or identical refresh wrote files")
	}
	// A malformed history must not silently turn a query into a healthy report.
	os.WriteFile("state/.findings.json", []byte("damaged"), 0o600)
	for _, command := range []string{"audit", "inspect"} {
		var out, stderr bytes.Buffer
		if code := runAt([]string{command, "--json"}, &out, &stderr, at); code != 2 || out.Len() != 0 {
			t.Fatal("malformed history was ignored", command, code)
		}
	}
}

func TestFindingsMissingAndUsage(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	_, data := findingsCLI(t, at, "findings", "list", dir, "--json")
	var v struct {
		Present bool `json:"present"`
		History any  `json:"history"`
	}
	json.Unmarshal(data, &v)
	if v.Present || v.History != nil {
		t.Fatal("missing history was fabricated")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("query created files")
	}
	for _, args := range [][]string{{"findings"}, {"findings", "refresh", dir}, {"findings", "refresh", dir, "--max-age", "x"}, {"findings", "prune", dir}, {"findings", "changes", dir, "--after", "1"}, {"findings", "export", dir}} {
		var out, stderr bytes.Buffer
		if code := runAt(args, &out, &stderr, at); code != 2 || out.Len() != 0 {
			t.Fatal(args, code)
		}
	}
}
