package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestCapabilitiesOfflineAndMCPParity(t *testing.T) {
	controlRepo(t)
	// A broken reference must not be resolved by either capability query.
	writeImportFile(t, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n  cloudflare:\n    instances:\n      main:\n        api_token: sops://missing.enc.yaml#absent\n")
	before := mcpFiles(t)
	cli := importCLI(t, 0, "capabilities", "--json")
	var want map[string]any
	if err := json.Unmarshal([]byte(cli), &want); err != nil {
		t.Fatal(err)
	}
	s := mcpSession(t, ".")
	got := callMCP(t, s, "capabilities", map[string]any{}, false)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("CLI and MCP catalogs differ")
	}
	callMCP(t, s, "capabilities", map[string]any{"dir": "/tmp/private"}, true)
	callMCP(t, s, "capabilities", json.RawMessage(`{"key":"Server/web1","key":"Server/web2"}`), true)
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("capability query wrote files")
	}
	t.Chdir(t.TempDir())
	if empty := importCLI(t, 0, "capabilities", "--json"); !bytes.Equal(empty, cli) {
		t.Fatal("catalog depends on ledger, credentials or current directory")
	}
	text := importCLI(t, 0, "capabilities")
	if !bytes.Contains(text, []byte("cloudflare")) || !bytes.Contains(text, []byte("conditional")) {
		t.Fatal(text)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"capabilities", "unexpected", "--json"}, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
		t.Fatalf("invalid arguments: %d %s %s", code, &stdout, &stderr)
	}
}
