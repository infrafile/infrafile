package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOpsRefreshHelper(t *testing.T) {
	marker := -1
	for i, arg := range os.Args {
		if arg == "--ops-refresh-helper" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	args := os.Args[marker+1:]
	os.WriteFile(args[1], []byte("started"), 0600)
	switch args[0] {
	case "fail":
		os.Exit(1)
	case "flood":
		fmt.Print(strings.Repeat("x", 10000))
		os.Exit(0)
	case "wait":
		time.Sleep(10 * time.Second)
	}
	raw, err := os.ReadFile(args[2])
	if err != nil {
		os.Exit(2)
	}
	os.Stdout.Write(raw)
	os.Exit(0)
}
func refreshFixture(t *testing.T, mode string) (*webFixture, string, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX generation lock")
	}
	f, model, _ := opsFixture(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "generations")
	os.Mkdir(root, 0700)
	pkg := filepath.Join(root, strings.Repeat("a", 32))
	dir := filepath.Join(pkg, "bundle/candidate")
	os.MkdirAll(dir, 0700)
	if err := os.CopyFS(dir, os.DirFS(f.app.dir)); err != nil {
		t.Fatal(err)
	}
	model.CapturedAt = f.clock.Format(time.RFC3339Nano)
	raw, _ := json.Marshal(model)
	os.WriteFile(filepath.Join(pkg, "ops.json"), raw, 0600)
	digest := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	generated := opsGenerationOutput{Version: "infrafile-ops-generation/v1", Package: pkg, OverviewSHA256: digest(raw), Summary: model.Summary}
	packet := filepath.Join(base, "output.json")
	b, _ := json.Marshal(generated)
	os.WriteFile(packet, b, 0600)
	started := filepath.Join(base, "started")
	exe, _ := os.Executable()
	wrapper := filepath.Join(base, "generator")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nexec " + quote(exe) + " -test.run=^TestOpsRefreshHelper$ -- --ops-refresh-helper " + quote(mode) + " " + quote(started) + " " + quote(packet) + "\n"
	os.WriteFile(wrapper, []byte(script), 0700)
	lock := filepath.Join(base, "shared.lock")
	os.WriteFile(lock, []byte("0"), 0600)
	seconds := 3
	if mode == "wait" {
		seconds = 1
	}
	cfg := opsRefreshConfig{Version: "infrafile-ops-refresh/v1", Executable: wrapper, Command: []string{wrapper}, Directory: base, Files: map[string]string{wrapper: digest([]byte(script))}, GenerationRoot: root, LockFile: lock, TimeoutSeconds: seconds}
	config := filepath.Join(base, "refresh.json")
	b, _ = json.Marshal(cfg)
	os.WriteFile(config, b, 0600)
	var err error
	f.app.opsRefresher, err = f.app.loadOpsRefresher(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	return f, started, pkg, packet
}
func decodeRefresh(t *testing.T, out *httptest.ResponseRecorder) opsRefreshResult {
	t.Helper()
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	var result opsRefreshResult
	if json.Unmarshal(out.Body.Bytes(), &result) != nil {
		t.Fatal("bad result")
	}
	return result
}
func TestOpsRefreshAuthenticationAndNoCallerPaths(t *testing.T) {
	f, started, _, _ := refreshFixture(t, "ok")
	if r := f.send(f.request("POST", "/api/ops-refresh", "{}")); r.Code != 401 {
		t.Fatal(r.Code)
	}
	f.login(t)
	for _, body := range []string{`{"command":"arbitrary"}`, `{"path":"other"}`, `{"x":1,"x":2}`} {
		if r := f.send(f.request("POST", "/api/ops-refresh", body)); r.Code != 400 {
			t.Fatal(r.Code)
		}
	}
	r := f.request("POST", "/api/ops-refresh", "{}")
	r.Header.Set("X-CSRF-Token", "invalid")
	if out := f.send(r); out.Code != 403 {
		t.Fatal(out.Code)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatal("invalid request started producer")
	}
}
func TestOpsRefreshPublishesCompleteGenerationWithoutReplacingSession(t *testing.T) {
	f, _, pkg, _ := refreshFixture(t, "ok")
	f.login(t)
	session, csrf := f.session, f.csrf
	oldDir, oldFile := f.app.dir, f.app.opsFile
	old, _ := os.ReadFile(oldFile)
	result := decodeRefresh(t, f.send(f.request("POST", "/api/ops-refresh", "{}")))
	if result.Status != "PASS" || f.app.dir != filepath.Join(pkg, "bundle/candidate") {
		t.Fatal(result)
	}
	if f.session != session || f.csrf != csrf || f.app.dir == oldDir {
		t.Fatal("session or root transition failed")
	}
	readOps(t, f)
	after, _ := os.ReadFile(oldFile)
	if string(after) != string(old) {
		t.Fatal("old evidence modified")
	}
	if f.app.opsReader != nil || f.app.opsActor != nil {
		t.Fatal("refresh enabled unbound remote operations")
	}
}
func TestOpsRefreshFailuresKeepOldGeneration(t *testing.T) {
	for _, mode := range []string{"fail", "flood", "wait", "wrong-hash", "outside-root", "bad-ledger"} {
		t.Run(mode, func(t *testing.T) {
			helper := mode
			if mode == "wrong-hash" || mode == "outside-root" || mode == "bad-ledger" {
				helper = "ok"
			}
			f, _, pkg, packet := refreshFixture(t, helper)
			f.login(t)
			oldDir, oldFile := f.app.dir, f.app.opsFile
			if mode == "wrong-hash" || mode == "outside-root" {
				raw, _ := os.ReadFile(packet)
				var g opsGenerationOutput
				json.Unmarshal(raw, &g)
				if mode == "wrong-hash" {
					g.OverviewSHA256 = strings.Repeat("0", 64)
				} else {
					g.Package = filepath.Dir(pkg)
				}
				raw, _ = json.Marshal(g)
				os.WriteFile(packet, raw, 0600)
			}
			if mode == "bad-ledger" {
				os.WriteFile(filepath.Join(pkg, "bundle/candidate/infrafile.yaml"), []byte("invalid"), 0600)
			}
			result := decodeRefresh(t, f.send(f.request("POST", "/api/ops-refresh", "{}")))
			if result.Status == "PASS" || f.app.dir != oldDir || f.app.opsFile != oldFile {
				t.Fatal("partial generation published", result)
			}
			readOps(t, f)
		})
	}
}
func TestOpsRefreshRespectsRPCSharedLockAndUnresolvedReceipts(t *testing.T) {
	for _, mode := range []string{"rpc", "shared", "uncertain", "prepared"} {
		t.Run(mode, func(t *testing.T) {
			f, started, _, _ := refreshFixture(t, "ok")
			f.login(t)
			if mode == "rpc" {
				f.app.opsReads <- struct{}{}
				defer func() { <-f.app.opsReads }()
			}
			if mode == "shared" {
				release, err := opsGenerationLock(f.app.opsRefresher.config.LockFile)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			if mode == "uncertain" || mode == "prepared" {
				f.app.opsActor = &opsReadProvider{}
				if err := f.app.openOpsActionState(filepath.Join(t.TempDir(), "state")); err != nil {
					t.Fatal(err)
				}
				stamp := f.clock.Add(time.Minute).Format(time.RFC3339Nano)
				record := opsActionRecord{Version: opsActionVersion, ID: strings.Repeat("b", 32), State: mode, Plan: opsActionResult{ExpiresAt: &stamp}}
				if err := f.app.saveAction(record); err != nil {
					t.Fatal(err)
				}
			}
			result := decodeRefresh(t, f.send(f.request("POST", "/api/ops-refresh", "{}")))
			if result.Status == "PASS" {
				t.Fatal("pending operation ignored")
			}
			if _, err := os.Stat(started); !os.IsNotExist(err) {
				t.Fatal("producer ran before gate")
			}
		})
	}
}
func TestOpsRefreshSessionRevocationStillWorksDuringGeneration(t *testing.T) {
	f, started, _, _ := refreshFixture(t, "wait")
	f.login(t)
	old := f.app.dir
	request := f.request("POST", "/api/ops-refresh", "{}")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.send(request) }()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("producer not started")
		}
		time.Sleep(time.Millisecond)
	}
	logout := f.request("POST", "/logout", "csrf="+f.csrf)
	logout.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if out := f.send(logout); out.Code != 303 {
		t.Fatal("revocation blocked", out.Code)
	}
	select {
	case out := <-done:
		if out.Code != 401 {
			t.Fatal("revocation not honored", out.Code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled generation retained lock")
	}
	if f.app.dir != old {
		t.Fatal("revoked session published generation")
	}
}

func TestOpsRefreshCannotBeEnabledByModelAttachment(t *testing.T) {
	f, o, path := opsFixture(t)
	o.Refresh = &opsRefreshState{Enabled: true, TimeoutSeconds: 25}
	writeOps(t, path, o)
	f.login(t)
	if out := f.send(f.request("POST", "/api/ops-overview", "{}")); out.Code != 422 {
		t.Fatal("attachment enabled producer", out.Code)
	}
	if out := f.send(f.request("POST", "/api/ops-refresh", "{}")); out.Code != 404 {
		t.Fatal("unbound refresh route", out.Code)
	}
}
