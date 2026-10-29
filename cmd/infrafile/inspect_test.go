package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInspectCLIAndMCPAgreeOfflineAndDoNotWrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	before := mcpFiles(t)
	oldHTTP, oldDNS := http.DefaultTransport, net.DefaultResolver
	http.DefaultTransport = cloudReplay(func(*http.Request) (*http.Response, error) { t.Fatal("inspect contacted HTTP"); return nil, nil })
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("inspect contacted DNS")
		return nil, nil
	}}
	t.Cleanup(func() { http.DefaultTransport = oldHTTP; net.DefaultResolver = oldDNS })
	bin := t.TempDir()
	marker := filepath.Join(bin, "called")
	for _, name := range []string{"ssh", "sops", "op", "infrafile-example"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf called >> \"$INFRAFILE_INSPECT_CALLS\"\nexit 99\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INFRAFILE_INSPECT_CALLS", marker)
	when := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	args := []string{"inspect", "--key", "Domain/example.com", "--max-age", "7d", "--json"}
	var stdout, stderr bytes.Buffer
	if code := runAt(args, &stdout, &stderr, when); code != 0 {
		t.Fatalf("%d %s", code, &stderr)
	}
	var expected map[string]any
	decoder := json.NewDecoder(&stdout)
	if err := decoder.Decode(&expected); err != nil {
		t.Fatal(err)
	}
	s := mcpSession(t, ".")
	got := callMCP(t, s, "inspect", map[string]any{"key": "Domain/example.com", "max_age_days": 7}, false)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("CLI/MCP differ: %v %v", expected, got)
	}
	if expected["ok"] != false {
		t.Fatal("legacy snapshots must not appear fully covered")
	}
	for _, args := range []map[string]any{{"labels": map[string]string{"env": "prod"}}, {"kind": "Server"}, {}} {
		callMCP(t, s, "inspect", args, false)
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("inspect wrote repository")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("inspect executed a program")
	}
}

func TestInspectRejectsUnsafeAndInvalidArguments(t *testing.T) {
	emptyRepo(t)
	s := mcpSession(t, ".")
	for _, args := range []any{map[string]any{"dir": "/private"}, map[string]any{"key": "https://example.com"}, map[string]any{"key": "Domain/example.com", "labels": map[string]string{"env": "prod"}}, map[string]any{"max_age_days": -1}, map[string]any{"max_age_days": 1.5}, json.RawMessage(`{"kind":"Server","kind":"Domain"}`)} {
		callMCP(t, s, "inspect", args, true)
	}
	for _, args := range [][]string{{"--max-age", "-1d"}, {"--max-age", "bad"}, {"--kind", "unknown"}, {"--key", "Domain/missing"}, {"--key", "Domain/example.com", "--label", "env=prod"}} {
		var out, err bytes.Buffer
		if code := run(append([]string{"inspect", "--json"}, args...), &out, &err); code != 2 || out.Len() != 0 {
			t.Fatalf("args=%v code=%d stdout=%s stderr=%s", args, code, &out, &err)
		}
	}
}

func TestInspectCLIRefusesSensitiveSnapshotReflection(t *testing.T) {
	emptyRepo(t)
	writeImportFile(t, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n  aliyun:\n    instances:\n      home: {services: [cdn]}\n")
	value := "ghp_" + strings.Repeat("x", 36)
	importSnapshot(t, "aliyun", "home", fmt.Sprintf("cdn: [{name: edge.example.com, status: '%s'}]\n", value))
	var out, stderr bytes.Buffer
	if code := run([]string{"inspect", "--json"}, &out, &stderr); code != 2 || out.Len() != 0 || strings.Contains(stderr.String(), value) {
		t.Fatalf("reflection code=%d stdout=%s stderr=%s", code, &out, &stderr)
	}
	callMCP(t, mcpSession(t, "."), "inspect", map[string]any{}, true)
}
