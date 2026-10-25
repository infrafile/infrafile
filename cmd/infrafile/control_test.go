package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPlanApplyUnchanged(t *testing.T) {
	controlRepo(t)
	importCLI(t, 0, "plan", "cdn-pause", "Resource/edge", "-o", "plan.json")
	if _, err := os.Stat("changed"); !os.IsNotExist(err) {
		t.Fatal("plan performed a write")
	}
	importCLI(t, 0, "plan", "show", "plan.json")
	out := importCLI(t, 0, "apply", "plan.json", "--json")
	if !json.Valid(out) {
		t.Fatalf("bad JSON: %s", out)
	}
	if _, err := os.Stat("changed"); err != nil {
		t.Fatal("apply did not control plugin")
	}
}

func controlRepo(t *testing.T) {
	fixturePlugin(t)
	t.Setenv("INFRAFILE_PLUGIN_MODE", "control")
	emptyRepo(t)
	if err := os.MkdirAll("inventory/resources", 0755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n  aaa:\n    instances:\n      home: {}\n")
	writeImportFile(t, "inventory/resources/edge.yaml", "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: edge}\nspec:\n  provider: aaa\n  type: cdn\n  control: {plugin: aaa, instance: home, zone_id: example-zone}\n")
}

func controlHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Action string `json:"action"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(3)
		}
		result := any(map[string]any{"data": map[string]any{"paused": false}})
		switch req.Method {
		case "describe":
			result = map[string]any{"protocol_version": "v1", "capabilities": []string{"probe", "control"}}
		case "probe":
			if os.Getenv("INFRAFILE_CONTROL_CASE") == "timeout" {
				time.Sleep(time.Minute)
			}
			_, err := os.Stat("changed")
			paused := err == nil
			if os.Getenv("INFRAFILE_CONTROL_CASE") == "mismatch" {
				paused = false
			}
			state := map[string]any{"paused": paused}
			if req.Params.Action == "cdn-origin" {
				state["rules"] = []any{map[string]any{"id": "example-rule", "action": "route", "enabled": true, "action_parameters": map[string]any{"origin": map[string]any{"host": "old.example.net"}}}}
			}
			if os.Getenv("INFRAFILE_CONTROL_CASE") == "large" {
				// 小 RPC 经缩进后也可能成为大计划，避免用多 MiB 文本拖慢 race 测试。
				values := make([]string, 20000)
				for i := range values {
					values[i] = "x"
				}
				var nested any = values
				for i := 0; i < 50; i++ {
					nested = map[string]any{"nested": nested}
				}
				state["description"] = nested
			}
			if os.Getenv("INFRAFILE_CONTROL_CASE") == "sensitive" {
				state["password"] = "synthetic-control-secret"
			}
			result = map[string]any{"data": state}
		case "control":
			if req.Params.Action == "cdn-resume" {
				if err := os.Remove("changed"); err != nil {
					os.Exit(5)
				}
			} else {
				if err := os.WriteFile("changed", []byte("yes"), 0600); err != nil {
					os.Exit(5)
				}
			}
			result = map[string]any{"data": map[string]any{}}
		}
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		fmt.Println(string(data))
	}
}

func TestPlanRejectsOversizedOutputBeforePublishing(t *testing.T) {
	controlRepo(t)
	t.Setenv("INFRAFILE_CONTROL_CASE", "large")
	for _, flags := range [][]string{{"-o", "large.json"}, {"--json"}} {
		var stdout, stderr bytes.Buffer
		args := append([]string{"plan", "cdn-pause", "Resource/edge", "--timeout", "2m"}, flags...)
		code := run(args, &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "计划超过 4 MiB") {
			t.Fatalf("oversize result: exit=%d stdout=%d bytes stderr=%s", code, stdout.Len(), &stderr)
		}
	}
	if _, err := os.Stat("large.json"); !os.IsNotExist(err) {
		t.Fatal("unreadable plan file created")
	}
}

func TestApplyStateChangedIsRefused(t *testing.T) {
	controlRepo(t)
	importCLI(t, 0, "plan", "cdn-pause", "Resource/edge", "-o", "plan.json")
	writeImportFile(t, "changed", "external-change")
	out := importCLI(t, 1, "apply", "plan.json", "--json")
	if !bytes.Contains(out, []byte(`"status": "stale"`)) {
		t.Fatalf("not stale: %s", out)
	}
	got, _ := os.ReadFile("changed")
	if string(got) != "external-change" {
		t.Fatal("stale apply wrote")
	}
}

func TestApplyReadbackMismatch(t *testing.T) {
	controlRepo(t)
	importCLI(t, 0, "plan", "cdn-pause", "Resource/edge", "-o", "plan.json")
	t.Setenv("INFRAFILE_CONTROL_CASE", "mismatch")
	out := importCLI(t, 1, "apply", "plan.json", "--json")
	if !bytes.Contains(out, []byte(`"status": "mismatch"`)) {
		t.Fatalf("not mismatch: %s", out)
	}
}

func TestApplyResumeAndNoop(t *testing.T) {
	controlRepo(t)
	writeImportFile(t, "changed", "initial")
	importCLI(t, 0, "plan", "cdn-resume", "Resource/edge", "-o", "resume.json")
	importCLI(t, 0, "apply", "resume.json")
	if _, err := os.Stat("changed"); !os.IsNotExist(err) {
		t.Fatal("resume not applied")
	}
	importCLI(t, 0, "plan", "cdn-resume", "Resource/edge", "-o", "noop.json")
	out := importCLI(t, 0, "apply", "noop.json", "--json")
	if !bytes.Contains(out, []byte(`"status": "unchanged"`)) {
		t.Fatal(string(out))
	}
}

func TestApplyLedgerChangesRefused(t *testing.T) {
	for _, change := range []string{"record", "config", "archived"} {
		t.Run(change, func(t *testing.T) {
			controlRepo(t)
			importCLI(t, 0, "plan", "cdn-pause", "Resource/edge", "-o", "plan.json")
			file := "inventory/resources/edge.yaml"
			if change == "config" {
				file = "infrafile.yaml"
			}
			data, _ := os.ReadFile(file)
			s := string(data)
			switch change {
			case "record":
				s = strings.Replace(s, "example-zone", "different-zone", 1)
			case "config":
				s = strings.Replace(s, "home: {}", "home: {region: another}", 1)
			case "archived":
				s = strings.Replace(s, "metadata: {name: edge}", "metadata: {name: edge, archived: {date: 2026-10-25, reason: retired}}", 1)
			}
			writeImportFile(t, file, s)
			importCLI(t, 2, "apply", "plan.json")
			if _, err := os.Stat("changed"); !os.IsNotExist(err) {
				t.Fatal("modified target applied")
			}
		})
	}
}

func TestPlanValidationAndSecretSafety(t *testing.T) {
	controlRepo(t)
	for _, mode := range []string{"sensitive", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("INFRAFILE_CONTROL_CASE", mode)
			var out, errout bytes.Buffer
			code := run([]string{"plan", "cdn-pause", "Resource/edge", "--timeout", "150ms"}, &out, &errout)
			if code != 2 || out.Len() != 0 || strings.Contains(errout.String(), "synthetic-control-secret") {
				t.Fatalf("unsafe failure: %d %s %s", code, &out, &errout)
			}
		})
	}
	importCLI(t, 0, "plan", "cdn-pause", "Resource/edge", "-o", "plan.json")
	original, _ := os.ReadFile("plan.json")
	for _, tamper := range []string{"version", "extra", "after", "hash", "duplicate", "case-duplicate", "case-variant", "multiple"} {
		t.Run(tamper, func(t *testing.T) {
			var p map[string]any
			json.Unmarshal(original, &p)
			a := p["actions"].([]any)[0].(map[string]any)
			switch tamper {
			case "version":
				p["version"] = "v9"
			case "extra":
				p["arbitrary"] = true
			case "after":
				a["after"] = map[string]any{"paused": false}
			case "hash":
				a["before_hash"] = strings.Repeat("0", 64)
			case "multiple":
				p["actions"] = []any{a, a}
			}
			data, _ := json.Marshal(p)
			switch tamper {
			case "duplicate":
				data = bytes.Replace(data, []byte(`"version":"v1"`), []byte(`"version":"v2","version":"v1"`), 1)
			case "case-duplicate":
				// jq 看到 v2，大小写不敏感的解码器却取 v1。
				data = bytes.Replace(data, []byte(`"version":"v1"`), []byte(`"version":"v2","Version":"v1"`), 1)
			case "case-variant":
				// jq 的 .parameters 为空，解码器仍会使用它。
				data = bytes.Replace(data, []byte(`"parameters":`), []byte(`"Parameters":`), 1)
			}
			writeImportFile(t, "bad.json", string(data))
			importCLI(t, 2, "plan", "show", "bad.json")
			importCLI(t, 2, "apply", "bad.json")
		})
	}
	importCLI(t, 2, "plan", "cdn-pause", "192.0.2.1")
	importCLI(t, 2, "plan", "cdn-pause", "Resource/edge", "-o", "plan.json")
	after, _ := os.ReadFile("plan.json")
	if !bytes.Equal(original, after) {
		t.Fatal("plan overwritten")
	}
}
