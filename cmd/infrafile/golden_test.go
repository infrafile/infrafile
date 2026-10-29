package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/testutil"
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
		{"capabilities", []string{"capabilities"}, nil},
		{"capabilities-json", []string{"capabilities", "--json"}, nil},
		{"doctor", []string{"doctor"}, emptyRepo},
		{"doctor-json", []string{"doctor", "--json"}, emptyRepo},
		{"doctor-example-json", []string{"doctor", "--json"}, goldenSync},
		{"agent-guide", []string{"agent", "guide"}, nil},
		{"agent-guide-json", []string{"agent", "guide", "--json"}, nil},
		{"agent-install", []string{"agent", "install", "codex"}, tempDir},
		{"agent-install-codex-json", []string{"agent", "install", "codex", "--json"}, tempDir},
		{"agent-install-claude-json", []string{"agent", "install", "claude", "--json"}, tempDir},
		{"agent-install-repeat-json", []string{"agent", "install", "codex", "--json"}, func(t *testing.T) { tempDir(t); importCLI(t, 0, "agent", "install", "codex") }},

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

		{"show-web1", []string{"show", "web1", homelab}, nil},
		{"show-web1-json", []string{"show", "web1", homelab, "--json"}, nil},
		{"show-app-json", []string{"show", "App/blog", homelab, "--json"}, nil},
		{"show-archived", []string{"show", "old-vps", homelab}, nil},
		{"show-waivers", []string{"show", "example.net", homelab}, nil},
		{"show-archived-json", []string{"show", "old-vps", homelab, "--json"}, nil},
		{"show-ambiguous", []string{"show", "blog", homelab}, nil},
		{"show-not-found-json", []string{"show", "missing", homelab, "--json"}, nil},
		{"expiry", []string{"expiry", homelab}, nil},
		{"expiry-json", []string{"expiry", homelab, "--json"}, nil},
		{"expiry-zero-json", []string{"expiry", homelab, "--within", "0d", "--json"}, nil},
		{"expiry-empty", []string{"expiry"}, emptyRepo},
		{"expiry-empty-json", []string{"expiry", "--json"}, emptyRepo},
		{"ssh-config", []string{"ssh-config", homelab}, nil},
		{"ssh-config-json", []string{"ssh-config", homelab, "--json"}, nil},
		{"ssh-config-empty", []string{"ssh-config"}, emptyRepo},
		{"ssh-config-empty-json", []string{"ssh-config", "--json"}, emptyRepo},

		{"secret-check", []string{"secret", "check"}, secretRepo},
		{"secret-check-json", []string{"secret", "check", "--json"}, secretRepo},
		{"secret-check-missing", []string{"secret", "check"}, missingSecretRepo},
		{"secret-check-missing-json", []string{"secret", "check", "--json"}, missingSecretRepo},
		{"run", []string{"run", "--secret", "INJECTED=env://INFRAFILE_GOLDEN_VALUE", "--", "infrafile-test-child", "-test.run=^TestRunSecretChild$"}, goldenRun},
		{"run-json", []string{"run", "--json", "--secret", "INJECTED=env://INFRAFILE_GOLDEN_VALUE", "--", "infrafile-test-child", "-test.run=^TestRunSecretChild$"}, goldenRun},
		{"run-exit-two-json", []string{"run", "--json", "--secret", "INJECTED=env://INFRAFILE_GOLDEN_VALUE", "--", "infrafile-test-child", "-test.run=^TestRunSecretChild$"}, func(t *testing.T) { goldenRun(t); t.Setenv("INFRAFILE_CHILD_EXIT", "2") }},

		{"sync", []string{"sync"}, goldenSync},
		{"sync-json", []string{"sync", "--json"}, goldenSync},
		{"sync-unchanged-json", []string{"sync", "--json"}, goldenSyncUnchanged},
		{"sync-empty-json", []string{"sync", "--json"}, emptyRepo},
		{"sync-partial", []string{"sync"}, goldenSyncPartial},
		{"sync-partial-json", []string{"sync", "--json"}, goldenSyncPartial},
		{"sync-first-party", []string{"sync"}, firstPartyRepo},
		{"sync-first-party-json", []string{"sync", "--json"}, firstPartyRepo},
		{"import-ssh", []string{"import", "ssh-config", "--file", "imports/ssh.conf"}, goldenImport},
		{"import-ssh-json", []string{"import", "ssh-config", "--file", "imports/ssh.conf", "--json"}, goldenImport},
		{"import-state", []string{"import", "state"}, goldenImport},
		{"import-state-json", []string{"import", "state", "--json"}, goldenImport},
		{"import-state-write-json", []string{"import", "state", "--write", "--json"}, goldenImport},
		{"import-state-repeat-json", []string{"import", "state", "--write", "--json"}, goldenImportWritten},
		{"plan", []string{"plan", "cdn-pause", "Resource/edge", "-o", "plan.json"}, controlRepo},
		{"plan-json", []string{"plan", "cdn-pause", "Resource/edge", "--json"}, controlRepo},
		{"plan-show", []string{"plan", "show", "plan.json"}, goldenPlan},
		{"plan-show-json", []string{"plan", "show", "plan.json", "--json"}, goldenPlan},
		{"apply", []string{"apply", "plan.json"}, goldenPlan},
		{"apply-json", []string{"apply", "plan.json", "--json"}, goldenPlan},
		{"apply-stale-json", []string{"apply", "plan.json", "--json"}, func(t *testing.T) { goldenPlan(t); writeImportFile(t, "changed", "external-change") }},
		{"apply-mismatch-json", []string{"apply", "plan.json", "--json"}, func(t *testing.T) { goldenPlan(t); t.Setenv("INFRAFILE_CONTROL_CASE", "mismatch") }},
		{"dnscontrol", []string{"dnscontrol", homelab}, nil},
		{"dnscontrol-json", []string{"dnscontrol", homelab, "--json"}, nil},
		{"dnscontrol-empty-json", []string{"dnscontrol", "--json"}, emptyRepo},
		{"gatus", []string{"gatus", homelab}, nil},
		{"gatus-json", []string{"gatus", homelab, "--json"}, nil},
		{"gatus-empty-json", []string{"gatus", "--json"}, emptyRepo},

		{"audit", []string{"audit", homelab}, nil},
		{"audit-origin", []string{"audit", homelab, "--rule", "origin_exposure"}, nil},
		{"audit-origin-json", []string{"audit", homelab, "--rule", "origin_exposure", "--json"}, nil},
		{"audit-origin-unwaived-json", []string{"audit", "--rule", "origin_exposure", "--json"}, func(t *testing.T) {
			goldenImport(t)
			writeImportFile(t, "inventory/domains/example.net.yaml", originUnwaivedDomain)
		}},
		{"audit-lifecycle", []string{"audit", homelab + "/lifecycle"}, nil},
		{"audit-lifecycle-json", []string{"audit", homelab + "/lifecycle", "--json"}, nil},
		{"audit-orphan-json", []string{"audit", homelab + "/lifecycle", "--rule", "orphan", "--json"}, nil},
		{"audit-zombie-json", []string{"audit", homelab + "/lifecycle", "--rule", "zombie", "--json"}, nil},
		{"audit-json", []string{"audit", homelab, "--json"}, nil},
		{"audit-stale", []string{"audit", homelab, "--rule", "stale_state", "--max-age", "45d"}, nil},
		{"audit-stale-json", []string{"audit", homelab, "--rule", "stale_state", "--max-age", "45d", "--json"}, nil},
		{"audit-ignored", []string{"audit", "--rule", "stale_state", "--max-age", "45d"}, auditIgnore(ignoreCloudflare)},
		{"audit-ignored-json", []string{"audit", "--rule", "stale_state", "--max-age", "45d", "--json"}, auditIgnore(ignoreCloudflare)},
		{"audit-partial", []string{"audit", "--rule", "stale_state", "--max-age", "7d"}, auditIgnore(ignoreCloudflare)},
		{"audit-waiver-notices-json", []string{"audit", "--rule", "stale_state", "--max-age", "45d", "--json"}, auditIgnore(ignoreExpired + ignoreUnused)},
		{"audit-no-state-json", []string{"audit", "--max-age", "7d", "--json"}, emptyRepo},
		{"audit-no-sync-json", []string{"audit", "--max-age", "45d", "--json"}, auditState("_sync.yaml", "")},
		{"audit-bad-sync-json", []string{"audit", "--max-age", "45d", "--json"}, auditState("_sync.yaml", "a: [\n")},
		{"audit-bad-snapshot-json", []string{"audit", "--json"}, auditState("aliyun/home.yaml", "a: [\n")},
		{"audit-locked-json", []string{"audit", "--json"}, auditState(".sync.lock", "x")},
		{"audit-disabled-broken-json", []string{"audit", "--json"}, auditState("dns/blog.yaml", "a: [\n")},
		{"audit-cdn", []string{"audit", "--rule", "cdn_drift"}, goldenCDN},
		{"audit-cdn-json", []string{"audit", "--rule", "cdn_drift", "--json"}, goldenCDN},
		{"audit-cdn-origin-json", []string{"audit", "--rule", "cdn_drift", "--max-age", "7d", "--json"}, goldenCDNOrigin("drift")},
		{"audit-cdn-origin-equal-json", []string{"audit", "--rule", "cdn_drift", "--max-age", "7d", "--json"}, goldenCDNOrigin("equal")},
		{"audit-cdn-origin-unknown-json", []string{"audit", "--rule", "cdn_drift", "--max-age", "7d", "--json"}, goldenCDNOrigin("unknown")},
		{"audit-missing", []string{"audit", "--rule", "missing", "--max-age", "7d"}, goldenMissing("missing")},
		{"audit-missing-json", []string{"audit", "--rule", "missing", "--max-age", "7d", "--json"}, goldenMissing("missing")},
		{"audit-missing-unknown-json", []string{"audit", "--rule", "missing", "--max-age", "7d", "--json"}, goldenMissing("unknown")},
		{"audit-missing-waived-json", []string{"audit", "--rule", "missing", "--max-age", "7d", "--json"}, goldenMissing("waived")},
		{"audit-missing-needs-max-age", []string{"audit", "--rule", "missing"}, nil},
		{"audit-rule-unknown", []string{"audit", homelab, "--rule", "nope"}, nil},
		{"audit-rule-needs-max-age", []string{"audit", homelab, "--rule", "stale_state"}, nil},
		{"audit-bad-max-age", []string{"audit", homelab, "--max-age", "7"}, nil},
		{"audit-invalid-repo-json", []string{"audit", "testdata/fixtures/broken-ref", "--max-age", "7d", "--json"}, nil},
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
			code := runAt(tt.args, &stdout, &stderr, time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC))
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

func goldenPlan(t *testing.T) {
	controlRepo(t)
	var out, errout bytes.Buffer
	if code := runAt([]string{"plan", "cdn-pause", "Resource/edge", "-o", "plan.json"}, &out, &errout, time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)); code != 0 {
		t.Fatalf("prepare plan: %d %s", code, &errout)
	}
}

func goldenImport(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

func goldenImportWritten(t *testing.T) {
	goldenImport(t)
	importCLI(t, 0, "import", "state", "--write")
}

const (
	ignoreCloudflare = "  - rule: stale_state\n    plugin: cloudflare\n    instance: home\n    reason: 每季度手动同步一次\n    until: 2026-12-31\n"
	ignoreExpired    = "  - rule: stale_state\n    plugin: cloudflare\n    instance: home\n    reason: 迁移期间暂停同步\n    until: 2026-11-30\n"
	ignoreUnused     = "  - rule: stale_state\n    plugin: example\n    instance: home\n    reason: 示例插件不做审计\n"
)

// auditIgnore 在 homelab 副本的 infrafile.yaml 末尾追加 audit.ignore。
func auditIgnore(entries string) func(*testing.T) {
	return func(t *testing.T) {
		goldenImport(t)
		config, err := os.ReadFile("infrafile.yaml")
		if err != nil {
			t.Fatal(err)
		}
		writeImportFile(t, "infrafile.yaml", string(config)+"audit:\n  ignore:\n"+entries)
	}
}

// auditState 改写 homelab 副本 state/ 下的文件；content 为空时删除该文件。
func auditState(rel, content string) func(*testing.T) {
	return func(t *testing.T) {
		goldenImport(t)
		path := filepath.Join("state", filepath.FromSlash(rel))
		if content == "" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			return
		}
		writeImportFile(t, path, content)
	}
}

func goldenSync(t *testing.T) {
	t.Chdir(syncRepo(t, "  example:\n    instances:\n      home: {}\n"))
}

func goldenSyncUnchanged(t *testing.T) {
	goldenSync(t)
	var out, stderr bytes.Buffer
	if code := run([]string{"sync"}, &out, &stderr); code != 0 {
		t.Fatalf("seed exit %d: %s %s", code, &out, &stderr)
	}
}

func goldenSyncPartial(t *testing.T) {
	fixturePlugin(t)
	t.Setenv("INFRAFILE_PLUGIN_MODE", "sensitive")
	t.Chdir(syncRepo(t, twoPlugins))
}

func tempDir(t *testing.T) {
	t.Chdir(t.TempDir())
}

func emptyRepo(t *testing.T) {
	tempDir(t)
	if err := os.WriteFile("infrafile.yaml", []byte("apiVersion: infrafile/v1alpha1\nkind: Config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func secretRepo(t *testing.T) {
	emptyRepo(t)
	testutil.IsolateKeys(t)
	testutil.FakeOp(t)
	testutil.EncryptedFile(t, ".")
	t.Setenv("INFRAFILE_GOLDEN_VALUE", "synthetic-golden-value")
	if err := os.MkdirAll("inventory/accounts", 0o755); err != nil {
		t.Fatal(err)
	}
	data := `apiVersion: infrafile/v1alpha1
kind: Account
metadata:
  name: example
spec:
  provider: example
  secrets:
    env: env://INFRAFILE_GOLDEN_VALUE
    op: op://Test/Item/password
    sops: sops://secrets.enc.yaml#database.password
`
	if err := os.WriteFile("inventory/accounts/example.yaml", []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func missingSecretRepo(t *testing.T) {
	secretRepo(t)
	if err := os.Unsetenv("INFRAFILE_GOLDEN_VALUE"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove("secrets.enc.yaml"); err != nil {
		t.Fatal(err)
	}
}

func goldenRun(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, "infrafile-test-child")); err != nil {
		t.Skip("系统不支持测试命令符号链接")
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INFRAFILE_TEST_CHILD", "1")
	t.Setenv("INFRAFILE_GOLDEN_CHILD", "1")
	t.Setenv("INFRAFILE_CHILD_EXIT", "0")
	value := "synthetic-golden-value"
	t.Setenv("INFRAFILE_GOLDEN_VALUE", value)
	digest := sha256.Sum256([]byte(value))
	t.Setenv("INFRAFILE_EXPECT_SHA", hex.EncodeToString(digest[:]))
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
