package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/audit"
)

func TestWorkbenchPagesAndReadOnlyChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "infrafile.yaml")
	data, _ := os.ReadFile(config)
	if err := os.WriteFile(config, append(data, []byte("\nstorageVersion: v1\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	f := newWebFixture(t, dir)
	f.login(t)
	t.Chdir(dir)
	findingsCLI(t, f.clock, "findings", "refresh", "--max-age", "7d", "--json")
	before := mcpFiles(t)
	for _, path := range []string{"/assets", "/inspections", "/changes"} {
		out := f.send(f.request("GET", path, ""))
		if out.Code != 200 || !strings.Contains(out.Body.String(), "aria-label=\"工作台\"") || !strings.Contains(out.Body.String(), "/app.js") {
			t.Fatal("page not embedded", path, out.Code)
		}
	}
	out := f.send(f.request("POST", "/api/changes", "{}"))
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	_, cli := findingsCLI(t, f.clock, "findings", "changes", "--json")
	var actual, want any
	if json.Unmarshal(out.Body.Bytes(), &actual) != nil || json.Unmarshal(cli, &want) != nil || !reflect.DeepEqual(actual, want) {
		t.Fatal("changes differs from CLI")
	}
	for _, body := range []string{`{"after":1}`, `{"dir":"/tmp"}`, `{"command":"refresh"}`, `{"key":"Server/web1"}`} {
		if out := f.send(f.request("POST", "/api/changes", body)); out.Code != 400 {
			t.Fatal(body, out.Code)
		}
	}
	for _, body := range []string{`{}`, `{"key":"../../outside"}`, `{"key":"Server/web1","labels":{}}`, `{"key":"Server/web1","key":"Server/web1"}`} {
		if out := f.send(f.request("POST", "/api/context", body)); out.Code != 400 {
			t.Fatal(body, out.Code)
		}
	}
	out = f.send(f.request("POST", "/api/context", `{"key":"Server/web1","max_age_days":7}`))
	if out.Code != 200 || strings.Contains(out.Body.String(), "sops://") {
		t.Fatal(out.Code, out.Body.String())
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("views changed ledger or history")
	}
}

func TestWorkbenchChangesUsesRetainedPrefixWithoutRounding(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "infrafile.yaml")
	data, _ := os.ReadFile(config)
	if err := os.WriteFile(config, append(data, []byte("\nstorageVersion: v1\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	f := newWebFixture(t, dir)
	f.login(t)
	t.Chdir(dir)
	findingsCLI(t, f.clock, "findings", "refresh", "--max-age", "7d", "--json")
	h, err := audit.ReadHistory(context.Background(), dir)
	if err != nil || h == nil || len(h.Data.Changes) == 0 {
		t.Fatal("history fixture missing", err)
	}
	const base uint64 = 9007199254740993
	h.Data.LastSequence += base
	h.Data.PrunedThrough += base
	for index := range h.Data.Changes {
		h.Data.Changes[index].Sequence += base
	}
	encoded, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state/.findings.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	out := f.send(f.request("POST", "/api/changes", "{}"))
	if out.Code != 200 {
		t.Fatal("retained prefix refused", out.Code, out.Body.String())
	}
	want, err := mcpCLI(context.Background(), f.clock, "findings", "changes", dir, "--after", strconv.FormatUint(base, 10), "--json")
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(want)
	if !bytes.Equal(canonical, out.Body.Bytes()) {
		t.Fatal("retained changes differ from exact CLI numbers")
	}
}
