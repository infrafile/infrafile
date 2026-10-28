package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

const valid = "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata:\n  name: home\nspec:\n  domains: []\n"

func TestReadMissingState(t *testing.T) {
	dir := t.TempDir()
	got, err := Read(context.Background(), dir, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("Read = %v, %v", got, err)
	}
	stamps, err := ReadSync(dir)
	if err != nil || len(stamps) != 0 {
		t.Fatalf("ReadSync = %v, %v", stamps, err)
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "state/aliyun/home.yaml", valid)
	write(t, dir, "state/aliyun/.tmp", "ignored")
	write(t, dir, "state/_sync.yaml", "apiVersion: infrafile/v1alpha1\nkind: Sync\nspec: {}\n")
	got, err := Read(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Plugin != "aliyun" || got[0].Instance != "home" || got[0].File != "state/aliyun/home.yaml" {
		t.Fatalf("Read = %+v", got)
	}
	if _, ok := got[0].Spec["domains"]; !ok {
		t.Fatalf("Spec = %v", got[0].Spec)
	}
}

func TestReadSkip(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "state/aliyun/home.yaml", valid)
	write(t, dir, "state/dns/blog.yaml", "a: [\n")
	got, err := Read(context.Background(), dir, func(plugin, _ string) bool { return plugin == "dns" })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Skipped || got[0].Spec == nil || !got[1].Skipped || got[1].Spec != nil || got[1].File != "state/dns/blog.yaml" || got[1].Instance != "blog" {
		t.Fatalf("Read = %+v", got)
	}
	write(t, dir, "state/dns/blog.yml", valid)
	if _, err := Read(context.Background(), dir, func(string, string) bool { return true }); err == nil || !strings.Contains(err.Error(), ".yaml") {
		t.Fatalf("跳过的实例仍检查路径，err = %v", err)
	}
}

func TestReadRejects(t *testing.T) {
	cases := map[string]struct{ rel, content, want string }{
		"lock":     {"state/.sync.lock", "", "锁"},
		"yaml":     {"state/aliyun/home.yaml", "a: [\n", "YAML"},
		"envelope": {"state/aliyun/home.yaml", "apiVersion: infrafile/v1alpha1\nkind: Snapshot\n", "信封"},
		"name":     {"state/aliyun/other.yaml", valid, "文件名"},
		"ext":      {"state/aliyun/home.yml", valid, ".yaml"},
		"secret":   {"state/aliyun/home.yaml", strings.Replace(valid, "domains: []", "password: x", 1), "敏感"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, c.rel, c.content)
			if _, err := Read(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v，想要包含 %q", err, c.want)
			}
		})
	}
}

func TestReadSync(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "state/_sync.yaml", "apiVersion: infrafile/v1alpha1\nkind: Sync\nspec:\n  aliyun:\n    home: 2026-11-28T03:00:00Z\n")
	got, err := ReadSync(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 28, 3, 0, 0, 0, time.UTC); !got["aliyun"]["home"].Equal(want) {
		t.Fatalf("ReadSync = %v", got)
	}
}

func TestReadSyncRejects(t *testing.T) {
	cases := map[string]string{
		"yaml":    "a: [\n",
		"kind":    "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nspec: {}\n",
		"stamp":   "apiVersion: infrafile/v1alpha1\nkind: Sync\nspec:\n  aliyun:\n    home: yesterday\n",
		"unknown": "apiVersion: infrafile/v1alpha1\nkind: Sync\nspec: {}\nextra: 1\n",
		"docs":    "apiVersion: infrafile/v1alpha1\nkind: Sync\nspec: {}\n---\na: 1\n",
		"nospec":  "apiVersion: infrafile/v1alpha1\nkind: Sync\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "state/_sync.yaml", content)
			if _, err := ReadSync(dir); err == nil || !strings.Contains(err.Error(), "_sync.yaml") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("lock", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "state/.sync.lock", "")
		if _, err := ReadSync(dir); err == nil || !strings.Contains(err.Error(), "锁") {
			t.Fatalf("err = %v", err)
		}
	})
}
