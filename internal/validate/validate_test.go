package validate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/ledger"
)

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo 建一个只有一台服务器 web1 的合法数据仓，files 追加或覆盖文件。
func newRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\n")
	write(t, dir, "inventory/servers/web1.yaml", `apiVersion: infrafile/v1alpha1
kind: Server
metadata:
  name: web1
spec:
  address: 192.0.2.10
`)
	for rel, content := range files {
		write(t, dir, rel, content)
	}
	return dir
}

func run(t *testing.T, dir string, opts Options) []ledger.Problem {
	t.Helper()
	res, err := Run(context.Background(), dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	return res.Problems
}

// want 检查问题列表恰好是 want，每项写成 "code file:line path"。
func want(t *testing.T, got []ledger.Problem, want ...string) {
	t.Helper()
	var keys []string
	for _, p := range got {
		keys = append(keys, keyOf(p))
	}
	if strings.Join(keys, "\n") != strings.Join(want, "\n") {
		t.Errorf("问题 =\n%s\n想要\n%s\n完整内容：%+v", strings.Join(keys, "\n"), strings.Join(want, "\n"), got)
	}
}

func keyOf(p ledger.Problem) string {
	return p.Code + " " + p.File + ":" + strconv.Itoa(p.Line) + " " + p.Path
}

func TestValid(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"inventory/accounts/main.yaml": `apiVersion: infrafile/v1alpha1
kind: Account
metadata:
  name: main
  labels:
    team/infra: "yes"
spec:
  provider: example-cloud
  console_url: https://console.example.com/
  x-note: 自定义字段
  secrets:
    api_token: sops://secrets.enc.yaml#accounts.main.api_token
`,
		"inventory/domains/example.com.yaml": `apiVersion: infrafile/v1alpha1
kind: Domain
metadata:
  name: example.com
spec:
  account: main
  expires_at: 2027-03-01
  bindings:
    - name: "@"
      server: web1
`,
	})
	res, err := Run(context.Background(), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != 3 || len(res.Problems) != 0 {
		t.Fatalf("Records = %d，Problems = %+v", res.Records, res.Problems)
	}
}

func TestSchemaMessages(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"inventory/servers/web2.yaml": `apiVersion: infrafile/v1
kind: Server
metadata:
  name: web2
  labels:
    "bad label": x
spec:
  addr: 192.0.2.11
  ssh:
    port: 70000
  expires_at: next year
  secrets:
    root: hunter2
`,
	})
	got := run(t, dir, Options{})
	want(t, got,
		"schema inventory/servers/web2.yaml:1 apiVersion",
		"schema inventory/servers/web2.yaml:6 metadata.labels[\"bad label\"]",
		"schema inventory/servers/web2.yaml:7 spec.address",
		"schema inventory/servers/web2.yaml:8 spec.addr",
		"schema inventory/servers/web2.yaml:10 spec.ssh.port",
		"schema inventory/servers/web2.yaml:11 spec.expires_at",
		"plaintext inventory/servers/web2.yaml:13 spec.secrets.root",
	)
	msgs := map[string]string{}
	for _, p := range got {
		msgs[p.Path] = p.Message
		if strings.Contains(p.Message, "hunter2") {
			t.Errorf("消息回显了明文：%s", p.Message)
		}
	}
	for path, msg := range map[string]string{
		"apiVersion":                   `应为 "infrafile/v1alpha1"`,
		"spec.address":                 "缺少必填字段",
		`metadata.labels["bad label"]`: "标签键名不合法：只能用字母、数字和 . _ / -，首尾是字母或数字",
		"spec.addr":                    "未知字段（自定义字段请加 x- 前缀）",
		"spec.ssh.port":                "不能大于 65535",
		"spec.expires_at":              "应为日期（YYYY-MM-DD）",
		"spec.secrets.root":            "应为凭据引用（sops://、op:// 或 env://），不要写明文",
	} {
		if msgs[path] != msg {
			t.Errorf("%s 的消息 = %q，想要 %q", path, msgs[path], msg)
		}
	}
}

func TestName(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"inventory/servers/web2.yaml": `apiVersion: infrafile/v1alpha1
kind: Server
metadata:
  name: web3
spec:
  address: 192.0.2.11
`,
		"inventory/servers/Web4.yaml": `apiVersion: infrafile/v1alpha1
kind: Server
metadata:
  name: Web4
spec:
  address: 192.0.2.12
`,
	})
	got := run(t, dir, Options{})
	want(t, got,
		"schema inventory/servers/Web4.yaml:4 metadata.name",
		"name inventory/servers/web2.yaml:4 metadata.name",
	)
}

func TestRefs(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"inventory/servers/old.yaml": `apiVersion: infrafile/v1alpha1
kind: Server
metadata:
  name: old
  archived:
    date: 2026-06-01
    reason: 迁移到 web1
spec:
  address: 192.0.2.9
`,
		"inventory/apps/blog.yaml": `apiVersion: infrafile/v1alpha1
kind: App
metadata:
  name: blog
spec:
  deployments:
    - server: web1
    - server: old
    - server: web9
  resources: [db9]
`,
		// 已归档的记录引用已归档的记录不算问题
		"inventory/apps/legacy.yaml": `apiVersion: infrafile/v1alpha1
kind: App
metadata:
  name: legacy
  archived:
    date: 2026-06-01
    reason: 不再维护
spec:
  deployments:
    - server: old
`,
	})
	got := run(t, dir, Options{})
	want(t, got,
		"ref-archived inventory/apps/blog.yaml:8 spec.deployments[1].server",
		"ref inventory/apps/blog.yaml:9 spec.deployments[2].server",
		"ref inventory/apps/blog.yaml:10 spec.resources[0]",
	)
}

// 引用后面接明文不能因为前缀合法就通过；op 名称本身允许空格。
func TestSecretRefWithTrailingPlaintext(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"inventory/accounts/main.yaml": `apiVersion: infrafile/v1alpha1
kind: Account
metadata:
  name: main
spec:
  provider: example-cloud
  x-admin-token: env://ADMIN_TOKEN hunter2
  secrets:
    api_token: env://CLOUDFLARE_API_TOKEN hunter2
    deploy_key: sops://secrets.enc.yaml#deploy.key ghp_abcdefghijklmnopqrstuvwxyz0123456789
    console: op://Infra/Cloud Console/password
    ok_sops: sops://secrets.enc.yaml#accounts.main.api_token
`,
	})
	got := run(t, dir, Options{})
	want(t, got,
		"plaintext inventory/accounts/main.yaml:7 spec.x-admin-token",
		"plaintext inventory/accounts/main.yaml:9 spec.secrets.api_token",
		"plaintext inventory/accounts/main.yaml:10 spec.secrets.deploy_key",
	)
	for _, p := range got {
		if strings.Contains(p.Message, "hunter2") || strings.Contains(p.Message, "ghp_") {
			t.Errorf("消息回显了明文：%s", p.Message)
		}
	}
}

func TestSecretRefRejectsSpacesAndControls(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"inventory/accounts/main.yaml": `apiVersion: infrafile/v1alpha1
kind: Account
metadata:
  name: main
spec:
  provider: example-cloud
  secrets:
    sops_space: sops://secrets.enc.yaml#deploy.key hunter2
    sops_file_space: sops://my secrets.enc.yaml#deploy.key
    op_del: "op://Infra/Item\x7f/password"
    op_c1: "op://Infra/Item\u0085/password"
    sops_c1: "sops://secrets.enc.yaml#deploy\u0085key"
    console: op://Infra/Cloud Console/password
    escaped: sops://my%20secrets.enc.yaml#deploy.key
`,
	})
	want(t, run(t, dir, Options{}),
		"plaintext inventory/accounts/main.yaml:8 spec.secrets.sops_space",
		"plaintext inventory/accounts/main.yaml:9 spec.secrets.sops_file_space",
		"plaintext inventory/accounts/main.yaml:10 spec.secrets.op_del",
		"plaintext inventory/accounts/main.yaml:11 spec.secrets.op_c1",
		"plaintext inventory/accounts/main.yaml:12 spec.secrets.sops_c1",
	)
}

func TestPlaintext(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"inventory/repos/blog.yaml": `apiVersion: infrafile/v1alpha1
kind: Repo
metadata:
  name: blog
  description: "部署密钥 AKIAIOSFODNN7EXAMPLE"
spec:
  remote: https://deploy:example-password@git.example.com/blog.git
  x-api-token: not-a-reference
  x-db-password: 123456
  x-admin-token: env://ADMIN_TOKEN
  x-notes:
    - ssh://git@git.example.com:2222/blog.git
`,
	})
	got := run(t, dir, Options{})
	want(t, got,
		"plaintext inventory/repos/blog.yaml:5 metadata.description",
		"plaintext inventory/repos/blog.yaml:7 spec.remote",
		"plaintext inventory/repos/blog.yaml:8 spec.x-api-token",
		"plaintext inventory/repos/blog.yaml:9 spec.x-db-password",
	)
	for _, p := range got {
		if strings.Contains(p.Message, "AKIA") || strings.Contains(p.Message, "example-password") {
			t.Errorf("消息回显了明文：%s", p.Message)
		}
	}
}

func TestPlaintextMapKey(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"inventory/repos/blog.yaml": `apiVersion: infrafile/v1alpha1
kind: Repo
metadata:
  name: blog
spec:
  remote: https://git.example.com/blog.git
  x-notes:
    AKIAIOSFODNN7EXAMPLE: backup
`,
	})
	got := run(t, dir, Options{})
	want(t, got, "plaintext inventory/repos/blog.yaml:8 spec.x-notes")
	for _, p := range got {
		if strings.Contains(p.Message+p.Path, "AKIA") {
			t.Errorf("问题回显了明文：%+v", p)
		}
	}
}

func TestTokenPatterns(t *testing.T) {
	// 都是按格式拼出来的假值。
	for _, s := range []string{
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"ghp_" + strings.Repeat("EXAMPLE0", 5),
		"github_pat_" + strings.Repeat("EXAMPLE_", 4),
		"glpat-" + strings.Repeat("EXAMPLE", 3),
		"xoxb-0000000000-EXAMPLE",
		"sk-proj-" + strings.Repeat("EXAMPLE0", 5),
		"AIza" + strings.Repeat("EXAMPLE", 5),
		"LTAI" + strings.Repeat("EXAMPLE0", 2),
		"AKID" + strings.Repeat("EXAMPLE0", 4),
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJleGFtcGxlIn0.c2lnbmF0dXJl",
	} {
		matched := false
		for _, p := range tokenPatterns {
			if p.re.MatchString(s) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("%q 应被识别为凭据", s)
		}
	}
	for _, s := range []string{
		"192.0.2.10", "https://example.com:8443/a@b", "git@git.example.com:example/blog.git",
		"sk-short", "example-cloud-main",
	} {
		for _, p := range tokenPatterns {
			if p.re.MatchString(s) {
				t.Errorf("%q 不应被识别为凭据（%s）", s, p.label)
			}
		}
	}
}

func TestDeleted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "ledger") // 数据仓在 git 仓库的子目录里
	write(t, dir, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\n")
	server := func(name string) string {
		return "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata:\n  name: " + name + "\nspec:\n  address: 192.0.2.10\n"
	}
	write(t, dir, "inventory/servers/web1.yaml", server("web1"))
	write(t, dir, "inventory/servers/web2.yaml", server("web2"))
	write(t, dir, "inventory/servers/.gitkeep", "")
	write(t, root, "inventory/servers/outside.yaml", server("outside"))
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "base")
	if err := os.Remove(filepath.Join(dir, "inventory/servers/web2.yaml")); err != nil {
		t.Fatal(err)
	}

	got := run(t, dir, Options{Base: "HEAD"})
	want(t, got, "deleted inventory/servers/web2.yaml:0 ")
	if !strings.Contains(got[0].Message, "metadata.archived") {
		t.Errorf("消息 = %q", got[0].Message)
	}

	for _, ref := range []string{"nope", "--all"} {
		if _, err := Run(context.Background(), dir, Options{Base: ref}); err == nil {
			t.Errorf("--base %s 应报错", ref)
		}
	}
	if _, err := Run(context.Background(), newRepo(t, nil), Options{Base: "HEAD"}); err == nil {
		t.Error("不在 git 仓库里时 --base 应报错")
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v：%v\n%s", args, err, out)
	}
}
