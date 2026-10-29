package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/control"
)

func managedControlRepo(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("v2 执行回执写入尚未支持 Windows；只读载荷单测独立覆盖")
	}
	controlRepo(t)
	data, err := os.ReadFile("infrafile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "infrafile.yaml", string(data)+"\nstorageVersion: v1\n")
}

func managedPlan(t *testing.T) control.Plan {
	t.Helper()
	var p control.Plan
	if json.Unmarshal(importCLI(t, 0, "plan", "cdn-pause", "Resource/edge", "-o", "plan.json", "--json"), &p) != nil {
		t.Fatal("bad plan")
	}
	return p
}
func applyManaged(p control.Plan) []string {
	return []string{"apply", "plan.json", "--reviewed-digest", p.Digest, "--authorization-ref", "chat:test", "--actor", "test-user", "--json"}
}

func TestManagedControlRejectsExpiredFutureTamperedAndChangedInputs(t *testing.T) {
	for _, mode := range []string{"expired", "future", "tampered", "record", "config", "marker", "actor", "reference"} {
		t.Run(mode, func(t *testing.T) {
			managedControlRepo(t)
			p := managedPlan(t)
			args := applyManaged(p)
			at := time.Now()
			switch mode {
			case "expired":
				at = at.Add(31 * time.Minute)
			case "future":
				at = at.Add(-time.Minute)
			case "tampered":
				p.Actions[0].Target = "Resource/other"
				p.Digest = p.Sum()
				data, _ := p.Encode(context.Background())
				writeImportFile(t, "plan.json", string(data))
			case "record":
				b, _ := os.ReadFile("inventory/resources/edge.yaml")
				writeImportFile(t, "inventory/resources/edge.yaml", strings.Replace(string(b), "example-zone", "changed-zone", 1))
			case "config":
				b, _ := os.ReadFile("infrafile.yaml")
				writeImportFile(t, "infrafile.yaml", strings.Replace(string(b), "home: {}", "home: {region: other}", 1))
			case "marker":
				b, _ := os.ReadFile("infrafile.yaml")
				writeImportFile(t, "infrafile.yaml", strings.Replace(string(b), "storageVersion: v1", "", 1))
			case "actor":
				args[7] = "untrusted\nactor"
			case "reference":
				args[5] = "ghp_" + strings.Repeat("A", 36)
			}
			var out, stderr bytes.Buffer
			if code := runAt(args, &out, &stderr, at); code != 2 || out.Len() != 0 || strings.Contains(stderr.String(), "ghp_") {
				t.Fatal(mode, code, out.String(), stderr.String())
			}
			if _, err := os.Stat("changed"); !os.IsNotExist(err) {
				t.Fatal("rejected plan wrote")
			}
		})
	}
}

func TestManagedControlPrewriteFailureAndPostwriteUncertainty(t *testing.T) {
	for _, mode := range []string{"prewrite_journal_fail", "lost_reply", "readback_fail", "final_journal_fail", "mismatch", "hang_after_write"} {
		t.Run(mode, func(t *testing.T) {
			managedControlRepo(t)
			p := managedPlan(t)
			t.Setenv("INFRAFILE_CONTROL_CASE", mode)
			args := applyManaged(p)
			var output []byte
			if mode == "hang_after_write" {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				written := make(chan bool, 1)
				// 在写后才取消，避免慢 runner 的插件启动耗时把故障移到写前。
				go func() {
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()
					for {
						if _, err := os.Stat("control-child.pid"); err == nil {
							written <- true
							cancel()
							return
						}
						select {
						case <-ctx.Done():
							written <- false
							return
						case <-ticker.C:
						}
					}
				}()
				root := newRoot(time.Now())
				root.SetArgs(args)
				var stdout, stderr bytes.Buffer
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				err := root.ExecuteContext(ctx)
				cancel()
				var code exitCode
				if !<-written || !errors.As(err, &code) || code != 1 {
					t.Fatalf("write then cancel: %v stdout=%s stderr=%s", err, &stdout, &stderr)
				}
				output = stdout.Bytes()
			} else {
				output = importCLI(t, 1, args...)
			}
			var r control.Result
			if json.Unmarshal(output, &r) != nil || r.Receipt != p.Digest {
				t.Fatal("missing failed receipt", r)
			}
			if mode == "mismatch" {
				if r.Status != "mismatch" {
					t.Fatal(r)
				}
			} else if r.Status != "uncertain" {
				t.Fatal(r)
			}
			_, err := os.Stat("changed")
			if mode == "prewrite_journal_fail" {
				if !os.IsNotExist(err) {
					t.Fatal("write despite failed write-ahead persistence")
				}
				return
			}
			if err != nil {
				t.Fatal("fixture never wrote", err)
			}
			if mode == "final_journal_fail" {
				return
			}
			before, _ := os.ReadFile(filepath.Join("history/executions", p.Digest+".json"))
			writeImportFile(t, "changed", "preserve-external")
			out := importCLI(t, 1, args...)
			if json.Unmarshal(out, &r) != nil || !r.Replayed {
				t.Fatal("failure blindly retried", string(out))
			}
			after, _ := os.ReadFile(filepath.Join("history/executions", p.Digest+".json"))
			got, _ := os.ReadFile("changed")
			if !bytes.Equal(before, after) || string(got) != "preserve-external" {
				t.Fatal("replay altered original evidence or remote state")
			}
		})
	}
}

func TestManagedControlExpirationCheckedAgainAfterProbe(t *testing.T) {
	managedControlRepo(t)
	p := managedPlan(t)
	p.GeneratedAt = time.Now().Add(-control.PlanLifetime + time.Second).UTC().Format(time.RFC3339)
	generated, _ := time.Parse(time.RFC3339, p.GeneratedAt)
	p.ExpiresAt = generated.Add(control.PlanLifetime).Format(time.RFC3339)
	p.Digest = p.Sum()
	data, _ := p.Encode(context.Background())
	writeImportFile(t, "plan.json", string(data))
	t.Setenv("INFRAFILE_CONTROL_CASE", "slow_probe")
	importCLI(t, 2, applyManaged(p)...)
	if _, err := os.Stat("changed"); !os.IsNotExist(err) {
		t.Fatal("expired after probe still wrote")
	}
}

func TestManagedControlBindingChangesDuringProbeRefuseControl(t *testing.T) {
	managedControlRepo(t)
	p := managedPlan(t)
	t.Setenv("INFRAFILE_CONTROL_CASE", "change_binding_during_probe")
	importCLI(t, 2, applyManaged(p)...)
	if _, err := os.Stat("changed"); !os.IsNotExist(err) {
		t.Fatal("stale local binding wrote")
	}
}

func TestManagedControlJournalPathFailuresNeverReachControl(t *testing.T) {
	for _, mode := range []string{"directory", "symlink", "parent_symlink", "public_mode"} {
		t.Run(mode, func(t *testing.T) {
			managedControlRepo(t)
			p := managedPlan(t)
			root := filepath.Join("history", "executions")
			outside := t.TempDir()
			if mode == "parent_symlink" {
				if err := os.Symlink(outside, "history"); err != nil {
					t.Skip("symlink unavailable")
				}
			} else {
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(root, p.Digest+".json")
				switch mode {
				case "directory":
					os.Mkdir(file, 0700)
				case "symlink":
					os.WriteFile(filepath.Join(outside, "receipt"), []byte("untouched"), 0600)
					if err := os.Symlink(filepath.Join(outside, "receipt"), file); err != nil {
						t.Skip("symlink unavailable")
					}
				case "public_mode":
					os.Chmod(root, 0755)
				}
			}
			importCLI(t, 2, applyManaged(p)...)
			if _, err := os.Stat("changed"); !os.IsNotExist(err) {
				t.Fatal("journal failure reached control")
			}
			if mode == "parent_symlink" {
				entries, _ := os.ReadDir(outside)
				if len(entries) != 0 {
					t.Fatal("followed history symlink")
				}
			}
			if mode == "symlink" {
				data, _ := os.ReadFile(filepath.Join(outside, "receipt"))
				if string(data) != "untouched" {
					t.Fatal("followed receipt symlink")
				}
			}
		})
	}
}

func TestManagedControlCrashAndConcurrentExecution(t *testing.T) {
	if os.Getenv("INFRAFILE_EXECUTION_CHILD") == "1" {
		p, err := control.Read(context.Background(), "plan.json")
		if err != nil {
			os.Exit(8)
		}
		os.Exit(run(applyManaged(*p), os.Stdout, os.Stderr))
	}
	managedControlRepo(t)
	p := managedPlan(t)
	t.Setenv("INFRAFILE_CONTROL_CASE", "hang_after_write")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestManagedControlCrashAndConcurrentExecution$")
	cmd.Env = append(os.Environ(), "INFRAFILE_EXECUTION_CHILD=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat("control-child.pid"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	pidBytes, _ := os.ReadFile("control-child.pid")
	pid, _ := strconv.Atoi(string(pidBytes))
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Kill() })
	importCLI(t, 2, applyManaged(p)...)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("crashed host succeeded")
	}
	child.Kill()
	r, err := control.ReadReceipt(context.Background(), ".", p.Digest)
	if err != nil || r.Data.Phase != "write_started" || r.Data.Result.Status != "uncertain" {
		t.Fatal("crash erased uncertain marker", r, err)
	}
	t.Setenv("INFRAFILE_CONTROL_CASE", "")
	before, _ := os.ReadFile(filepath.Join("history/executions", p.Digest+".json"))
	var result control.Result
	if json.Unmarshal(importCLI(t, 1, applyManaged(p)...), &result) != nil || !result.Replayed || result.Verification != "matches_expected" || result.Status != "uncertain" {
		t.Fatal("restart inferred a confirmed success", result)
	}
	after, _ := os.ReadFile(filepath.Join("history/executions", p.Digest+".json"))
	if !bytes.Equal(before, after) {
		t.Fatal("restart overwrote original uncertain receipt")
	}
}

func TestExecutionReadersAndWorkbenchAreReadOnlyAndEquivalent(t *testing.T) {
	managedControlRepo(t)
	p := managedPlan(t)
	importCLI(t, 0, applyManaged(p)...)
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	f := newWebFixture(t, dir)
	f.login(t)
	before := mcpFiles(t)
	for _, tc := range []struct {
		route, body string
		args        []string
	}{
		{"executions", `{"key":"Resource/edge"}`, []string{"executions", "list", "--key", "Resource/edge", "--json"}},
		{"execution", `{"id":"` + p.Digest + `"}`, []string{"executions", "show", p.Digest, "--json"}},
	} {
		out := f.send(f.request("POST", "/api/"+tc.route, tc.body))
		if out.Code != 200 {
			t.Fatal(tc.route, out.Code, out.Body.String())
		}
		var web, cli any
		if json.Unmarshal(out.Body.Bytes(), &web) != nil || json.Unmarshal(importCLI(t, 0, tc.args...), &cli) != nil || !reflect.DeepEqual(web, cli) {
			t.Fatal("web and CLI receipt differ")
		}
	}
	for _, tc := range []struct{ route, body string }{
		{"execution", `{"id":"../../outside"}`}, {"execution", `{"id":"` + p.Digest + `","command":"apply"}`}, {"execution", `{"id":"` + p.Digest + `","id":"` + p.Digest + `"}`}, {"executions", `{"dir":"/tmp"}`},
	} {
		if out := f.send(f.request("POST", "/api/"+tc.route, tc.body)); out.Code != 400 {
			t.Fatal(tc, out.Code)
		}
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("readers changed receipt files")
	}
	writeImportFile(t, filepath.Join("history/executions", p.Digest+".json"), `{"version":"v99"}`)
	if out := f.send(f.request("POST", "/api/executions", `{}`)); out.Code == 200 {
		t.Fatal("corrupt receipt presented as empty success")
	}
}

func TestManagedMCPPlanUsesV2WithoutWritingHistory(t *testing.T) {
	managedControlRepo(t)
	before := mcpFiles(t)
	session := mcpSession(t, ".")
	value := callMCP(t, session, "plan", map[string]any{"key": "Resource/edge", "action": "cdn-pause"}, false)
	data, _ := json.Marshal(value["plan"])
	var p control.Plan
	if json.Unmarshal(data, &p) != nil || p.Version != "v2" || p.Validate() != nil || value["dry_run"] != true {
		t.Fatal("missing v2 dry-run", string(data))
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("MCP plan wrote local files")
	}
}

func TestManagedControlNoopRecordsMatchingReadWithoutControl(t *testing.T) {
	managedControlRepo(t)
	var p control.Plan
	if json.Unmarshal(importCLI(t, 0, "plan", "cdn-resume", "Resource/edge", "-o", "plan.json", "--json"), &p) != nil {
		t.Fatal("bad noop plan")
	}
	var result control.Result
	if json.Unmarshal(importCLI(t, 0, applyManaged(p)...), &result) != nil || result.Status != "unchanged" {
		t.Fatal("noop execution failed", result)
	}
	r, err := control.ReadReceipt(context.Background(), ".", p.Digest)
	if err != nil || r.Data.WriteStartedAt != "" || r.Data.ReadbackHash != p.Actions[0].BeforeHash {
		t.Fatal("invalid noop evidence", err)
	}
	if _, err := os.Stat("changed"); !os.IsNotExist(err) {
		t.Fatal("noop sent control")
	}
}

func TestManagedExecutionLockIsPrivateAndIgnoredInExistingGitRepo(t *testing.T) {
	managedControlRepo(t)
	p := managedPlan(t)
	if out, err := exec.Command("git", "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	importCLI(t, 0, applyManaged(p)...)
	for _, name := range []string{".infrafile/execution.lock", ".infrafile/.gitignore"} {
		info, err := os.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			t.Fatal("private execution metadata missing", name, err)
		}
		if err := exec.Command("git", "check-ignore", "-q", name).Run(); err != nil {
			t.Fatal("private metadata would be staged by default", name, err)
		}
	}
	if _, err := os.Stat("history/executions/.execution.lock"); !os.IsNotExist(err) {
		t.Fatal("execution lock leaked into receipt history")
	}
}

func TestManagedExecutionPreservesExistingPrivateIgnoreRules(t *testing.T) {
	managedControlRepo(t)
	p := managedPlan(t)
	if err := os.Mkdir(".infrafile", 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("!operator-file\n")
	writeImportFile(t, ".infrafile/.gitignore", string(data))
	importCLI(t, 2, applyManaged(p)...)
	after, _ := os.ReadFile(".infrafile/.gitignore")
	if !bytes.Equal(data, after) {
		t.Fatal("overwrote user ignore rules")
	}
	if _, err := os.Stat("changed"); !os.IsNotExist(err) {
		t.Fatal("unprotected private lock reached cloud control")
	}
}

func TestManagedScopeBindsRelatedRegistrationsAndAllowsUnrelatedEdits(t *testing.T) {
	for _, mode := range []string{"related", "suppressed_scope", "unrelated"} {
		t.Run(mode, func(t *testing.T) {
			managedControlRepo(t)
			if err := os.MkdirAll("inventory/apps", 0700); err != nil {
				t.Fatal(err)
			}
			file := "inventory/apps/consumer.yaml"
			record := "apiVersion: infrafile/v1alpha1\nkind: App\nmetadata: {name: consumer}\nspec: {resources: [edge]}\n"
			writeImportFile(t, file, record)
			p := managedPlan(t)
			if p.Scope == nil || len(p.Scope.Related) != 1 || p.Scope.Related[0].Key != "App/consumer" {
				t.Fatal("missing associated consumer", p.Scope)
			}
			switch mode {
			case "related":
				writeImportFile(t, file, strings.Replace(record, "name: consumer", "name: consumer, description: edited", 1))
			case "suppressed_scope":
				p.Scope.Related = []control.ScopeAsset{}
				p.Digest = p.Sum()
				data, _ := p.Encode(context.Background())
				writeImportFile(t, "plan.json", string(data))
			case "unrelated":
				os.MkdirAll("inventory/servers", 0700)
				writeImportFile(t, "inventory/servers/other.yaml", "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: other}\nspec: {address: 192.0.2.10}\n")
			}
			if mode == "unrelated" {
				importCLI(t, 0, applyManaged(p)...)
			} else {
				importCLI(t, 2, applyManaged(p)...)
				if _, err := os.Stat("changed"); !os.IsNotExist(err) {
					t.Fatal("changed association reached control")
				}
			}
		})
	}
}

func TestManagedPlanShowExplainsUnknownScopeAndOmitsReferences(t *testing.T) {
	managedControlRepo(t)
	p := managedPlan(t)
	out := importCLI(t, 0, "plan", "show", "plan.json")
	if !bytes.Contains(out, []byte("实际作用范围未知")) || !bytes.Contains(out, []byte("不表示没有影响")) || !bytes.Contains(out, []byte("未知影响")) {
		t.Fatal("scope or unknown hidden", string(out))
	}
	data, _ := p.Encode(context.Background())
	if bytes.Contains(data, []byte("env://")) {
		t.Fatal("scope copied credential references")
	}
}

func TestManagedStorageRefusesLegacyExecutionWithoutRewritingPlan(t *testing.T) {
	controlRepo(t)
	importCLI(t, 0, "plan", "cdn-pause", "Resource/edge", "-o", "plan.json")
	before, _ := os.ReadFile("plan.json")
	cfg, _ := os.ReadFile("infrafile.yaml")
	writeImportFile(t, "infrafile.yaml", string(cfg)+"\nstorageVersion: v1\n")
	importCLI(t, 0, "plan", "show", "plan.json", "--json")
	importCLI(t, 2, "apply", "plan.json", "--json")
	after, _ := os.ReadFile("plan.json")
	if !bytes.Equal(before, after) {
		t.Fatal("legacy plan silently upgraded")
	}
	if _, err := os.Stat("changed"); !os.IsNotExist(err) {
		t.Fatal("legacy plan bypassed managed execution")
	}
}

func TestManagedControlBindsReviewAndPersistsBeforeWriting(t *testing.T) {
	managedControlRepo(t)
	var p control.Plan
	if json.Unmarshal(importCLI(t, 0, "plan", "cdn-pause", "Resource/edge", "-o", "plan.json", "--json"), &p) != nil || p.Version != "v2" || p.Digest == "" {
		t.Fatal("missing managed plan")
	}
	for _, flags := range [][]string{nil, {"--reviewed-digest", strings.Repeat("0", 64), "--authorization-ref", "chat:test", "--actor", "test-user"}} {
		args := append([]string{"apply", "plan.json", "--json"}, flags...)
		importCLI(t, 2, args...)
		if _, err := os.Stat("changed"); !os.IsNotExist(err) {
			t.Fatal("unreviewed control performed")
		}
	}
	t.Setenv("INFRAFILE_CONTROL_CASE", "require_receipt")
	args := []string{"apply", "plan.json", "--reviewed-digest", p.Digest, "--authorization-ref", "chat:test", "--actor", "test-user", "--json"}
	var result control.Result
	if json.Unmarshal(importCLI(t, 0, args...), &result) != nil || result.Status != "applied" || result.Receipt != p.Digest {
		t.Fatal("missing applied receipt", result)
	}
	data, err := os.ReadFile(filepath.Join("history", "executions", p.Digest+".json"))
	if err != nil || !bytes.Contains(data, []byte(`"actor": "test-user"`)) || !bytes.Contains(data, []byte(`"authorization_ref": "chat:test"`)) {
		t.Fatal("missing audit attribution", err)
	}
	writeImportFile(t, "changed", "external-after-first-write")
	if json.Unmarshal(importCLI(t, 0, args...), &result) != nil || !result.Replayed || result.Verification != "matches_expected" {
		t.Fatal("duplicate was not verified", result)
	}
	got, _ := os.ReadFile("changed")
	if string(got) != "external-after-first-write" {
		t.Fatal("duplicate sent control again")
	}
}
