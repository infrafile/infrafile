package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recoveryFixture(t *testing.T, statusMode string) (*webFixture, string, opsActionRecord, string) {
	t.Helper()
	f, dir, wrapper := actorFixture(t, "lost")
	cap := f.app.opsActor.config.Capabilities[0]
	plan, err := f.app.actionPlan(context.Background(), cap.ID, cap.Target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.actionApply(context.Background(), plan.ID, plan.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	record := waitAction(t, f, plan.ID)
	if record.State != "uncertain" {
		t.Fatal(record.State)
	}
	pkg := filepath.Dir(f.app.opsFile)
	actor := filepath.Join(pkg, "actor.json")
	raw, _ := os.ReadFile(f.app.opsActor.path)
	if err = os.WriteFile(actor, raw, 0600); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(pkg, "bundle", "candidate")
	frozen := filepath.Join(pkg, "bundle", "source")
	if err = os.MkdirAll(candidate, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(frozen, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.CopyFS(candidate, os.DirFS(f.app.dir)); err != nil {
		t.Fatal(err)
	}
	if err = os.CopyFS(frozen, os.DirFS(f.app.opsAuthority)); err != nil {
		t.Fatal(err)
	}
	program, _ := os.ReadFile(wrapper)
	program = []byte(strings.Replace(string(program), "'lost'", "'"+statusMode+"'", 1))
	currentWrapper := filepath.Join(dir, "current-actor")
	os.WriteFile(currentWrapper, program, 0700)
	currentWrapper, _ = filepath.EvalSymlinks(currentWrapper)
	digest := func(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
	current := f.app.opsActor.config
	current.Executable = currentWrapper
	current.Command = []string{currentWrapper}
	current.Files = map[string]string{currentWrapper: digest(program)}
	currentRaw, _ := json.Marshal(current)
	currentPath := filepath.Join(dir, "current-actor.json")
	os.WriteFile(currentPath, currentRaw, 0600)
	f.app.opsActor, err = f.app.loadOpsProvider(context.Background(), currentPath, "infrafile-ops-actor/v1", 45)
	if err != nil {
		t.Fatal(err)
	}
	status := current
	status.Version = "infrafile-ops-reconciler/v1"
	status.Files = map[string]string{currentWrapper: digest(program), actor: record.Binding, f.app.opsFile: status.OverviewSHA256}
	for name, hash := range func() map[string]string {
		var o opsOverview
		b, _ := os.ReadFile(f.app.opsFile)
		json.Unmarshal(b, &o)
		return o.AuthorityFiles
	}() {
		status.Files[filepath.Join(frozen, name)] = hash
	}
	statusRaw, _ := json.Marshal(status)
	statusPath := filepath.Join(dir, "status.json")
	os.WriteFile(statusPath, statusRaw, 0600)
	registry := filepath.Join(dir, "recoveries.json")
	registryRaw, _ := json.Marshal(struct {
		Version  string               `json:"version"`
		Bindings []opsRecoveryBinding `json:"bindings"`
	}{"infrafile-ops-recoveries/v1", []opsRecoveryBinding{{Binding: record.Binding, Overview: f.app.opsFile, Actor: actor, Provider: statusPath}}})
	os.WriteFile(registry, registryRaw, 0600)
	// Retained code is now invalid; recovery must run only the reviewed status adapter.
	os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 88\n"), 0700)
	if err = f.app.loadOpsRecoveries(context.Background(), registry); err != nil {
		t.Fatal(err)
	}
	return f, dir, record, registry
}

func TestOpsRecoveryOriginalBindingSurvivesNewActorAndLiveSourceDrift(t *testing.T) {
	f, dir, record, _ := recoveryFixture(t, "ok")
	file := filepath.Join(f.app.opsAuthority, "servers.yaml")
	raw, _ := os.ReadFile(file)
	os.WriteFile(file, append(raw, '\n'), 0600)
	f.login(t)
	view := readOps(t, f)
	if view.Alignment != "changed" || len(view.Recoveries) != 1 || !view.Recoveries[0].Enabled {
		t.Fatal(view.Alignment, view.Recoveries)
	}
	if countAction(dir, "status") != 0 {
		t.Fatal("overview contacted original execution")
	}
	out := f.send(f.request("POST", "/api/ops-action/reconcile", fmt.Sprintf(`{"id":%q}`, record.ID)))
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	var done opsActionRecord
	json.Unmarshal(out.Body.Bytes(), &done)
	if done.State != "completed" || done.Binding != "" || done.Plan.Digest != record.Plan.Digest || countAction(dir, "apply") != 1 || countAction(dir, "status") != 1 {
		t.Fatal(done.State, "wrong origin or repeated submission")
	}
	// A duplicate apply after recovery only retrieves the terminal receipt.
	f.send(f.request("POST", "/api/ops-action/apply", fmt.Sprintf(`{"id":%q,"digest":%q,"reviewed":true}`, record.ID, record.Plan.Digest)))
	if countAction(dir, "apply") != 1 {
		t.Fatal("replayed action")
	}
}

func TestOpsRecoveryNeverUsesCurrentActorForUnknownOrChangedOrigin(t *testing.T) {
	for _, kind := range []string{"unknown", "registry", "source", "program"} {
		t.Run(kind, func(t *testing.T) {
			f, dir, record, registry := recoveryFixture(t, "ok")
			switch kind {
			case "unknown":
				record.Binding = strings.Repeat("f", 64)
				if err := f.app.saveAction(record); err != nil {
					t.Fatal(err)
				}
			case "registry":
				file, _ := os.OpenFile(registry, os.O_APPEND|os.O_WRONLY, 0)
				file.WriteString("\n")
				file.Close()
			case "source":
				os.WriteFile(filepath.Join(filepath.Dir(f.app.opsFile), "bundle/source/servers.yaml"), []byte("changed\n"), 0600)
			case "program":
				os.WriteFile(filepath.Join(dir, "current-actor"), []byte("#!/bin/sh\nexit 77\n"), 0700)
			}
			done, err := f.app.actionReconcile(context.Background(), record.ID)
			if err != nil || done.State != "uncertain" || countAction(dir, "status") != 0 || countAction(dir, "apply") != 1 {
				t.Fatal(done.State, err, "fell back to current actor")
			}
		})
	}
}

func TestOpsRecoveryRejectsAnotherPlanOrInvocation(t *testing.T) {
	for _, mode := range []string{"foreign-digest", "foreign-invocation"} {
		t.Run(mode, func(t *testing.T) {
			f, dir, record, _ := recoveryFixture(t, mode)
			if mode == "foreign-invocation" {
				record.Result.InvocationID = strings.Repeat("b", 32)
				f.app.saveAction(record)
			}
			done, err := f.app.actionReconcile(context.Background(), record.ID)
			if err != nil || done.State != "uncertain" || done.ErrorCode != "recovery_origin_mismatch" || countAction(dir, "apply") != 1 {
				t.Fatal(done, err)
			}
			if mode == "foreign-invocation" && done.Result.InvocationID != strings.Repeat("b", 32) {
				t.Fatal("lost original invocation")
			}
		})
	}
}

func TestOpsRecoveryProviderCannotPrepareOrApply(t *testing.T) {
	f, dir, record, _ := recoveryFixture(t, "ok")
	provider, overview := f.app.recoveryActor(record)
	for _, op := range []string{"plan", "apply"} {
		result := f.app.invokeBoundAction(context.Background(), provider, overview, opsActionRequest{Version: opsActionVersion, ID: record.ID, ActionID: record.ActionID, Target: record.Target, Operation: op})
		if result.ErrorCode != "actor_unavailable" || countAction(dir, "plan") != 1 || countAction(dir, "apply") != 1 {
			t.Fatal("recovery adapter executed a mutation")
		}
	}
}
