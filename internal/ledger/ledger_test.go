package ledger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemasCompile(t *testing.T) {
	s, err := LoadSchemas()
	if err != nil {
		t.Fatal(err)
	}
	files, _ := SchemaFiles()
	for _, kind := range append([]string{ConfigKind}, kindNames()...) {
		if _, ok := files[SchemaFileName(kind)]; !ok {
			t.Errorf("缺少 %s", SchemaFileName(kind))
		}
	}
	got := map[string]string{}
	for _, r := range s.Refs("Domain") {
		got[strings.Join(r.Pattern, "/")] = r.Target
	}
	want := map[string]string{
		"spec/account":              "Account",
		"spec/dns_account":          "Account",
		"spec/bindings/[]/server":   "Server",
		"spec/bindings/[]/app":      "App",
		"spec/bindings/[]/resource": "Resource",
	}
	if len(got) != len(want) {
		t.Fatalf("Domain 外键 = %v，想要 %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Domain 外键 %s = %q，想要 %q", k, got[k], v)
		}
	}
}

func TestCollectRefsUnknownKind(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{
		"x": map[string]any{"x-ref": "Nope"},
	}}
	if _, err := collectRefs(schema, nil); err == nil {
		t.Fatal("x-ref 指向未知 kind 时应报错")
	}
}

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

func TestLoadNotRepo(t *testing.T) {
	_, _, err := Load(context.Background(), t.TempDir())
	if _, ok := err.(*NotRepoError); !ok {
		t.Fatalf("err = %v，想要 *NotRepoError", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\n")
	write(t, dir, "inventory/servers/web1.yaml", `apiVersion: infrafile/v1alpha1
kind: Server
metadata:
  name: web1
spec:
  address: 192.0.2.10
  ssh:
    port: 22
  expires_at: 2027-01-01
`)
	write(t, dir, "inventory/servers/bad.yaml", "a: [\n")
	write(t, dir, "inventory/servers/dup.yaml", "a: 1\na: 2\n")
	write(t, dir, "inventory/servers/wrong.yaml", "kind: App\n")
	write(t, dir, "inventory/servers/old.yml", "kind: Server\n")
	write(t, dir, "inventory/stuff/x.yaml", "")
	write(t, dir, "inventory/loose.yaml", "")
	write(t, dir, "inventory/servers/.gitkeep", "")

	repo, problems, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.Records) != 2 { // web1 和 dup（dup 只是重复键，仍可用）
		t.Fatalf("记录数 = %d，想要 2", len(repo.Records))
	}
	var web1 *Record
	for _, r := range repo.Records {
		if r.Name == "web1" {
			web1 = r
		}
	}
	if web1 == nil || web1.Stem() != "web1" || web1.Kind != "Server" {
		t.Fatalf("web1 = %+v", web1)
	}
	spec := web1.Value.(map[string]any)["spec"].(map[string]any)
	if spec["expires_at"] != "2027-01-01" {
		t.Errorf("日期应保持字符串，实际 %#v", spec["expires_at"])
	}
	if spec["ssh"].(map[string]any)["port"] != 22 {
		t.Errorf("端口应为整数，实际 %#v", spec["ssh"])
	}
	if got := web1.Line("spec.ssh.port"); got != 8 {
		t.Errorf("spec.ssh.port 行号 = %d，想要 8", got)
	}
	if len(repo.Files) != 4 {
		t.Errorf("Files = %v", repo.Files)
	}

	codes := map[string]int{}
	for _, p := range problems {
		codes[p.Code+" "+p.File]++
	}
	for _, want := range []string{
		"parse inventory/servers/bad.yaml",
		"parse inventory/servers/dup.yaml",
		"kind inventory/servers/wrong.yaml",
		"layout inventory/servers/old.yml",
		"layout inventory/stuff/",
		"layout inventory/loose.yaml",
	} {
		if codes[want] != 1 {
			t.Errorf("缺少问题 %q，全部问题：%v", want, problems)
		}
	}
	if len(problems) != 6 {
		t.Errorf("问题数 = %d：%v", len(problems), problems)
	}
}
