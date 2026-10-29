package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/change"
)

func changeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git: %v %s", err, data)
	}
	return strings.TrimSpace(string(data))
}
func changeRepo(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("inventory transactions currently require Linux/macOS")
	}
	dir := t.TempDir()
	if _, err := initRepo(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "infrafile.yaml"), []byte(configTemplate+"storageVersion: v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inventory/accounts/home.yaml"), []byte("apiVersion: infrafile/v1alpha1\nkind: Account\nmetadata: {name: home}\nspec: {provider: example, secrets: {token: 'env://EXAMPLE_TOKEN'}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changeGit(t, dir, "init", "-b", "main")
	for k, v := range map[string]string{"user.name": "Infrafile Test", "user.email": "infrafile@example.com", "commit.gpgSign": "false", "core.hooksPath": filepath.Join(dir, ".git/hooks")} {
		changeGit(t, dir, "config", k, v)
	}
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "fixture")
	return dir
}
func changeCLI(t *testing.T, want int, args ...string) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	got := run(args, &out, &errOut)
	if got != want {
		t.Fatalf("%v: exit %d want %d; %s %s", args, got, want, out.String(), errOut.String())
	}
	return out.Bytes()
}

const browserEdit = `{"version":"v1","records":[{"apiVersion":"infrafile/v1alpha1","kind":"Account","metadata":{"name":"home"},"spec":{"provider":"example","owner":"reviewed"}}]}`

func TestChangeCLIWebSharedPreview(t *testing.T) {
	dir := changeRepo(t)
	requestPath := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(requestPath, []byte(browserEdit), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "preview.json")
	cli := changeCLI(t, 0, "change", "preview", dir, "--request", requestPath, "--out", out, "--json")
	var p change.Preview
	if json.Unmarshal(cli, &p) != nil {
		t.Fatal("bad CLI preview")
	}
	f := newWebFixture(t, dir)
	f.login(t)
	if res := f.send(f.request("POST", "/api/change/preview", browserEdit)); res.Code != 403 {
		t.Fatal("editing enabled by default")
	}
	f.app.allowEdit = true
	res := f.send(f.request("POST", "/api/change/preview", browserEdit))
	if res.Code != 200 || strings.Contains(res.Body.String(), "env://") || strings.Contains(res.Body.String(), dir) {
		t.Fatal("unsafe web preview", res.Code, res.Body.String())
	}
	var web map[string]any
	if json.Unmarshal(res.Body.Bytes(), &web) != nil || web["ok"] != true {
		t.Fatal(res.Body.String())
	}
	expected, err := change.Prepare(context.Background(), dir, p.Request, true)
	if err != nil {
		t.Fatal(err)
	}
	safe, err := browserPreview(context.Background(), expected)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(safe)
	if !bytes.Equal(want, res.Body.Bytes()) {
		t.Fatal("web changed shared preview")
	}
	changeCLI(t, 2, "change", "apply", out, dir, "--digest", "incorrect", "--json")
	changeCLI(t, 0, "change", "apply", out, dir, "--digest", p.Digest, "--json")
	changeCLI(t, 1, "change", "apply", out, dir, "--digest", p.Digest, "--json")
	if changeGit(t, dir, "status", "--porcelain") != "" {
		t.Fatal("CLI left dirty tree")
	}
}

func TestChangeHTTPValidationAndSave(t *testing.T) {
	dir := changeRepo(t)
	f := newWebFixture(t, dir)
	f.app.allowEdit = true
	if res := f.send(f.request("POST", "/api/change/preview", browserEdit)); res.Code != 401 {
		t.Fatal("unauthenticated edit")
	}
	f.login(t)
	for _, body := range []string{`{"version":"v1","version":"v1","records":[]}`, `{"Version":"v1","records":[]}`, `{"version":"v1","records":[],"path":"../escape"}`} {
		if res := f.send(f.request("POST", "/api/change/preview", body)); res.Code != 400 {
			t.Fatal(body, res.Code)
		}
	}
	req := f.request("POST", "/api/change/preview", browserEdit)
	req.Header.Del("X-CSRF-Token")
	if res := f.send(req); res.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	res := f.send(f.request("POST", "/api/change/preview", browserEdit))
	var p map[string]any
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &p) != nil {
		t.Fatal("preview", res.Code)
	}
	body := `{"request":` + browserEdit + `,"digest":"` + p["digest"].(string) + `"}`
	res = f.send(f.request("POST", "/api/change/apply", body))
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"status":"committed"`) {
		t.Fatal(res.Code, res.Body.String())
	}
	record, err := os.ReadFile(filepath.Join(dir, "inventory/accounts/home.yaml"))
	if err != nil || !bytes.Contains(record, []byte("env://EXAMPLE_TOKEN")) || !bytes.Contains(record, []byte("owner: reviewed")) {
		t.Fatal("secret reference not retained")
	}
	res = f.send(f.request("POST", "/api/change/apply", body))
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"status":"rejected"`) {
		t.Fatal("stale edit accepted", res.Code)
	}
}

func TestChangeWorkbenchRestartRecovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook fixture")
	}
	dir := changeRepo(t)
	f := newWebFixture(t, dir)
	f.app.allowEdit = true
	f.login(t)
	res := f.send(f.request("POST", "/api/change/preview", browserEdit))
	var p map[string]any
	json.Unmarshal(res.Body.Bytes(), &p)
	hook := filepath.Join(dir, ".git/hooks/pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	body := `{"request":` + browserEdit + `,"digest":"` + p["digest"].(string) + `"}`
	res = f.send(f.request("POST", "/api/change/apply", body))
	var result change.Result
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &result) != nil || result.Status != "recovery_required" {
		t.Fatal(res.Code, res.Body.String())
	}
	restarted := newWebFixture(t, dir)
	restarted.app.allowEdit = true
	restarted.login(t)
	if res = restarted.send(restarted.request("POST", "/api/list", `{}`)); res.Code != 422 {
		t.Fatal("partial records visible", res.Code)
	}
	if res = restarted.send(restarted.request("POST", "/api/change/status", `{}`)); res.Code != 200 || !strings.Contains(res.Body.String(), result.Transaction) {
		t.Fatal("missing recovery status")
	}
	if res = restarted.send(restarted.request("POST", "/api/change/recover", `{"id":"`+result.Transaction+`"}`)); res.Code != 200 || !strings.Contains(res.Body.String(), `"status":"rolled_back"`) {
		t.Fatal(res.Code, res.Body.String())
	}
	if res = restarted.send(restarted.request("POST", "/api/list", `{}`)); res.Code != 200 {
		t.Fatal("recovered queries unavailable")
	}
}
