package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAppOriginCLIAndMCP(t *testing.T) {
	emptyRepo(t)
	for _, kind := range []string{"servers", "domains", "apps"} {
		if err := os.MkdirAll("inventory/"+kind, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeImportFile(t, "inventory/servers/web.yaml", "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: web}\nspec: {address: 192.0.2.10}\n")
	writeImportFile(t, "inventory/domains/example.com.yaml", "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec:\n  bindings: [{name: '@', app: blog}]\n  dns: {provider: example, records: [{name: '@', type: A, content: 192.0.2.10}]}\n")
	for _, deployed := range []bool{false, true} {
		app := "apiVersion: infrafile/v1alpha1\nkind: App\nmetadata: {name: blog}\nspec: {}\n"
		want := 0
		if deployed {
			app = strings.Replace(app, "spec: {}", "spec: {deployments: [{server: web}]}", 1)
			want = 1
		}
		writeImportFile(t, "inventory/apps/blog.yaml", app)
		before := mcpFiles(t)
		got := missingCLI(t, time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC), want, "audit", "--rule", "origin_exposure", "--json")
		mcp := callMCP(t, mcpSession(t, "."), "audit", map[string]any{"rules": []string{"origin_exposure"}}, false)
		if !reflect.DeepEqual(got, mcp) || !reflect.DeepEqual(before, mcpFiles(t)) || len(got["findings"].([]any)) != want || len(got["notices"].([]any)) != 1-want {
			t.Fatalf("CLI/MCP mismatch or writes: %v %v", got, mcp)
		}
	}
}

func TestCommandsRejectSymlinkedLedgerRoots(t *testing.T) {
	for _, path := range []string{"infrafile.yaml", "inventory"} {
		t.Run(path, func(t *testing.T) {
			emptyRepo(t)
			outside := filepath.Join(t.TempDir(), path)
			if path == "inventory" {
				if err := os.MkdirAll("inventory/servers", 0755); err != nil {
					t.Fatal(err)
				}
				writeImportFile(t, "inventory/servers/web.yaml", "not valid: [synthetic-private-value")
			}
			if err := os.Rename(path, outside); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
			targetFile := outside
			if path == "inventory" {
				targetFile = filepath.Join(outside, "servers/web.yaml")
			}
			before, err := os.ReadFile(targetFile)
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"validate"}, {"list"}, {"audit"}, {"sync"}, {"import", "state", "--write"}, {"gatus"}, {"secret", "check"}} {
				var out, stderr bytes.Buffer
				code := runAt(append(args, "--json"), &out, &stderr, time.Now())
				if code == 0 || strings.Contains(out.String()+stderr.String(), "synthetic-private-value") {
					t.Fatalf("%v: exit=%d %s %s", args, code, &out, &stderr)
				}
			}
			callMCP(t, mcpSession(t, "."), "list", map[string]any{}, true)
			after, err := os.ReadFile(targetFile)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected input changed files")
			}
		})
	}
}
