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

func TestOpsActionHelper(t *testing.T) {
	marker := -1
	for i, arg := range os.Args {
		if arg == "--ops-action-helper" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	args := os.Args[marker+1:]
	mode, stamp, folder, stateDir := args[0], args[1], args[2], args[3]
	var request opsActionRequest
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		os.Exit(2)
	}
	if request.Operation == "apply" {
		raw, err := os.ReadFile(filepath.Join(stateDir, request.ID+".json"))
		var record opsActionRecord
		if err != nil || json.Unmarshal(raw, &record) != nil || record.State != "submitting" {
			os.Exit(3)
		}
	}
	countFile := filepath.Join(folder, "count-"+request.Operation)
	count, _ := os.ReadFile(countFile)
	os.WriteFile(countFile, append(count, 'x'), 0600)
	expires, _ := time.Parse(time.RFC3339Nano, stamp)
	expiration := expires.Add(30 * time.Second).Format(time.RFC3339Nano)
	result := opsActionResult{Version: opsActionVersion, ID: request.ID, ActionID: request.ActionID, Target: request.Target, Operation: request.Operation, State: "prepared", ErrorCode: "ok", Digest: strings.Repeat("a", 64), ExpiresAt: &expiration, Disposition: "submit_once", Fields: []opsReadField{{Label: "Fixed unit", Value: "example-observer.service"}}}
	if mode == "identity-kind-change" {
		result.IdentityKind = "docker_transaction"
	}
	if request.Operation != "plan" {
		if mode == "slow" && request.Operation == "apply" {
			time.Sleep(350 * time.Millisecond)
		}
		result.State = "completed"
		result.InvocationID = strings.Repeat("b", 32)
		result.Disposition = "submitted"
		if mode == "identity-kind-change" {
			result.IdentityKind = "systemd_invocation"
		}
		if mode == "foreign-apply-digest" && request.Operation == "apply" {
			result.Digest = strings.Repeat("d", 64)
		}
		if mode == "lost" && request.Operation == "apply" {
			result.State = "uncertain"
			result.ErrorCode = "transport_failed"
			result.InvocationID = ""
		}
		if mode == "unsubmitted" && request.Operation == "status" {
			result.State = "prepared"
			result.InvocationID = ""
			result.Disposition = "submit_once"
		}
		if mode == "foreign-digest" && request.Operation == "status" {
			result.Digest = strings.Repeat("d", 64)
		}
		if mode == "foreign-invocation" && request.Operation == "status" {
			result.InvocationID = strings.Repeat("e", 32)
		}
	}
	json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(0)
}

func actorFixture(t *testing.T, mode string) (*webFixture, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixed adapter fixture")
	}
	f, o, ops := opsFixture(t)
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := f.app.openOpsActionState(state); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	wrapper := filepath.Join(dir, "actor")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nexec " + quote(exe) + " -test.run=^TestOpsActionHelper$ -- --ops-action-helper " + quote(mode) + " " + quote(f.clock.Format(time.RFC3339Nano)) + " " + quote(dir) + " " + quote(state) + "\n"
	os.WriteFile(wrapper, []byte(script), 0700)
	wrapper, _ = filepath.EvalSymlinks(wrapper)
	hash := func(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
	data, _ := os.ReadFile(ops)
	config := opsReadConfig{Version: "infrafile-ops-actor/v1", Executable: wrapper, OverviewSHA256: hash(data), Command: []string{wrapper}, Directory: dir, Files: map[string]string{wrapper: hash([]byte(script))}, Capabilities: []opsReadCapability{{ID: "selected-once", Label: "Execute existing observation once", Target: o.Servers[0].Key, RelatedKeys: []string{o.Applications[0].Key}, TimeoutSeconds: 45, MaxAgeSeconds: 300, MinIntervalSeconds: 60}}}
	raw, _ := json.Marshal(config)
	path := filepath.Join(dir, "actor.json")
	os.WriteFile(path, raw, 0600)
	var err error
	f.app.opsActor, err = f.app.loadOpsProvider(context.Background(), path, "infrafile-ops-actor/v1", 45)
	if err != nil {
		t.Fatal(err)
	}
	return f, dir, wrapper
}
func waitAction(t *testing.T, f *webFixture, id string) opsActionRecord {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		record, err := f.app.readAction(id)
		// Rename makes the terminal receipt visible just before directory fsync
		// and deferred slot release finish. Wait for the whole dispatch to settle.
		if err == nil && record.State != "submitting" && record.State != "running" && len(f.app.opsReads) == 0 {
			return record
		}
		if time.Now().After(deadline) {
			t.Fatal("action did not settle")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func countAction(dir, operation string) int {
	raw, _ := os.ReadFile(filepath.Join(dir, "count-"+operation))
	return len(raw)
}

func TestOpsActionAuthenticationExplicitReviewAndNoImplicitExecution(t *testing.T) {
	f, dir, _ := actorFixture(t, "ok")
	target := f.app.opsActor.config.Capabilities[0].Target
	body := fmt.Sprintf(`{"action_id":"selected-once","target":%q}`, target)
	if out := f.send(f.request("POST", "/api/ops-action/plan", body)); out.Code != 401 {
		t.Fatal(out.Code)
	}
	f.login(t)
	o := readOps(t, f)
	if len(o.ActionCapabilities) != 1 || !o.ActionCapabilities[0].Enabled {
		t.Fatal("action disabled")
	}
	if countAction(dir, "plan") != 0 || countAction(dir, "apply") != 0 {
		t.Fatal("overview invoked action")
	}
	bad := f.request("POST", "/api/ops-action/plan", body)
	bad.Header.Del("X-CSRF-Token")
	if out := f.send(bad); out.Code != 403 {
		t.Fatal(out.Code)
	}
	for _, value := range []string{strings.TrimSuffix(body, "}") + `,"command":"untrusted"}`, `{"action_id":"selected-once","target":"Server/other"}`} {
		out := f.send(f.request("POST", "/api/ops-action/plan", value))
		if out.Code != 400 && out.Code != 403 {
			t.Fatal(out.Code)
		}
	}
	planOut := f.send(f.request("POST", "/api/ops-action/plan", body))
	if planOut.Code != 200 {
		t.Fatal(planOut.Code)
	}
	var plan opsActionRecord
	json.Unmarshal(planOut.Body.Bytes(), &plan)
	if plan.State != "prepared" || countAction(dir, "apply") != 0 {
		t.Fatal(plan.State)
	}
	notReviewed := fmt.Sprintf(`{"id":%q,"digest":%q,"reviewed":false}`, plan.ID, plan.Plan.Digest)
	if out := f.send(f.request("POST", "/api/ops-action/apply", notReviewed)); out.Code != 400 {
		t.Fatal(out.Code)
	}
	apply := fmt.Sprintf(`{"id":%q,"digest":%q,"reviewed":true}`, plan.ID, plan.Plan.Digest)
	for range 2 {
		if out := f.send(f.request("POST", "/api/ops-action/apply", apply)); out.Code != 200 {
			t.Fatal(out.Code)
		}
	}
	done := waitAction(t, f, plan.ID)
	if done.State != "completed" || countAction(dir, "apply") != 1 {
		t.Fatal(done.State, "duplicate apply")
	}
}

func TestOpsActionExpiredDriftAndClaimFailClosed(t *testing.T) {
	for _, kind := range []string{"expired", "source", "code", "claim", "wrong_digest"} {
		t.Run(kind, func(t *testing.T) {
			f, dir, wrapper := actorFixture(t, "ok")
			cap := f.app.opsActor.config.Capabilities[0]
			plan, err := f.app.actionPlan(context.Background(), cap.ID, cap.Target)
			if err != nil {
				t.Fatal(err)
			}
			expected := plan.Plan.Digest
			switch kind {
			case "expired":
				f.clock = f.clock.Add(31 * time.Second)
			case "source":
				file, _ := os.OpenFile(filepath.Join(f.app.opsAuthority, "servers.yaml"), os.O_APPEND|os.O_WRONLY, 0)
				file.WriteString("\n")
				file.Close()
			case "code":
				file, _ := os.OpenFile(wrapper, os.O_APPEND|os.O_WRONLY, 0)
				file.WriteString("\n")
				file.Close()
			case "claim":
				os.WriteFile(filepath.Join(f.app.opsActionDir, plan.ID+".dispatch"), []byte("preexisting"), 0600)
			case "wrong_digest":
				expected = strings.Repeat("0", 64)
			}
			result, err := f.app.actionApply(context.Background(), plan.ID, expected)
			if err == nil && result.State != "uncertain" {
				t.Fatal("guard passed")
			}
			if countAction(dir, "apply") != 0 {
				t.Fatal("guard executed")
			}
		})
	}
}

func TestOpsActionSurvivesRequestCancellationAndPersistsOneReceipt(t *testing.T) {
	f, dir, _ := actorFixture(t, "slow")
	cap := f.app.opsActor.config.Capabilities[0]
	plan, err := f.app.actionPlan(context.Background(), cap.ID, cap.Target)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = f.app.actionApply(ctx, plan.ID, plan.Plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = f.app.actionPlan(context.Background(), cap.ID, cap.Target); err == nil {
		t.Fatal("new plan ignored active action")
	}
	result := waitAction(t, f, plan.ID)
	if result.State != "completed" || countAction(dir, "apply") != 1 {
		t.Fatal(result.State)
	}
	if len(f.app.opsReads) != 0 {
		t.Fatal("slot retained")
	}
	// A fresh process can read the same durable completed receipt.
	other := &workbench{dir: f.app.dir, opsAuthority: f.app.opsAuthority}
	if err = other.openOpsActionState(f.app.opsActionDir); err != nil {
		t.Fatal(err)
	}
	saved, err := other.readAction(plan.ID)
	if err != nil || saved.State != "completed" {
		t.Fatal("receipt lost")
	}
}

func TestOpsActionUncertaintyRequiresReadOnlyReconciliation(t *testing.T) {
	f, dir, _ := actorFixture(t, "lost")
	cap := f.app.opsActor.config.Capabilities[0]
	plan, err := f.app.actionPlan(context.Background(), cap.ID, cap.Target)
	if err != nil {
		t.Fatal(err)
	}
	f.app.actionApply(context.Background(), plan.ID, plan.Plan.Digest)
	unknown := waitAction(t, f, plan.ID)
	if unknown.State != "uncertain" {
		t.Fatal(unknown.State)
	}
	f.app.actionApply(context.Background(), plan.ID, plan.Plan.Digest)
	if countAction(dir, "apply") != 1 {
		t.Fatal("uncertain was resubmitted")
	}
	reconciled, err := f.app.actionReconcile(context.Background(), plan.ID)
	if err != nil || reconciled.State != "completed" || countAction(dir, "apply") != 1 || countAction(dir, "status") != 1 {
		t.Fatal("reconcile executed again", err)
	}
}

func TestOpsActionStateReplacementAndResultIdentityRefused(t *testing.T) {
	f, dir, _ := actorFixture(t, "ok")
	cap := f.app.opsActor.config.Capabilities[0]
	plan, err := f.app.actionPlan(context.Background(), cap.ID, cap.Target)
	if err != nil {
		t.Fatal(err)
	}
	os.Rename(f.app.opsActionDir, f.app.opsActionDir+".old")
	os.Mkdir(f.app.opsActionDir, 0700)
	if _, err = f.app.actionApply(context.Background(), plan.ID, plan.Plan.Digest); err == nil {
		t.Fatal("replaced state accepted")
	}
	if countAction(dir, "apply") != 0 {
		t.Fatal("state failure executed")
	}
	req := opsActionRequest{Version: opsActionVersion, ID: strings.Repeat("c", 32), ActionID: cap.ID, Target: cap.Target, Operation: "apply"}
	result := opsActionResult{Version: opsActionVersion, ID: req.ID, ActionID: req.ActionID, Target: req.Target, Operation: req.Operation, State: "completed", ErrorCode: "ok", Digest: strings.Repeat("a", 64), InvocationID: strings.Repeat("b", 32), Disposition: "submitted", Fields: []opsReadField{}}
	if validateActionResult(result, req, time.Now()) != nil {
		t.Fatal("baseline result invalid")
	}
	result.Target = "Server/other"
	if validateActionResult(result, req, time.Now()) == nil {
		t.Fatal("forged target accepted")
	}
}

func TestOpsActionDockerTransactionIdentity(t *testing.T) {
	req := opsActionRequest{Version: opsActionVersion, ID: strings.Repeat("c", 32), ActionID: "fixture-start", Target: "Server/local-container-fixture", Operation: "apply"}
	result := opsActionResult{Version: opsActionVersion, ID: req.ID, ActionID: req.ActionID, Target: req.Target, Operation: req.Operation, State: "completed", ErrorCode: "ok", Digest: strings.Repeat("a", 64), InvocationID: req.ID, IdentityKind: "docker_transaction", Fields: []opsReadField{}}
	if validateActionResult(result, req, time.Now()) != nil {
		t.Fatal("bound transaction rejected")
	}
	result.InvocationID = strings.Repeat("d", 32)
	if validateActionResult(result, req, time.Now()) == nil {
		t.Fatal("foreign transaction accepted")
	}
	result.InvocationID = req.ID
	result.IdentityKind = "unverified"
	if validateActionResult(result, req, time.Now()) == nil {
		t.Fatal("unknown identity kind accepted")
	}
}

func TestOpsActionApplyRejectsChangedOrigin(t *testing.T) {
	for _, mode := range []string{"identity-kind-change", "foreign-apply-digest"} {
		t.Run(mode, func(t *testing.T) {
			f, dir, _ := actorFixture(t, mode)
			cap := f.app.opsActor.config.Capabilities[0]
			plan, err := f.app.actionPlan(context.Background(), cap.ID, cap.Target)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.app.actionApply(context.Background(), plan.ID, plan.Plan.Digest); err != nil {
				t.Fatal(err)
			}
			result := waitAction(t, f, plan.ID)
			if result.State != "uncertain" || result.ErrorCode != "recovery_origin_mismatch" || countAction(dir, "apply") != 1 {
				t.Fatal("changed origin accepted or replayed")
			}
		})
	}
	plan := opsActionResult{IdentityKind: "docker_transaction", Digest: strings.Repeat("a", 64)}
	timeout := opsActionResult{State: "uncertain", ErrorCode: "action_timeout"}
	rejectChangedActionOrigin(&timeout, plan)
	if timeout.ErrorCode != "action_timeout" {
		t.Fatal("missing timeout evidence relabelled as identity mismatch")
	}
}

func TestOpsActionRecoveryRetainsOriginalIdentityKind(t *testing.T) {
	f, _, _ := actorFixture(t, "identity-kind-change")
	cap := f.app.opsActor.config.Capabilities[0]
	plan, err := f.app.actionPlan(context.Background(), cap.ID, cap.Target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.actionApply(context.Background(), plan.ID, plan.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	record := waitAction(t, f, plan.ID)
	record.Result.InvocationID = record.ID
	record.Result.IdentityKind = "docker_transaction"
	if err = f.app.saveAction(record); err != nil {
		t.Fatal(err)
	}
	after, err := f.app.actionReconcile(context.Background(), record.ID)
	if err != nil || after.State != "uncertain" || after.ErrorCode != "recovery_origin_mismatch" || after.Result.InvocationID != record.ID || after.Result.IdentityKind != "docker_transaction" {
		t.Fatal("recovery relabelled retained transaction as another identity")
	}
}
