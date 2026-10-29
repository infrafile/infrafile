package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/plugin"
)

func historyFixture(t *testing.T) *ledger.Repo {
	t.Helper()
	r := issueRepo(t)
	p := filepath.Join(r.Dir, ledger.ConfigFile)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, []byte("storageVersion: v1\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	issueObservations(t, r, now, true)
	inspectBaseline(t, r)
	return r
}

func refreshSaved(t *testing.T, dir string, at time.Time) *HistoryRefresh {
	t.Helper()
	r, err := RefreshHistory(context.Background(), dir, InspectOptions{Now: at, MaxAgeDays: days(7)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func savedBytes(t *testing.T, dir string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "state", HistoryFile))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHistoryRefreshRealRulesIdempotencyUnknownRecoveryRecurrence(t *testing.T) {
	r := historyFixture(t)
	first := refreshSaved(t, r.Dir, now)
	if !first.Written || len(first.Changes) != 9 || len(first.History.Data.Issues) != 9 {
		t.Fatalf("first: %+v", first)
	}
	b := savedBytes(t, r.Dir)
	info, _ := os.Stat(filepath.Join(r.Dir, "state", HistoryFile))
	if info.Mode().Perm() != 0o600 {
		t.Fatal(info.Mode())
	}
	second := refreshSaved(t, r.Dir, now)
	if second.Written || len(second.Changes) != 0 || !bytes.Equal(b, savedBytes(t, r.Dir)) {
		t.Fatal("repeat changed persisted history")
	}
	for _, state := range second.History.Data.Issues {
		if state.Occurrences != 1 {
			t.Fatal(state)
		}
	}
	expired := refreshSaved(t, r.Dir, now.Add(9*24*time.Hour))
	for _, state := range expired.History.Data.Issues {
		if state.Lifecycle != "open" {
			t.Fatalf("expiry recovered: %+v", state)
		}
	}
	unknown := 0
	for _, event := range expired.Changes {
		if event.Type == "unknown" {
			unknown++
		}
	}
	if unknown < 8 {
		t.Fatalf("expiry did not become unknown: %+v", expired.Changes)
	}
	recoverAt := now.Add(10 * 24 * time.Hour)
	issueObservations(t, r, recoverAt, false)
	recovered := refreshSaved(t, r.Dir, recoverAt)
	firstSeen := map[string]string{}
	for _, state := range expired.History.Data.Issues {
		firstSeen[state.Identity.storageKey()] = state.FirstSeen
	}
	for _, state := range recovered.History.Data.Issues {
		if state.Lifecycle != "recovered" || state.FirstSeen != firstSeen[state.Identity.storageKey()] || state.RecoveredAt != recoverAt.Format(time.RFC3339) {
			t.Fatal(state)
		}
	}
	recurAt := recoverAt.Add(time.Hour)
	issueObservations(t, r, recurAt, true)
	recurred := refreshSaved(t, r.Dir, recurAt)
	initial := map[string]bool{}
	for _, state := range first.History.Data.Issues {
		initial[state.Identity.storageKey()] = true
	}
	for _, state := range recurred.History.Data.Issues {
		lifecycle, occurrences := "recovered", 1
		if initial[state.Identity.storageKey()] {
			lifecycle, occurrences = "open", 2
		}
		if state.Lifecycle != lifecycle || state.Occurrences != occurrences || state.FirstSeen != firstSeen[state.Identity.storageKey()] || state.RecoveredAt != recoverAt.Format(time.RFC3339) {
			t.Fatal(state)
		}
	}
	for index, event := range recurred.History.Data.Changes {
		if event.Sequence != uint64(index+1) {
			t.Fatal("event sequence is not continuous")
		}
	}
	events, err := recurred.History.HistoryChanges(recovered.History.Data.LastSequence)
	if err != nil || len(events) != 9 {
		t.Fatalf("cursor: %v %+v", err, events)
	}
}

func TestHistoryInputConflictAndParallelWriters(t *testing.T) {
	for _, what := range []string{"inventory-edit", "new-file", "state-edit", "history-edit", "state-replace", "lock-replace", "clock-backwards", "disk-failure", "cancel"} {
		t.Run(what, func(t *testing.T) {
			r := historyFixture(t)
			refreshSaved(t, r.Dir, now)
			old := savedBytes(t, r.Dir)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := InspectOptions{Now: now.Add(time.Hour), MaxAgeDays: days(7)}
			if what == "clock-backwards" {
				opts.Now = now.Add(-time.Hour)
			}
			_, err := refreshHistory(ctx, r.Dir, opts, func() error {
				switch what {
				case "inventory-edit":
					return os.WriteFile(filepath.Join(r.Dir, "inventory/servers/web.yaml"), []byte("changed"), 0o600)
				case "new-file":
					return os.WriteFile(filepath.Join(r.Dir, "inventory/servers/new.yaml"), []byte("changed"), 0o600)
				case "state-edit":
					return os.WriteFile(filepath.Join(r.Dir, "state/aliyun/home.yaml"), []byte("changed"), 0o600)
				case "history-edit":
					return os.WriteFile(filepath.Join(r.Dir, "state", HistoryFile), []byte("external edit"), 0o600)
				case "lock-replace":
					if err := os.Rename(filepath.Join(r.Dir, "state/.sync.lock"), filepath.Join(r.Dir, "state/.old-lock")); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join(r.Dir, "state/.sync.lock"), []byte("other writer"), 0o600)
				case "state-replace":
					if err := os.Rename(filepath.Join(r.Dir, "state"), filepath.Join(r.Dir, "old-state")); err != nil {
						return err
					}
					return os.Mkdir(filepath.Join(r.Dir, "state"), 0o700)
				case "disk-failure":
					return syscall.ENOSPC
				case "cancel":
					cancel()
				}
				return nil
			})
			if err == nil {
				t.Fatal("conflict or failure accepted")
			}
			if what == "history-edit" {
				if string(savedBytes(t, r.Dir)) != "external edit" {
					t.Fatal("external edit overwritten")
				}
				return
			}
			if what == "state-replace" {
				b, e := os.ReadFile(filepath.Join(r.Dir, "old-state", HistoryFile))
				if e != nil || !bytes.Equal(old, b) {
					t.Fatal("original state lost")
				}
				return
			}
			if !bytes.Equal(old, savedBytes(t, r.Dir)) {
				t.Fatal("old history overwritten")
			}
			if what == "lock-replace" {
				if data, _ := os.ReadFile(filepath.Join(r.Dir, "state/.sync.lock")); string(data) != "other writer" {
					t.Fatal("another writer's lock removed")
				}
			}
		})
	}
	r := historyFixture(t)
	refreshSaved(t, r.Dir, now)
	ready, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := refreshHistory(context.Background(), r.Dir, InspectOptions{Now: now.Add(time.Hour), MaxAgeDays: days(7)}, func() error { close(ready); <-release; return nil })
		finished <- err
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("first writer did not reach publication")
	}
	_, secondErr := RefreshHistory(context.Background(), r.Dir, InspectOptions{Now: now.Add(2 * time.Hour), MaxAgeDays: days(7)})
	_, readErr := ReadHistory(context.Background(), r.Dir)
	close(release)
	if firstErr := <-finished; firstErr != nil || secondErr == nil || readErr == nil {
		t.Fatalf("concurrent writers/reader: %v %v %v", firstErr, secondErr, readErr)
	}
	got, err := ReadHistory(context.Background(), r.Dir)
	if err != nil || got.Data.CheckedAt != now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatal("second writer replaced first")
	}
}

func TestHistoryRejectsCorruptVersionShapeAndSymlinks(t *testing.T) {
	r := historyFixture(t)
	refreshSaved(t, r.Dir, now)
	valid := savedBytes(t, r.Dir)
	mutations := map[string]func(map[string]any){
		"version":         func(v map[string]any) { v["version"] = "v2" },
		"payload-version": func(v map[string]any) { v["data"].(map[string]any)["identity_version"] = "v2" },
		"unknown-payload": func(v map[string]any) { v["data"].(map[string]any)["future"] = true },
		"missing-payload": func(v map[string]any) { delete(v["data"].(map[string]any), "issues") },
		"null-issues":     func(v map[string]any) { v["data"].(map[string]any)["issues"] = nil },
		"input-digest":    func(v map[string]any) { v["data"].(map[string]any)["input_hash"] = strings.Repeat("a", 64) },
		"wrong-case":      func(v map[string]any) { v["Version"] = v["version"]; delete(v, "version") },
		"event-gap": func(v map[string]any) {
			v["data"].(map[string]any)["changes"].([]any)[0].(map[string]any)["sequence"] = 2
		},
		"event-type": func(v map[string]any) {
			v["data"].(map[string]any)["changes"].([]any)[0].(map[string]any)["type"] = "deleted"
		},
		"identity": func(v map[string]any) {
			v["data"].(map[string]any)["issues"].([]any)[0].(map[string]any)["identity"].(map[string]any)["fingerprint"] = strings.Repeat("b", 64)
		},
		"unconfirmed-confirmed": func(v map[string]any) {
			s := v["data"].(map[string]any)["issues"].([]any)[0].(map[string]any)
			s["occurrences"] = 0
			delete(s, "last_confirmed")
		},
		"incomplete-evaluation": func(v map[string]any) {
			d := v["data"].(map[string]any)
			d["checked_at"] = now.Add(time.Hour).Format(time.RFC3339)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			json.Unmarshal(valid, &value)
			mutate(value)
			data, _ := json.Marshal(value)
			if _, err := decodeHistory(context.Background(), data); err == nil {
				t.Fatal("corruption accepted")
			}
		})
	}
	for name, data := range map[string][]byte{"duplicate": []byte(`{"version":"v1","version":"v1","kind":"FindingHistory","data":{}}`), "multiple": append(append([]byte{}, valid...), valid...), "too-large": bytes.Repeat([]byte(" "), maxHistoryBytes+1), "deep": []byte(strings.Repeat("[", 101) + "null" + strings.Repeat("]", 101))} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeHistory(context.Background(), data); err == nil {
				t.Fatal("malformed accepted")
			}
		})
	}
	p := filepath.Join(r.Dir, "state", HistoryFile)
	os.WriteFile(p, []byte("damaged"), 0o600)
	if _, err := RefreshHistory(context.Background(), r.Dir, InspectOptions{Now: now, MaxAgeDays: days(7)}); err == nil || string(savedBytes(t, r.Dir)) != "damaged" {
		t.Fatal("damaged history was replaced")
	}
	os.Remove(p)
	outside := filepath.Join(t.TempDir(), "history.json")
	os.WriteFile(outside, valid, 0o600)
	if err := os.Symlink(outside, p); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHistory(context.Background(), r.Dir); err == nil {
		t.Fatal("history symlink accepted")
	}
	if _, err := RefreshHistory(context.Background(), r.Dir, InspectOptions{Now: now, MaxAgeDays: days(7)}); err == nil {
		t.Fatal("history symlink overwritten")
	}
	if b, _ := os.ReadFile(outside); !bytes.Equal(b, valid) {
		t.Fatal("outside file changed")
	}
}

func TestHistoryMissingMarkerAndReadOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if h, err := ReadHistory(ctx, dir); h != nil || err != nil {
		t.Fatal(h, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created state")
	}
	r := issueRepo(t)
	if _, err := RefreshHistory(ctx, r.Dir, InspectOptions{Now: now, MaxAgeDays: days(7)}); err == nil {
		t.Fatal("missing storage marker accepted")
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing marker created state")
	}
	r = historyFixture(t)
	refreshSaved(t, r.Dir, now)
	before := savedBytes(t, r.Dir)
	for n := 0; n < 2; n++ {
		h, err := ReadHistory(ctx, r.Dir)
		if err != nil || len(h.Data.Issues) != 9 {
			t.Fatal(err)
		}
		b, err := ExportHistory(ctx, r.Dir)
		if err != nil || !bytes.Equal(b, before) {
			t.Fatal("export not exact")
		}
	}
	if !bytes.Equal(before, savedBytes(t, r.Dir)) {
		t.Fatal("read advanced history")
	}
}

func TestHistoryFixedInputBudgetAndScopeChange(t *testing.T) {
	r := historyFixture(t)
	first := refreshSaved(t, r.Dir, now)
	old := savedBytes(t, r.Dir)
	f, err := os.Create(filepath.Join(r.Dir, "inventory/oversize"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxHistoryInputBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := RefreshHistory(context.Background(), r.Dir, InspectOptions{Now: now.Add(time.Hour), MaxAgeDays: days(7)}); err == nil || !bytes.Equal(old, savedBytes(t, r.Dir)) {
		t.Fatal("oversize input overwrote history")
	}
	os.Remove(filepath.Join(r.Dir, "inventory/oversize"))
	result, err := RefreshHistory(context.Background(), r.Dir, InspectOptions{Now: now, MaxAgeDays: days(6)})
	if err != nil {
		t.Fatal(err)
	}
	prior := map[string]bool{}
	for _, state := range first.History.Data.Issues {
		prior[state.Identity.storageKey()] = true
	}
	for _, state := range result.History.Data.Issues {
		if prior[state.Identity.storageKey()] && (state.Lifecycle != "out_of_scope" || state.RecoveredAt != "") {
			t.Fatal("TTL scope change falsely recovered", state)
		}
	}
	for _, event := range result.Changes {
		if event.Type == "recovered" {
			t.Fatal("scope shrink emitted recovery")
		}
	}
}

func TestHistoryAtomicPublishIOFailures(t *testing.T) {
	r := historyFixture(t)
	refreshSaved(t, r.Dir, now)
	old := savedBytes(t, r.Dir)
	root, err := os.OpenRoot(filepath.Join(r.Dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := publishHistory(context.Background(), root, []byte("new bytes"), func() error { return root.Close() }); err == nil {
		t.Fatal("rename on closed root accepted")
	}
	if !bytes.Equal(old, savedBytes(t, r.Dir)) {
		t.Fatal("rename failure replaced old history")
	}
	if err := publishHistory(context.Background(), root, []byte("new bytes"), func() error { return nil }); err == nil {
		t.Fatal("create on closed root accepted")
	}
	if !bytes.Equal(old, savedBytes(t, r.Dir)) {
		t.Fatal("create failure replaced old history")
	}
}

func TestHistoryExportPruneCapacityAndExpiredCursor(t *testing.T) {
	r := historyFixture(t)
	first := refreshSaved(t, r.Dir, now)
	// Exercise the boundary with distinct valid identities, not duplicated references.
	h := *first.History
	h.Data.Issues = []IssueState{}
	h.Data.Changes = []HistoryEvent{}
	for n := 0; n < MaxHistoryIssues; n++ {
		state := first.History.Data.Issues[0]
		state.Identity.ScopeHash = plugin.Hash(n)
		h.Data.Issues = append(h.Data.Issues, state)
	}
	for n := 0; n < MaxHistoryChanges; n++ {
		h.Data.Changes = append(h.Data.Changes, HistoryEvent{uint64(n + 1), IssueChange{h.Data.Issues[n%MaxHistoryIssues].Identity, "opened", h.Data.CheckedAt}})
	}
	sort.Slice(h.Data.Issues, func(i, j int) bool {
		return h.Data.Issues[i].Identity.storageKey() < h.Data.Issues[j].Identity.storageKey()
	})
	h.Data.LastSequence = MaxHistoryChanges
	data, err := h.Encode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("capacity: %d issues, %d events, %d bytes", len(h.Data.Issues), len(h.Data.Changes), len(data))
	if err := os.WriteFile(filepath.Join(r.Dir, "state", HistoryFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshHistory(context.Background(), r.Dir, InspectOptions{Now: now.Add(time.Hour), MaxAgeDays: days(7)}); err == nil || !bytes.Equal(savedBytes(t, r.Dir), data) {
		t.Fatal("capacity exhaustion discarded active issues")
	}
	original, err := first.History.Encode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(r.Dir, "state", HistoryFile), original, 0o600)
	h.Data.Issues = append(h.Data.Issues, h.Data.Issues[0])
	if _, err := h.Encode(context.Background()); err == nil {
		t.Fatal("issue capacity exceeded")
	}
	h.Data.Issues = h.Data.Issues[:MaxHistoryIssues]
	h.Data.Changes = append(h.Data.Changes, HistoryEvent{MaxHistoryChanges + 1, IssueChange{h.Data.Issues[0].Identity, "unknown", h.Data.CheckedAt}})
	h.Data.LastSequence++
	if _, err := h.Encode(context.Background()); err == nil {
		t.Fatal("change capacity exceeded")
	}
	// Actual prune requires a current independent export and preserves open/out-of-scope states.
	at := now.Add(time.Hour)
	issueObservations(t, r, at, false)
	recovered := refreshSaved(t, r.Dir, at)
	recovered.History.Data.Issues[0].Lifecycle = "out_of_scope"
	recovered.History.Data.Issues[0].Resume = "open"
	recovered.History.Data.Issues[0].Assessment = "unknown"
	b, err := recovered.History.Encode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(r.Dir, "state", HistoryFile)
	os.WriteFile(p, b, 0o600)
	exported := filepath.Join(t.TempDir(), "export.json")
	os.WriteFile(exported, []byte("old export"), 0o600)
	if _, err := PruneHistory(context.Background(), r.Dir, exported, at.Add(time.Minute), at.AddDate(0, 0, 181)); err == nil {
		t.Fatal("stale export accepted")
	}
	if _, err := PruneHistory(context.Background(), r.Dir, p, at.Add(time.Minute), at.AddDate(0, 0, 181)); err == nil {
		t.Fatal("current history accepted as independent export")
	}
	os.WriteFile(exported, b, 0o600)
	pruned, err := PruneHistory(context.Background(), r.Dir, exported, at.Add(time.Minute), at.AddDate(0, 0, 181))
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned.History.Data.Issues) != 1 || pruned.History.Data.Issues[0].Lifecycle != "out_of_scope" || len(pruned.History.Data.Changes) != 0 || pruned.History.Data.PrunedThrough != recovered.History.Data.LastSequence {
		t.Fatal("unsafe prune")
	}
	if _, err := pruned.History.HistoryChanges(0); err == nil {
		t.Fatal("expired cursor silently skipped changes")
	}
	if events, err := pruned.History.HistoryChanges(pruned.History.Data.PrunedThrough); err != nil || len(events) != 0 {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(exported); !bytes.Equal(after, b) {
		t.Fatal("export changed")
	}
}

func TestHistoryCrashBeforePublication(t *testing.T) {
	if dir := os.Getenv("INFRAFILE_HISTORY_CRASH_DIR"); dir != "" {
		_, err := refreshHistory(context.Background(), dir, InspectOptions{Now: now.Add(time.Hour), MaxAgeDays: days(7)}, func() error { p, _ := os.FindProcess(os.Getpid()); return p.Kill() })
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	r := historyFixture(t)
	refreshSaved(t, r.Dir, now)
	old := savedBytes(t, r.Dir)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHistoryCrashBeforePublication$")
	cmd.Env = append(os.Environ(), "INFRAFILE_HISTORY_CRASH_DIR="+r.Dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("crash process unexpectedly succeeded")
	}
	if !bytes.Equal(old, savedBytes(t, r.Dir)) {
		t.Fatal("interrupted write lost old history")
	}
	if _, err := ReadHistory(context.Background(), r.Dir); err == nil {
		t.Fatal("orphaned lock was silently cleared")
	}
	// Operator confirms the process exited; only then is its lock removed.
	if err := os.Remove(filepath.Join(r.Dir, "state/.sync.lock")); err != nil {
		t.Fatal(err)
	}
	h, err := ReadHistory(context.Background(), r.Dir)
	if err != nil || len(h.Data.Issues) != 9 {
		t.Fatal("old active issues could not be recovered", err)
	}
	if !reflect.DeepEqual(refreshSaved(t, r.Dir, now).History, h) {
		t.Fatal("restart changed identical evaluation")
	}
}
