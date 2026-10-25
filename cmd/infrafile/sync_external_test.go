package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// 用真实子进程测试 stdio 边界，协议错误不依赖 Python 或 shell。
func TestMain(m *testing.M) {
	if os.Getenv("INFRAFILE_PLUGIN_HELPER") == "1" && strings.HasPrefix(filepath.Base(os.Args[0]), "infrafile-plugin-") {
		pluginHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func pluginHelper() {
	mode := os.Getenv("INFRAFILE_PLUGIN_MODE")
	fmt.Fprintln(os.Stderr, "synthetic-plugin-private-value")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Instance string         `json:"instance"`
				Config   map[string]any `json:"config"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(3)
		}
		if mode == "crash" {
			os.Exit(7)
		}
		if mode == "hang" || (mode == "sync-hang" && req.Method == "sync") {
			time.Sleep(time.Minute)
		}
		reply := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if req.Method == "describe" {
			version := "v1"
			capabilities := []string{"sync", "probe", "control", "import"}
			if mode == "version" {
				version = "v99"
			}
			if mode == "capability" {
				capabilities = []string{"probe"}
			}
			reply["result"] = map[string]any{"protocol_version": version, "capabilities": capabilities}
		} else {
			if mode == "time-write-failure" {
				file := filepath.Join(os.Getenv("INFRAFILE_TEST_STATE"), "_sync.yaml")
				if req.Params.Instance == "a" {
					if os.Rename(file, file+".saved") != nil || os.Mkdir(file, 0o755) != nil {
						os.Exit(8)
					}
				} else {
					if os.Remove(file) != nil || os.Rename(file+".saved", file) != nil {
						os.Exit(8)
					}
				}
			}
			data := map[string]any{"instance": req.Params.Instance, "region": req.Params.Config["region"], "id": uint64(9007199254740993), "large": json.RawMessage("18446744073709551617"), "decimal": json.RawMessage("0.1234567890123456789")}
			switch mode {
			case "sensitive":
				data["nested"] = []any{map[string]any{"clientSecret": "synthetic-plugin-private-value"}}
			case "token":
				data["description"] = "ghp_" + strings.Repeat("a", 36)
			case "key-token":
				data["ghp_"+strings.Repeat("a", 36)] = true
			case "empty-secret":
				data["private_key"] = nil
			case "ref-secret":
				data["api_key"] = "env://TEST"
			case "password-url":
				data["endpoint"] = "https://u:synthetic-plugin-private-value@example.com"
			case "cookies":
				data["cookies"] = []any{"session=synthetic-plugin-private-value"}
			case "passwords":
				data["passwords"] = []any{"synthetic-plugin-private-value"}
			case "tokens":
				data["accessTokens"] = []any{"synthetic-plugin-private-value"}
			}
			reply["result"] = map[string]any{"data": data}
		}
		switch mode {
		case "id":
			reply["id"] = req.ID + 1
		case "jsonrpc":
			reply["jsonrpc"] = "1.0"
		case "error":
			delete(reply, "result")
			reply["error"] = map[string]any{"code": -32000, "message": "synthetic-plugin-private-value"}
		case "both":
			reply["error"] = map[string]any{"code": -32000, "message": "synthetic-plugin-private-value"}
		case "malformed":
			fmt.Fprintln(os.Stdout, "synthetic-plugin-private-value")
			continue
		case "large":
			fmt.Fprintln(os.Stdout, strings.Repeat("x", 4<<20))
			continue
		}
		if req.Method == "sync" && mode == "shape" {
			reply["result"] = map[string]any{"data": []any{}}
		}
		if err := json.NewEncoder(os.Stdout).Encode(reply); err != nil {
			os.Exit(4)
		}
	}
}

func fixturePlugin(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, "infrafile-plugin-aaa")); err != nil {
		t.Skip("系统不支持测试命令符号链接")
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INFRAFILE_PLUGIN_HELPER", "1")
}

func syncRepo(t *testing.T, plugins string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "infrafile.yaml"), []byte("apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n"+plugins), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "inventory", "servers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inventory", "servers", "web.yaml"), []byte("apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: web}\nspec: {address: 192.0.2.1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const twoPlugins = "  aaa:\n    instances:\n      home: {region: test}\n  example:\n    instances:\n      home: {}\n"

func TestSyncExternalFailuresAreIsolated(t *testing.T) {
	fixturePlugin(t)
	for _, tc := range []struct{ mode, code string }{
		{"hang", "timeout"}, {"sync-hang", "timeout"}, {"crash", "protocol"},
		{"version", "protocol"}, {"capability", "capability"}, {"id", "protocol"}, {"jsonrpc", "protocol"},
		{"error", "plugin"}, {"both", "protocol"}, {"malformed", "protocol"}, {"large", "protocol"}, {"shape", "protocol"},
		{"sensitive", "sensitive"}, {"token", "sensitive"}, {"key-token", "sensitive"}, {"empty-secret", "sensitive"}, {"ref-secret", "sensitive"}, {"password-url", "sensitive"},
		{"cookies", "sensitive"}, {"passwords", "sensitive"}, {"tokens", "sensitive"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			dir := syncRepo(t, twoPlugins)
			t.Setenv("INFRAFILE_PLUGIN_MODE", "good")
			var out, stderr bytes.Buffer
			args := []string{"sync", "--dir", dir, "--json", "--timeout", "2s"}
			if code := run(args, &out, &stderr); code != 0 {
				t.Fatalf("seed exit %d: %s %s", code, &out, &stderr)
			}
			file := filepath.Join(dir, "state", "aaa", "home.yaml")
			before, err := os.ReadFile(file)
			if err != nil || !bytes.Contains(before, []byte("9007199254740993")) || !bytes.Contains(before, []byte("18446744073709551617")) || !bytes.Contains(before, []byte("0.1234567890123456789")) || !bytes.Contains(before, []byte("region: test")) {
				t.Fatalf("external result: %s %v", before, err)
			}
			inventoryFile := filepath.Join(dir, "inventory", "servers", "web.yaml")
			inventory, _ := os.ReadFile(inventoryFile)
			t.Setenv("INFRAFILE_PLUGIN_MODE", tc.mode)
			if tc.mode == "hang" || tc.mode == "sync-hang" {
				args[len(args)-1] = "250ms"
			}
			out.Reset()
			stderr.Reset()
			start := time.Now()
			if code := run(args, &out, &stderr); code != 1 {
				t.Fatalf("exit %d: %s %s", code, &out, &stderr)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("插件没有及时回收")
			}
			var report struct {
				OK      bool `json:"ok"`
				Results []struct {
					OK   bool   `json:"ok"`
					Code string `json:"code"`
				} `json:"results"`
			}
			if json.Unmarshal(out.Bytes(), &report) != nil || report.OK || len(report.Results) != 2 || report.Results[0].Code != tc.code || !report.Results[1].OK {
				t.Fatalf("isolation result: %s", &out)
			}
			if strings.Contains(out.String()+stderr.String(), "synthetic-plugin-private-value") {
				t.Fatal("插件错误或日志泄露到输出")
			}
			after, _ := os.ReadFile(file)
			if !bytes.Equal(before, after) {
				t.Fatal("失败覆盖了旧快照")
			}
			inventoryAfter, _ := os.ReadFile(inventoryFile)
			if !bytes.Equal(inventory, inventoryAfter) {
				t.Fatal("改动了手写台账")
			}
		})
	}
}

func TestSyncFailedTimeWriteDoesNotAdvanceLater(t *testing.T) {
	fixturePlugin(t)
	dir := syncRepo(t, "  aaa:\n    instances:\n      a: {region: test}\n      b: {region: test}\n")
	t.Setenv("INFRAFILE_PLUGIN_MODE", "good")
	t.Setenv("INFRAFILE_TEST_STATE", filepath.Join(dir, "state"))
	var out, stderr bytes.Buffer
	args := []string{"sync", "--dir", dir, "--json"}
	before := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	if code := runAt(args, &out, &stderr, before); code != 0 {
		t.Fatalf("seed exit %d: %s %s", code, &out, &stderr)
	}
	t.Setenv("INFRAFILE_PLUGIN_MODE", "time-write-failure")
	out.Reset()
	stderr.Reset()
	if code := runAt(args, &out, &stderr, before.Add(time.Hour)); code != 1 {
		t.Fatalf("exit %d: %s %s", code, &out, &stderr)
	}
	var report struct {
		Results []struct {
			OK   bool   `json:"ok"`
			Code string `json:"code"`
		} `json:"results"`
	}
	if json.Unmarshal(out.Bytes(), &report) != nil || len(report.Results) != 2 || report.Results[0].Code != "write" || !report.Results[1].OK {
		t.Fatalf("write failure not isolated: %s", &out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "state", "_sync.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var log struct {
		Spec map[string]map[string]string `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &log); err != nil {
		t.Fatal(err)
	}
	if log.Spec["aaa"]["a"] != "2026-12-01T12:00:00Z" || log.Spec["aaa"]["b"] != "2026-12-01T13:00:00Z" {
		t.Fatalf("failed instance time advanced: %s", data)
	}
}

func TestSyncPythonExample(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python 示例需要 python3")
	}
	plugins, err := filepath.Abs("../../examples/plugins")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", plugins+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := syncRepo(t, "  example-python:\n    instances:\n      home: {region: example-region}\n")
	var out, stderr bytes.Buffer
	if code := run([]string{"sync", "example-python", "--dir", dir, "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s %s", code, &out, &stderr)
	}
	data, err := os.ReadFile(filepath.Join(dir, "state", "example-python", "home.yaml"))
	if err != nil || !bytes.Contains(data, []byte("example.net")) || !bytes.Contains(data, []byte("example-region")) {
		t.Fatalf("Python result: %s %v", data, err)
	}
}
