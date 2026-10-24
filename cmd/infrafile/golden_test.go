package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "重写 testdata/golden 下的期望输出")

const homelab = "../../examples/homelab"

// TestGolden 锁定各命令的文本和 --json 输出，--json 的结构说明见 docs/json/。
// 输出变了且符合预期时，用 go test ./cmd/infrafile -update 重写期望文件。
func TestGolden(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		setup func(t *testing.T) // 可以 t.Chdir 到临时目录
	}{
		{"version", []string{"version"}, nil},
		{"version-json", []string{"version", "--json"}, nil},

		{"validate-homelab", []string{"validate", homelab}, nil},
		{"validate-homelab-json", []string{"validate", "--json", homelab}, nil},
		{"validate-broken-ref", []string{"validate", "testdata/fixtures/broken-ref"}, nil},
		{"validate-broken-ref-json", []string{"validate", "--json", "testdata/fixtures/broken-ref"}, nil},
		{"validate-plaintext", []string{"validate", "testdata/fixtures/plaintext"}, nil},
		{"validate-plaintext-json", []string{"validate", "--json", "testdata/fixtures/plaintext"}, nil},
		{"validate-deleted", []string{"validate", "--base", "HEAD"}, deletedRepo},
		{"validate-deleted-json", []string{"validate", "--json", "--base", "HEAD"}, deletedRepo},
		{"validate-not-repo", []string{"validate", "testdata"}, nil},
		{"validate-not-repo-json", []string{"validate", "--json", "testdata"}, nil},

		{"init", []string{"init", "ledger"}, tempDir},
		{"init-json", []string{"init", "--json", "ledger"}, tempDir},
		{"init-exists", []string{"init", homelab}, nil},

		{"schema-export", []string{"schema", "export"}, nil},
		{"schema-export-dir", []string{"schema", "export", "-o", "schemas"}, tempDir},
		{"schema-export-dir-json", []string{"schema", "export", "--json", "-o", "schemas"}, tempDir},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			golden, err := filepath.Abs(filepath.Join("testdata", "golden", tt.name+".txt"))
			if err != nil {
				t.Fatal(err)
			}
			if tt.setup != nil {
				tt.setup(t)
			}
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != 2 && strings.Contains(strings.Join(tt.args, " "), "--json") && !json.Valid(stdout.Bytes()) {
				t.Errorf("--json 的 stdout 不是合法 JSON：\n%s", stdout.String())
			}
			got := fmt.Sprintf("$ infrafile %s\n[退出码 %d]\n--- stdout\n%s--- stderr\n%s",
				strings.Join(tt.args, " "), code, stdout.String(), stderr.String())

			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v（第一次运行请加 -update）", err)
			}
			if got != string(want) {
				t.Errorf("输出与 %s 不一致：\n--- 实际\n%s\n--- 期望\n%s", golden, got, want)
			}
		})
	}
}

func tempDir(t *testing.T) {
	t.Chdir(t.TempDir())
}

// deletedRepo 把 homelab 复制进临时 git 仓库并提交，再删掉 repos/wiki.yaml，工作目录切到那里。
func deletedRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "base"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v：%v\n%s", args, err, out)
		}
	}
	if err := os.Remove(filepath.Join(dir, "inventory", "repos", "wiki.yaml")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}
