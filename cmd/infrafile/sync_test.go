package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/protocol"
	"go.yaml.in/yaml/v3"
)

func TestSyncBuiltin(t *testing.T) {
	dir := t.TempDir()
	config := "apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n  example:\n    instances:\n      home: {}\n"
	if err := os.WriteFile(filepath.Join(dir, "infrafile.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	now := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	if code := runAt([]string{"sync", "--dir", dir, "--json"}, &out, &stderr, now); code != 0 {
		t.Fatalf("exit %d: %s %s", code, &out, &stderr)
	}
	file := filepath.Join(dir, "state", "example", "home.yaml")
	first, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "kind: Snapshot") || !strings.Contains(string(first), "example.com") || bytes.Contains(first, []byte("2026")) {
		t.Fatalf("snapshot: %s", first)
	}
	var snapshot any
	if err := yaml.Unmarshal(first, &snapshot); err != nil {
		t.Fatal(err)
	}
	jsonSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := protocol.Validate("snapshot", jsonSnapshot); err != nil {
		t.Fatalf("snapshot schema: %v", err)
	}
	out.Reset()
	stderr.Reset()
	if code := runAt([]string{"sync", "example", "--dir", dir}, &out, &stderr, now.Add(time.Hour)); code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	second, _ := os.ReadFile(file)
	if !bytes.Equal(first, second) {
		t.Fatalf("unchanged data changed snapshot: %s", second)
	}
	stamp, err := os.ReadFile(filepath.Join(dir, "state", "_sync.yaml"))
	if err != nil || !bytes.Contains(stamp, []byte("2026-12-01T13:00:00Z")) {
		t.Fatalf("sync time: %s %v", stamp, err)
	}
	gotConfig, _ := os.ReadFile(filepath.Join(dir, "infrafile.yaml"))
	if string(gotConfig) != config {
		t.Fatal("changed config")
	}
}

func TestSyncProtectsStateAndInventory(t *testing.T) {
	for _, scenario := range []string{"state-link", "plugin-link", "snapshot-link", "log-link", "bad-log", "incomplete-log", "locked"} {
		t.Run(scenario, func(t *testing.T) {
			dir := syncRepo(t, "  example:\n    instances:\n      home: {}\n")
			inventory := filepath.Join(dir, "inventory", "servers", "web.yaml")
			before, _ := os.ReadFile(inventory)
			stateDir := filepath.Join(dir, "state")
			var link, target string
			if scenario == "state-link" {
				link, target = stateDir, filepath.Dir(inventory)
			} else {
				if err := os.MkdirAll(filepath.Join(stateDir, "example"), 0o755); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "plugin-link":
					if err := os.Remove(filepath.Join(stateDir, "example")); err != nil {
						t.Fatal(err)
					}
					link, target = filepath.Join(stateDir, "example"), filepath.Dir(inventory)
				case "snapshot-link":
					link, target = filepath.Join(stateDir, "example", "home.yaml"), inventory
				case "log-link":
					link, target = filepath.Join(stateDir, "_sync.yaml"), inventory
				case "bad-log":
					if err := os.WriteFile(filepath.Join(stateDir, "_sync.yaml"), []byte("[broken"), 0o644); err != nil {
						t.Fatal(err)
					}
				case "incomplete-log":
					if err := os.WriteFile(filepath.Join(stateDir, "_sync.yaml"), []byte("kind: Sync\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				case "locked":
					if err := os.WriteFile(filepath.Join(stateDir, ".sync.lock"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if link != "" {
				if err := os.Symlink(target, link); err != nil {
					t.Skip("系统不支持符号链接")
				}
			}
			var out, stderr bytes.Buffer
			if code := run([]string{"sync", "--dir", dir, "--json"}, &out, &stderr); code == 0 {
				t.Fatalf("unsafe write: %s", &out)
			}
			after, _ := os.ReadFile(inventory)
			if !bytes.Equal(before, after) {
				t.Fatal("改动了 inventory")
			}
			if scenario == "locked" {
				if _, err := os.Stat(filepath.Join(stateDir, ".sync.lock")); err != nil {
					t.Fatal("删除了其他 sync 的锁")
				}
			}
		})
	}
}

func TestSyncConfigValidation(t *testing.T) {
	for _, config := range []string{
		"  ../escape:\n    instances: {home: {}}\n",
		"  example:\n    instances: {../escape: {}}\n",
		"  example:\n    instances: {}\n",
		"  example:\n    instances: {home: {password: synthetic-private-value}}\n",
	} {
		dir := syncRepo(t, config)
		var out, stderr bytes.Buffer
		if code := run([]string{"sync", "--dir", dir, "--json"}, &out, &stderr); code != 2 || out.Len() != 0 {
			t.Fatalf("invalid config: %d %s %s", code, &out, &stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
			t.Fatal("无效配置仍然创建了 state")
		}
	}
}

func TestSyncSelectedPluginAndMissingExecutable(t *testing.T) {
	dir := syncRepo(t, "  missing-fixture-plugin:\n    instances: {home: {}}\n  example:\n    instances: {home: {}}\n")
	for _, tc := range []struct {
		name string
		code int
	}{{"example", 0}, {"missing-fixture-plugin", 1}, {"not-enabled", 2}} {
		var out, stderr bytes.Buffer
		if code := run([]string{"sync", tc.name, "--dir", dir, "--json"}, &out, &stderr); code != tc.code {
			t.Fatalf("%s: exit %d: %s %s", tc.name, code, &out, &stderr)
		}
		if tc.code == 2 && out.Len() != 0 {
			t.Fatal("用法错误有 stdout")
		}
		if tc.name == "missing-fixture-plugin" && !strings.Contains(out.String(), `"code": "not-found"`) {
			t.Fatal("缺失插件未分类")
		}
	}
}
