package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/snapshot"
)

func TestSyncStatusFailureAndRecovery(t *testing.T) {
	fixturePlugin(t)
	dir := syncRepo(t, twoPlugins)
	start := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	args := []string{"sync", "--dir", dir, "--json"}
	for i, mode := range []string{"good", "error", "good"} {
		t.Setenv("INFRAFILE_PLUGIN_MODE", mode)
		var out, stderr bytes.Buffer
		want := 0
		if mode == "error" {
			want = 1
		}
		if code := runAt(args, &out, &stderr, start.Add(time.Duration(i)*time.Hour)); code != want {
			t.Fatalf("%s exit %d: %s %s", mode, code, &out, &stderr)
		}
		status, err := snapshot.ReadStatus(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		attempt := status.Spec["aaa"]["home"]
		if i == 1 {
			if attempt.Status != "failed" || attempt.Code != "plugin" {
				t.Fatalf("failure receipt: %+v", attempt)
			}
			stamps, err := snapshot.ReadSync(dir)
			if err != nil || !stamps["aaa"]["home"].Equal(start) {
				t.Fatalf("failure advanced success time: %+v %v", stamps, err)
			}
		} else if attempt.Status != "succeeded" || attempt.Code != "" || attempt.SnapshotHash == "" {
			t.Fatalf("success receipt: %+v", attempt)
		}
		out.Reset()
		if code := runAt([]string{"audit", dir, "--rule", "stale_state", "--max-age", "7d", "--json"}, &out, &stderr, start.Add(2*time.Hour)); code != 0 {
			t.Fatalf("audit exit %d: %s %s", code, &out, &stderr)
		}
		var audit map[string]any
		if err := json.Unmarshal(out.Bytes(), &audit); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			notices := audit["notices"].([]any)
			if len(notices) != 1 || notices[0].(map[string]any)["code"] != "sync_failed" || audit["ok"] != true {
				t.Fatalf("execution must be visible without changing findings exit code: %s", &out)
			}
			s := mcpSession(t, dir)
			got := callMCP(t, s, "audit", map[string]any{"rules": []string{"stale_state"}, "max_age_days": 7}, false)
			if !reflect.DeepEqual(got, audit) {
				t.Fatalf("MCP/CLI differ: %v\n%s", got, &out)
			}
		} else if len(audit["notices"].([]any)) != 0 {
			t.Fatalf("old failure retained after recovery: %s", &out)
		}
	}
}

func TestSyncFirstFailureHasOfflineEvidence(t *testing.T) {
	dir := syncRepo(t, "  missing-fixture-plugin:\n    instances:\n      home: {}\n")
	var out, stderr bytes.Buffer
	if code := run([]string{"sync", "--dir", dir, "--json"}, &out, &stderr); code != 1 {
		t.Fatalf("exit %d: %s %s", code, &out, &stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "state/missing-fixture-plugin/home.yaml")); !os.IsNotExist(err) {
		t.Fatal("failed plugin produced a snapshot")
	}
	s := mcpSession(t, dir)
	got := callMCP(t, s, "audit", map[string]any{}, false)
	notices := got["notices"].([]any)
	if len(notices) != 1 || notices[0].(map[string]any)["code"] != "sync_failed" || len(got["snapshots"].([]any)) != 0 {
		t.Fatalf("first failure disappeared from offline audit: %v", got)
	}
}

func TestSyncCancellationPersistsCurrentAttemptOnly(t *testing.T) {
	fixturePlugin(t)
	dir := syncRepo(t, twoPlugins)
	t.Setenv("INFRAFILE_PLUGIN_MODE", "hang")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := newRoot(time.Now())
	cmd.SetArgs([]string{"sync", "--dir", dir, "--timeout", "10s", "--json"})
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	deadline := time.After(5 * time.Second)
	waiting := true
	for waiting {
		select {
		case err := <-done:
			t.Fatalf("sync ended before cancellation: %v", err)
		case <-deadline:
			t.Fatal("running receipt not written before plugin call")
		case <-time.After(5 * time.Millisecond):
			data, err := os.ReadFile(filepath.Join(dir, "state", snapshot.StatusFile))
			if err == nil {
				status, err := snapshot.ParseStatus(context.Background(), data)
				waiting = err != nil || status.Spec["aaa"]["home"].Status != "running"
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled sync succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled plugin not reaped")
	}
	status, err := snapshot.ReadStatus(context.Background(), dir)
	if err != nil || status.Spec["aaa"]["home"].Code != "timeout" || len(status.Spec) != 1 {
		t.Fatalf("cancel status or skipped instances incorrect: %+v %v", status, err)
	}
	var result struct {
		Cancelled bool `json:"cancelled"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || !result.Cancelled {
		t.Fatalf("cancellation result: %s %s", &out, &stderr)
	}
}
