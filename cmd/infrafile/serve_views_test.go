package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
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
		if out.Code != 200 || !strings.Contains(out.Body.String(), `id="root"`) || !strings.Contains(out.Body.String(), `<script src="/app.js" type="module">`) {
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

func TestWorkbenchEmbeddedAssets(t *testing.T) {
	f := newWebFixture(t, homelab)
	const csp = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
	for name, mime := range map[string]string{"app.js": "application/javascript; charset=utf-8", "app.css": "text/css; charset=utf-8"} {
		out := f.send(f.request("GET", "/"+name, ""))
		compiled, err := workbenchFiles.ReadFile("web/dist/" + name)
		if err != nil || out.Code != 200 || len(compiled) == 0 || !bytes.Equal(out.Body.Bytes(), compiled) || out.Header().Get("Content-Type") != mime || out.Header().Get("Content-Security-Policy") != csp {
			t.Fatalf("compiled asset %s not served with exact policy/MIME: %d %v", name, out.Code, err)
		}
	}
	var embedded []string
	if err := fs.WalkDir(workbenchFiles, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			embedded = append(embedded, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(embedded, []string{"web/dist/app.css", "web/dist/app.js", "web/serve.html"}) {
		t.Fatal("unexpected embedded files", embedded)
	}
	for _, loggedIn := range []bool{false, true} {
		if loggedIn {
			f.login(t)
		}
		out := f.send(f.request("GET", "/assets", ""))
		html := out.Body.String()
		if out.Code != 200 || out.Header().Get("Content-Security-Policy") != csp || strings.Contains(html, "Server/web1") || strings.Contains(html, "sops://") {
			t.Fatal("unsafe HTML shell")
		}
		if loggedIn {
			for _, fragment := range []string{`id="root" data-repo=`, `id="session-controls" hidden`, `name="csrf-token"`, `action="/logout"`, `formaction="/revoke-all"`, `type="module"`} {
				if !strings.Contains(html, fragment) {
					t.Fatal("missing shell/form", fragment)
				}
			}
		} else if !strings.Contains(html, `action="/pair"`) || strings.Contains(html, `src="/app.js"`) {
			t.Fatal("pairing shell changed")
		}
		for _, path := range []string{"/app.js.map", "/web/dist/app.js", "/web/src/main.tsx", "/tests/csp.tsx", "/assets/app.js", "/package-lock.json", "/node_modules/react/index.js"} {
			if code := f.send(f.request("GET", path, "")).Code; code != 401 && code != 404 {
				t.Fatal("unexpected static route", path, code)
			}
		}
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
