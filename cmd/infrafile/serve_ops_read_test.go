package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOpsReadHelper(t *testing.T) {
	marker := -1
	for i, arg := range os.Args {
		if arg == "--ops-read-helper" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	args := os.Args[marker+1:]
	mode, stamp, path := args[0], args[1], args[2]
	os.WriteFile(path, []byte("started"), 0600)
	if mode == "dynamic" {
		raw, _ := os.ReadFile(path + ".mode")
		mode = string(raw)
	}
	if mode == "wait" {
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	if mode == "flood" {
		fmt.Print(strings.Repeat("x", 100000))
		os.Exit(0)
	}
	if mode == "invalid" {
		fmt.Print(`{"status":"PASS","password":"unexpected"}`)
		os.Exit(0)
	}
	var req opsReadRequest
	if json.NewDecoder(os.Stdin).Decode(&req) != nil {
		os.Exit(2)
	}
	result := opsReadResult{Version: req.Version, RequestID: req.RequestID, ActionID: req.ActionID, Target: req.Target, Status: "PASS", ErrorCode: "ok", StartedAt: stamp, FinishedAt: stamp, ObservedAt: &stamp, DurationMS: 1, MaxAgeSeconds: 300, Sections: []opsReadSection{{ID: "sample", Title: "Fixed selected output", Status: "PASS", Fields: []opsReadField{{Label: "Scope", Value: "Read only"}}, Lines: []string{"selected observation"}}}}
	json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(0)
}

func readerFixture(t *testing.T, mode string) (*webFixture, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("local POSIX adapter fixture")
	}
	f, o, path := opsFixture(t)
	dir := t.TempDir()
	exe, _ := os.Executable()
	started := filepath.Join(dir, "started")
	wrapper := filepath.Join(dir, "reader")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nexec " + quote(exe) + " -test.run=^TestOpsReadHelper$ -- --ops-read-helper " + quote(mode) + " " + quote(f.clock.Format(time.RFC3339Nano)) + " " + quote(started) + "\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	wrapper, _ = filepath.EvalSymlinks(wrapper)
	hash := func(data []byte) string { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }
	model, _ := os.ReadFile(path)
	seconds := 3
	if mode == "wait" {
		seconds = 1
	}
	config := opsReadConfig{Version: "infrafile-ops-reader/v1", Executable: wrapper, OverviewSHA256: hash(model), Command: []string{wrapper}, Directory: dir, Files: map[string]string{wrapper: hash([]byte(script))}, Capabilities: []opsReadCapability{{ID: "selected-read", Label: "Collect selected evidence", Target: o.Servers[0].Key, RelatedKeys: []string{o.Applications[0].Key}, TimeoutSeconds: seconds, MaxAgeSeconds: 300, MinIntervalSeconds: 1}}}
	raw, _ := json.Marshal(config)
	configPath := filepath.Join(dir, "reader.json")
	os.WriteFile(configPath, raw, 0600)
	var err error
	f.app.opsReader, err = f.app.loadOpsReader(context.Background(), configPath)
	if err != nil {
		t.Fatal(err)
	}
	return f, started, wrapper
}

func TestOpsReaderAuthenticationArgumentsAndNoImplicitCollection(t *testing.T) {
	f, started, _ := readerFixture(t, "ok")
	body := fmt.Sprintf(`{"action_id":"selected-read","target":%q}`, f.app.opsReader.config.Capabilities[0].Target)
	if out := f.send(f.request("POST", "/api/ops-read", body)); out.Code != 401 {
		t.Fatal(out.Code)
	}
	f.login(t)
	o := readOps(t, f)
	if len(o.ReadCapabilities) != 1 || !o.ReadCapabilities[0].Enabled {
		t.Fatal("reader not available")
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatal("overview executed provider")
	}
	for _, bad := range []string{strings.TrimSuffix(body, "}") + `,"command":"untrusted"}`, `{"action_id":"selected-read","target":"Server/other"}`, strings.TrimSuffix(body, "}") + `,"target":"Server/other"}`} {
		out := f.send(f.request("POST", "/api/ops-read", bad))
		if out.Code != 400 && out.Code != 403 {
			t.Fatal(out.Code)
		}
	}
	noCSRF := f.request("POST", "/api/ops-read", body)
	noCSRF.Header.Del("X-CSRF-Token")
	if out := f.send(noCSRF); out.Code != 403 {
		t.Fatal(out.Code)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatal("rejected request executed provider")
	}
	out := f.send(f.request("POST", "/api/ops-read", body))
	if out.Code != 200 {
		t.Fatal(out.Code)
	}
	var result opsReadResult
	json.Unmarshal(out.Body.Bytes(), &result)
	if result.Status != "PASS" || len(result.Sections) != 1 {
		t.Fatal("valid fixed provider rejected", result.ErrorCode)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatal("explicit read not executed")
	}
}

func TestOpsReaderChangedSourceAndCodeRefuseBeforeSpawn(t *testing.T) {
	for _, kind := range []string{"source", "code", "config", "ledger"} {
		t.Run(kind, func(t *testing.T) {
			f, started, wrapper := readerFixture(t, "ok")
			path := wrapper
			switch kind {
			case "source":
				path = filepath.Join(f.app.opsAuthority, "servers.yaml")
			case "config":
				path = f.app.opsReader.path
			case "ledger":
				path = filepath.Join(f.app.dir, "infrafile.yaml")
			}
			file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			file.WriteString("\n")
			file.Close()
			result := f.app.collectOps(context.Background(), "selected-read", f.app.opsReader.config.Capabilities[0].Target)
			if result.Status != "UNKNOWN" || len(result.Sections) != 0 {
				t.Fatal("changed binding passed")
			}
			if _, err := os.Stat(started); !os.IsNotExist(err) {
				t.Fatal("changed binding executed provider")
			}
		})
	}
}

func TestOpsReaderTimeoutOutputAndFailureRemainExplicit(t *testing.T) {
	for _, mode := range []string{"wait", "flood", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			f, _, _ := readerFixture(t, mode)
			tick := time.Now()
			result := f.app.collectOps(context.Background(), "selected-read", f.app.opsReader.config.Capabilities[0].Target)
			expected := map[string]string{"wait": "timeout", "flood": "output_limit", "invalid": "invalid_output"}[mode]
			if result.ErrorCode != expected || len(result.Sections) != 0 || time.Since(tick) > 3*time.Second {
				t.Fatal(result.ErrorCode)
			}
		})
	}
}

func TestOpsReaderGlobalBusyAndCancellationReleaseSlot(t *testing.T) {
	f, started, _ := readerFixture(t, "wait")
	target := f.app.opsReader.config.Capabilities[0].Target
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan opsReadResult, 1)
	go func() { done <- f.app.collectOps(ctx, "selected-read", target) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("provider did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	busy := f.app.collectOps(context.Background(), "selected-read", target)
	if busy.ErrorCode != "busy" {
		t.Fatal(busy.ErrorCode)
	}
	cancel()
	select {
	case result := <-done:
		if result.ErrorCode != "cancelled" {
			t.Fatal(result.ErrorCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not end reader")
	}
	if len(f.app.opsReads) != 0 {
		t.Fatal("global slot leaked")
	}
}

func TestOpsReadResultCannotInjectSecretsIdentityOrExtraOutput(t *testing.T) {
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	req := opsReadRequest{Version: opsReadVersion, RequestID: "example", ActionID: "read", Target: "Server/example"}
	valid := opsReadResult{Version: req.Version, RequestID: req.RequestID, ActionID: req.ActionID, Target: req.Target, Status: "PASS", ErrorCode: "ok", StartedAt: stamp, FinishedAt: stamp, ObservedAt: &stamp, DurationMS: 1, MaxAgeSeconds: 300, Sections: []opsReadSection{{ID: "sample", Title: "Sample", Status: "PASS", Lines: []string{}}}}
	if validateOpsRead(valid, req, now) != nil {
		t.Fatal("baseline invalid")
	}
	for _, kind := range []string{"target", "ip", "lines", "old", "failure_with_evidence"} {
		t.Run(kind, func(t *testing.T) {
			raw, _ := json.Marshal(valid)
			var changed opsReadResult
			json.Unmarshal(raw, &changed)
			switch kind {
			case "target":
				changed.Target = "Server/other"
			case "ip":
				changed.Sections[0].Lines = []string{"192.0.2.1"}
			case "lines":
				changed.Sections[0].Lines = make([]string, 121)
			case "old":
				old := now.Add(-time.Hour).Format(time.RFC3339)
				changed.ObservedAt = &old
			case "failure_with_evidence":
				changed.Status = "UNKNOWN"
				changed.ErrorCode = "ssh_authentication_failed"
			}
			if validateOpsRead(changed, req, now) == nil {
				t.Fatal("unsafe result accepted")
			}
		})
	}
}

func TestOpsReadPausedSectionPreservesDeliberateStop(t *testing.T) {
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	request := opsReadRequest{Version: opsReadVersion, RequestID: "example_request_123", ActionID: "standby-read", Target: "Server/standby"}
	result := opsReadResult{Version: request.Version, RequestID: request.RequestID, ActionID: request.ActionID, Target: request.Target, Status: "PASS", ErrorCode: "ok", StartedAt: stamp, FinishedAt: stamp, ObservedAt: &stamp, MaxAgeSeconds: 300, Sections: []opsReadSection{{ID: "unit", Title: "Retained disabled unit", Status: "PAUSED"}}}
	if err := validateOpsRead(result, request, now); err != nil {
		t.Fatal(err)
	}
	result.Sections[0].Status = "UNRECOGNIZED"
	if err := validateOpsRead(result, request, now); err == nil {
		t.Fatal("unknown status accepted")
	}
}

func TestOpsReadCapabilityCatalogBoundDoesNotWidenActorCatalog(t *testing.T) {
	f, _, _ := readerFixture(t, "ok")
	config := f.app.opsReader.config
	configPath := f.app.opsReader.path
	original := config.Capabilities[0]
	config.Capabilities = nil
	for i := 0; i < 32; i++ {
		c := original
		c.ID = fmt.Sprintf("read-%02d", i)
		config.Capabilities = append(config.Capabilities, c)
	}
	save := func() {
		raw, _ := json.Marshal(config)
		if err := os.WriteFile(configPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	if _, err := f.app.loadOpsReader(context.Background(), configPath); err != nil {
		t.Fatal(err)
	}
	c := original
	c.ID = "over-limit"
	config.Capabilities = append(config.Capabilities, c)
	save()
	if _, err := f.app.loadOpsReader(context.Background(), configPath); err == nil {
		t.Fatal("unbounded catalog accepted")
	}
	config.Version = "infrafile-ops-action/v1"
	config.Capabilities = config.Capabilities[:9]
	save()
	if _, err := f.app.loadOpsProvider(context.Background(), configPath, "infrafile-ops-action/v1", 45); err == nil {
		t.Fatal("actor catalog widened with read-only catalog")
	}
}
