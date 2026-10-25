package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func TestShowRelations(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"show", "web1", homelab, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("退出码 = %d，stderr = %s", code, &stderr)
	}
	var got struct {
		Record struct {
			Kind string `json:"kind"`
		} `json:"record"`
		Incoming []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"incoming"`
		Outgoing []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"outgoing"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Record.Kind != "Server" {
		t.Fatalf("record.kind = %s", got.Record.Kind)
	}
	var incoming, outgoing []string
	for _, e := range got.Incoming {
		incoming = append(incoming, e.Kind+"/"+e.Name+":"+e.Path)
	}
	for _, e := range got.Outgoing {
		outgoing = append(outgoing, e.Kind+"/"+e.Name+":"+e.Path)
	}
	wantIncoming := []string{
		"App/blog:spec.deployments[0].server",
		"App/wiki:spec.deployments[0].server",
		"Domain/example.com:spec.bindings[0].server",
		"Domain/example.com:spec.bindings[1].server",
		"Domain/example.net:spec.bindings[0].server",
		"Repo/blog:spec.deploys_to[0].server",
		"Repo/wiki:spec.deploys_to[0].server",
	}
	if !reflect.DeepEqual(incoming, wantIncoming) {
		t.Errorf("入边 = %v，期望 %v", incoming, wantIncoming)
	}
	if want := []string{"Account/example-cloud-main:spec.account"}; !reflect.DeepEqual(outgoing, want) {
		t.Errorf("出边 = %v，期望 %v", outgoing, want)
	}
}

func TestExpiryBoundaries(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "inventory/servers/web1.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.ReplaceAll(string(data), "2027-03-01", "2026-12-01")), 0o644); err != nil {
		t.Fatal(err)
	}
	// 用跨 UTC 日期的时刻，确认按调用方的本地日历日计算。
	now := time.Date(2026, 12, 1, 1, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	var stdout, stderr bytes.Buffer
	if code := runAt([]string{"expiry", dir, "--json"}, &stdout, &stderr, now); code != 1 {
		t.Fatalf("退出码 = %d，stderr = %s", code, &stderr)
	}
	var got struct {
		AsOf    string `json:"as_of"`
		Entries []struct {
			Name     string `json:"name"`
			DaysLeft int    `json:"days_left"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AsOf != "2026-12-01" || len(got.Entries) != 3 {
		t.Fatalf("结果 = %s", &stdout)
	}
	for i, want := range []struct {
		name string
		days int
	}{{"assets-cdn", -1}, {"web1", 0}, {"example.net", 30}} {
		if got.Entries[i].Name != want.name || got.Entries[i].DaysLeft != want.days {
			t.Errorf("第 %d 项 = %+v，期望 %+v", i, got.Entries[i], want)
		}
	}
}

func TestQueryErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"缺少名称", []string{"show"}, "需要记录名称"},
		{"名称歧义", []string{"show", "blog", homelab}, "App/blog、Repo/blog"},
		{"未找到", []string{"show", "missing", homelab}, "找不到记录"},
		{"目录不存在", []string{"show", "web1", "testdata"}, "不是数据仓"},
		{"无效阈值", []string{"expiry", homelab, "--within", "1h"}, "--within"},
		{"负数阈值", []string{"expiry", homelab, "--within=-1d"}, "--within"},
		{"溢出阈值", []string{"expiry", homelab, "--within", "999999999999999999999999d"}, "--within"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, jsonMode := range []bool{false, true} {
				args := append([]string(nil), tt.args...)
				if jsonMode {
					args = append(args, "--json")
				}
				var stdout, stderr bytes.Buffer
				code := run(args, &stdout, &stderr)
				if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tt.want) {
					t.Fatalf("json=%v: 退出码 = %d，stdout = %s，stderr = %s", jsonMode, code, &stdout, &stderr)
				}
			}
		})
	}
}

func TestQueriesRejectInvalidLedger(t *testing.T) {
	for _, fixture := range []string{"broken-ref", "plaintext"} {
		dir := "testdata/fixtures/" + fixture
		for _, args := range [][]string{{"show", "example-cloud-main", dir}, {"expiry", dir}, {"ssh-config", dir}} {
			for _, jsonMode := range []bool{false, true} {
				callArgs := append([]string(nil), args...)
				if jsonMode {
					callArgs = append(callArgs, "--json")
				}
				var stdout, stderr bytes.Buffer
				code := run(callArgs, &stdout, &stderr)
				if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "请先运行 infrafile validate") {
					t.Errorf("%v: 退出码 = %d，stdout = %s，stderr = %s", callArgs, code, &stdout, &stderr)
				}
			}
		}
	}
}

func TestSSHConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"ssh-config", homelab}, &stdout, &stderr); code != 0 {
		t.Fatalf("退出码 = %d，stderr = %s", code, &stderr)
	}
	want := "Host db1\n  HostName 198.51.100.20\n  User admin\n  Port 2222\n\nHost web1\n  HostName 203.0.113.10\n  User deploy\n  Port 22\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q，期望 %q", stdout.String(), want)
	}
}

func TestSSHConfigInputs(t *testing.T) {
	for _, tt := range []struct {
		name     string
		address  string
		ssh      map[string]any
		wantCode int
		want     string
	}{
		{"主机换行", "203.0.113.10\nProxyCommand false", nil, 2, "spec.address"},
		{"主机通配符", "*", nil, 2, "spec.address"},
		{"主机展开", "%h", nil, 2, "spec.address"},
		{"用户换行", "203.0.113.10", map[string]any{"user": "root\nProxyCommand false"}, 2, "spec.ssh.user"},
		{"用户引号", "203.0.113.10", map[string]any{"user": "\"root\""}, 2, "spec.ssh.user"},
		{"空用户", "203.0.113.10", map[string]any{"user": ""}, 2, "spec.ssh.user"},
		{"IPv6 和 SSH 默认值", "2001:db8::1", nil, 0, "Host web1\n  HostName 2001:db8::1\n"},
		{"整数形式的浮点端口", "host.example.com", map[string]any{"port": 2222.0}, 0, "Host web1\n  HostName host.example.com\n  Port 2222\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "inventory/servers"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "infrafile.yaml"), []byte("apiVersion: infrafile/v1alpha1\nkind: Config\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			spec := map[string]any{"address": tt.address}
			if tt.ssh != nil {
				spec["ssh"] = tt.ssh
			}
			data, err := yaml.Marshal(map[string]any{"apiVersion": "infrafile/v1alpha1", "kind": "Server", "metadata": map[string]any{"name": "web1"}, "spec": spec})
			if err != nil {
				t.Fatal(err)
			}
			// 保留合法 JSON Schema integer 的 YAML 浮点写法。
			data = bytes.ReplaceAll(data, []byte("port: 2222\n"), []byte("port: 2222.0\n"))
			if err := os.WriteFile(filepath.Join(dir, "inventory/servers/web1.yaml"), data, 0o644); err != nil {
				t.Fatal(err)
			}
			for _, jsonMode := range []bool{false, true} {
				args := []string{"ssh-config", dir}
				if jsonMode {
					args = append(args, "--json")
				}
				var stdout, stderr bytes.Buffer
				code := run(args, &stdout, &stderr)
				if code != tt.wantCode {
					t.Fatalf("json=%v: 退出码 = %d，stdout = %s，stderr = %s", jsonMode, code, &stdout, &stderr)
				}
				if code == 2 {
					if stdout.Len() != 0 || !strings.Contains(stderr.String(), tt.want) {
						t.Fatalf("stdout = %s，stderr = %s", &stdout, &stderr)
					}
				} else if !jsonMode && stdout.String() != tt.want {
					t.Errorf("stdout = %q，期望 %q", &stdout, tt.want)
				}
			}
		})
	}
}
