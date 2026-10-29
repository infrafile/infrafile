package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
)

func TestImportSSHConfigDraftAndCreateOnly(t *testing.T) {
	emptyRepo(t)
	writeImportFile(t, "ssh.conf", "Host web1 web2\n  HostName 192.0.2.10\n  User observer\n  Port 2222\n")
	out := importCLI(t, 0, "import", "ssh-config", "--file", "ssh.conf", "--json")
	var report struct {
		Records []struct {
			Status string         `json:"status"`
			Record map[string]any `json:"record"`
		} `json:"records"`
	}
	if json.Unmarshal(out, &report) != nil || len(report.Records) != 2 || report.Records[0].Status != "draft" {
		t.Fatalf("draft output: %s", out)
	}
	if _, err := os.Stat("inventory"); !os.IsNotExist(err) {
		t.Fatal("dry run changed inventory")
	}
	importCLI(t, 0, "import", "ssh-config", "--file", "ssh.conf", "--write")
	repo, problems, err := ledger.Load(context.Background(), ".")
	if err != nil || len(problems) != 0 || len(repo.Records) != 2 {
		t.Fatalf("created records: %v %v", problems, err)
	}
	importCLI(t, 0, "validate")
	want := []byte("# 手写字段应保留\napiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: web1}\nspec: {address: 198.51.100.10}\n")
	writeImportFile(t, "inventory/servers/web1.yaml", string(want))
	before, _ := os.Stat("inventory/servers/web1.yaml")
	importCLI(t, 0, "import", "ssh-config", "--file", "ssh.conf", "--write", "--json")
	after, _ := os.Stat("inventory/servers/web1.yaml")
	got, _ := os.ReadFile("inventory/servers/web1.yaml")
	if !bytes.Equal(got, want) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("reimport overwrote existing record")
	}
}

func TestImportSSHLimitDoesNotPublish(t *testing.T) {
	emptyRepo(t)
	child, err := filepath.Abs("child.conf")
	if err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, child, "Host sample\n HostName 192.0.2.1\n")
	writeImportFile(t, "ssh.conf", strings.Repeat(fmt.Sprintf("Include %q\n", child), 1024))
	for _, destination := range [][]string{{"--write"}, {"--output", "review"}, {"--draft", "review.json"}} {
		var out, stderr bytes.Buffer
		args := append([]string{"import", "ssh-config", "--file", "ssh.conf"}, destination...)
		if code := run(args, &out, &stderr); code != 2 || out.Len() != 0 || !strings.Contains(stderr.String(), "1024") {
			t.Fatalf("limit failure: exit=%d stdout=%s stderr=%s", code, &out, &stderr)
		}
		for _, path := range []string{"inventory", "review", "review.json"} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("limit failure created %s", path)
			}
		}
	}
}

func TestImportSSHIncludesDefaultsAndReviewDirectory(t *testing.T) {
	emptyRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh", "parts"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, filepath.Join(home, ".ssh", "config"), "Include parts/*.conf\nHost * !db1\n  User fallback\n  Port=2200\nHost *\n  User general\n")
	writeImportFile(t, filepath.Join(home, ".ssh", "parts", "a.conf"), "Host web1\n  HostName=192.0.2.10 # comment\n  User \"observer\"\nHost db1\n  HostName db.example.com\n")
	importCLI(t, 0, "import", "ssh-config", "--output", "review")
	for _, name := range []string{"web1", "db1"} {
		data, err := os.ReadFile("review/servers/" + name + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		if name == "web1" && (!bytes.Contains(data, []byte("observer")) || !bytes.Contains(data, []byte("2200"))) {
			t.Fatalf("first value/defaults: %s", data)
		}
		if name == "db1" && (!bytes.Contains(data, []byte("general")) || bytes.Contains(data, []byte("2200"))) {
			t.Fatalf("negation: %s", data)
		}
	}
	if _, err := os.Stat("inventory"); !os.IsNotExist(err) {
		t.Fatal("review output wrote inventory")
	}
	writeImportFile(t, "review/servers/web1.yaml", "reviewer's draft\n")
	importCLI(t, 0, "import", "ssh-config", "--output", "review")
	got, _ := os.ReadFile("review/servers/web1.yaml")
	if string(got) != "reviewer's draft\n" {
		t.Fatal("overwrote review draft")
	}
	if out := importCLI(t, 2, "import", "ssh-config", "--output", "inventory"); len(out) != 0 {
		t.Fatal("invalid output emitted data")
	}
}

func TestImportStateUsesAssetSemantics(t *testing.T) {
	emptyRepo(t)
	importSnapshot(t, "aliyun", "home", "domains: [{name: example.com, expires_at: '2030-01-01'}]\ncdn: [{name: cdn.example.com, id: edge-1}]\n")
	importSnapshot(t, "cloudflare", "home", "zones: [{id: zone-1, name: sub.example.com, dns_records: []}]\n")
	importSnapshot(t, "whois", "home", "domain: example.net\nfound: true\nregistrar: Example Registrar\nexpires_at: '2030-02-01'\n")
	importSnapshot(t, "ssh", "web1", "host: 192.0.2.1\nsystem: Linux\nrelease: 6.1-example\narchitecture: x86_64\n")
	importSnapshot(t, "dns", "blog", "host: blog.example.com\naddresses: [192.0.2.1]\n")
	importSnapshot(t, "tls", "blog", "host: blog.example.com\nverified: false\n")
	out := importCLI(t, 0, "import", "state", "--json")
	var report struct {
		Records []struct {
			Kind string `json:"kind"`
		} `json:"records"`
		Skipped []struct {
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	if json.Unmarshal(out, &report) != nil || len(report.Records) != 5 || len(report.Skipped) != 2 {
		t.Fatalf("mapped records: %s", out)
	}
	importCLI(t, 0, "import", "state", "--write")
	importCLI(t, 0, "validate")
	if _, err := os.Stat("inventory/domains/sub.example.com.yaml"); !os.IsNotExist(err) {
		t.Fatal("DNS zone became registered domain")
	}
	before, _ := os.ReadFile("inventory/domains/example.com.yaml")
	importSnapshot(t, "aliyun", "home", "domains: [{name: example.com, expires_at: '2035-01-01'}]\n")
	importCLI(t, 0, "import", "state", "--write")
	after, _ := os.ReadFile("inventory/domains/example.com.yaml")
	if !bytes.Equal(before, after) {
		t.Fatal("changed snapshot overwrote human ledger")
	}
}

func TestImportFirstPartyReplayAndRepeat(t *testing.T) {
	for _, provider := range []string{"aliyun", "tencent", "baidu", "ctyun", "cloudflare"} {
		t.Run(provider, func(t *testing.T) {
			firstPartyRepo(t)
			importCLI(t, 0, "sync", provider, "--json")
			importCLI(t, 0, "import", "state", "--write", "--json")
			importCLI(t, 0, "validate")
			repo, _, err := ledger.Load(context.Background(), ".")
			if err != nil || len(repo.Records) == 0 {
				t.Fatal("provider produced no records")
			}
			before := importFiles(t)
			importCLI(t, 0, "import", "state", "--write", "--json")
			if !reflect.DeepEqual(before, importFiles(t)) {
				t.Fatal("repeat import changed files")
			}
		})
	}
}

func TestImportEmptyStateAndArchivedRecord(t *testing.T) {
	emptyRepo(t)
	before := importFiles(t)
	importCLI(t, 0, "import", "state", "--write", "--json")
	if !reflect.DeepEqual(before, importFiles(t)) {
		t.Fatal("empty import changed files")
	}
	if err := os.MkdirAll("inventory/domains", 0o755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "inventory/domains/example.com.yaml", "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com, archived: {date: '2026-01-01', reason: retired}}\nspec: {}\n")
	importSnapshot(t, "aliyun", "home", "domains: [{name: example.com}]\n")
	before = importFiles(t)
	importCLI(t, 0, "import", "state", "--write", "--json")
	if !reflect.DeepEqual(before, importFiles(t)) {
		t.Fatal("archived record was changed or revived")
	}
}

func TestImportIncludesAreConditionalAndAliasesAreNormalized(t *testing.T) {
	emptyRepo(t)
	child := filepath.Join(t.TempDir(), "included.conf")
	writeImportFile(t, child, "User included\nHost db1\n  User never-leak\nHost hidden\n  HostName 192.0.2.99\n")
	writeImportFile(t, "ssh.conf", "Host Web_One\n HostName 192.0.2.1\n Include "+child+"\n User outer\nHost db1\n HostName 192.0.2.2\n User database\n")
	importCLI(t, 0, "import", "ssh-config", "--file", "ssh.conf", "--write")
	web, _ := os.ReadFile("inventory/servers/web-one.yaml")
	db, _ := os.ReadFile("inventory/servers/db1.yaml")
	if !bytes.Contains(web, []byte("included")) || !bytes.Contains(db, []byte("database")) || bytes.Contains(db, []byte("never-leak")) {
		t.Fatalf("include semantics: %s %s", web, db)
	}
	if _, err := os.Stat("inventory/servers/hidden.yaml"); !os.IsNotExist(err) {
		t.Fatal("inactive include invented a host")
	}
	before := importFiles(t)
	importCLI(t, 0, "import", "ssh-config", "--file", "ssh.conf", "--write")
	if !reflect.DeepEqual(before, importFiles(t)) {
		t.Fatal("repeat import changed files")
	}
}

func TestImportRejectsUnsafeSourcesAndDestinations(t *testing.T) {
	for _, scenario := range []string{"match-exec", "user-token", "include-cycle", "snapshot-secret", "duplicate-yaml", "bad-expiry", "conflict", "review-symlink", "inventory-symlink"} {
		t.Run(scenario, func(t *testing.T) {
			emptyRepo(t)
			args := []string{"import", "state", "--write", "--json"}
			switch scenario {
			case "match-exec":
				writeImportFile(t, "ssh.conf", "Host web1\n HostName 192.0.2.1\nMatch exec \"touch executed\"\n User root\n")
				args = []string{"import", "ssh-config", "--file", "ssh.conf", "--write"}
			case "user-token":
				writeImportFile(t, "ssh.conf", "Host web1\n HostName 192.0.2.1\n User %r\n")
				args = []string{"import", "ssh-config", "--file", "ssh.conf", "--write"}
			case "include-cycle":
				file, _ := filepath.Abs("ssh.conf")
				writeImportFile(t, file, "Include "+file+"\n")
				args = []string{"import", "ssh-config", "--file", file, "--write"}
			case "snapshot-secret":
				importSnapshot(t, "aliyun", "home", "domains: []\npassword: synthetic-private-value\n")
			case "duplicate-yaml":
				importSnapshot(t, "aliyun", "home", "domains: []\ndomains: []\n")
			case "bad-expiry":
				importSnapshot(t, "aliyun", "home", "domains: [{name: example.com, expires_at: wrong}]\n")
			case "conflict":
				importSnapshot(t, "aliyun", "a", "domains: [{name: example.com, expires_at: '2030-01-01'}]\n")
				importSnapshot(t, "aliyun", "b", "domains: [{name: example.com, expires_at: '2031-01-01'}]\n")
			case "review-symlink", "inventory-symlink":
				importSnapshot(t, "aliyun", "home", "domains: [{name: example.com}]\n")
				if scenario == "review-symlink" {
					os.Mkdir("inventory", 0o755)
					if err := os.Symlink("inventory", "review"); err != nil {
						t.Skip(err)
					}
					args = []string{"import", "state", "--output", "review"}
				} else {
					if err := os.Symlink(t.TempDir(), "inventory"); err != nil {
						t.Skip(err)
					}
				}
			}
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 2 || out.Len() != 0 || strings.Contains(stderr.String(), "synthetic-private-value") {
				t.Fatalf("unsafe failure: %d %s %s", code, &out, &stderr)
			}
			if _, err := os.Stat("executed"); !os.IsNotExist(err) {
				t.Fatal("Match exec was executed")
			}
		})
	}
}

func TestImportPartialWriteIsReportedAndPreserved(t *testing.T) {
	emptyRepo(t)
	importSnapshot(t, "aliyun", "home", "domains: [{name: example.com}]\ncdn: [{name: cdn.example.com}]\n")
	if err := os.Mkdir("review", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), "review/resources"); err != nil {
		t.Skip(err)
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"import", "state", "--output", "review"}, &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "已创建") || out.Len() != 0 {
		t.Fatalf("partial write not reported: exit=%d %s", code, &stderr)
	}
	if _, err := os.Stat("review/domains/example.com.yaml"); err != nil {
		t.Fatal("previously created file lost")
	}
}

func TestImportSSHBracketsAreLiteral(t *testing.T) {
	emptyRepo(t)
	writeImportFile(t, "ssh.conf", "Host web[12]\n HostName bracket.example.com\n User bracket-user\nHost web1\n HostName 192.0.2.1\n User observer\n")
	importCLI(t, 0, "import", "ssh-config", "--file", "ssh.conf", "--write")
	data, _ := os.ReadFile("inventory/servers/web1.yaml")
	if !bytes.Contains(data, []byte("observer")) || bytes.Contains(data, []byte("bracket-user")) {
		t.Fatalf("SSH bracket pattern misapplied: %s", data)
	}
	if _, err := os.Stat("inventory/servers/web-12.yaml"); err != nil {
		t.Fatal("literal bracket alias disappeared")
	}
}

func TestImportNamesFitFileSystem(t *testing.T) {
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	t.Run("resource", func(t *testing.T) {
		emptyRepo(t)
		importSnapshot(t, "aliyun", strings.Repeat("a", 64), "cdn: [{name: "+long+"}]\n")
		importCLI(t, 0, "import", "state", "--write")
		files, err := os.ReadDir("inventory/resources")
		if err != nil || len(files) != 1 || len(files[0].Name()) > 255 {
			t.Fatalf("long resource filename: %v %v", files, err)
		}
		importCLI(t, 0, "validate")
	})
	for _, source := range []string{"domain", "ssh"} {
		t.Run(source, func(t *testing.T) {
			emptyRepo(t)
			args := []string{"import", "state", "--write"}
			if source == "domain" {
				importSnapshot(t, "aliyun", "home", "domains: [{name: example.com}, {name: "+long+"}]\n")
			} else {
				writeImportFile(t, "ssh.conf", "Host "+strings.Repeat("a", 251)+"\n HostName 192.0.2.1\n")
				args = []string{"import", "ssh-config", "--file", "ssh.conf", "--write"}
			}
			importCLI(t, 2, args...)
			if _, err := os.Stat("inventory"); !os.IsNotExist(err) {
				t.Fatal("long filename was not rejected before writes")
			}
		})
	}
}

func TestImportReviewCannotUseInventoryCaseAlias(t *testing.T) {
	for _, existing := range []bool{true, false} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			emptyRepo(t)
			if err := os.Mkdir("inventory", 0o755); err != nil {
				t.Fatal(err)
			}
			lower, _ := os.Stat("inventory")
			upper, err := os.Stat("INVENTORY")
			if err != nil || !os.SameFile(lower, upper) {
				t.Skip("测试卷区分大小写")
			}
			if !existing {
				if err := os.Remove("inventory"); err != nil {
					t.Fatal(err)
				}
			}
			importSnapshot(t, "aliyun", "home", "domains: [{name: example.com}]\n")
			before := importFiles(t)
			for _, output := range []string{"INVENTORY", "INVENTORY/review"} {
				importCLI(t, 2, "import", "state", "--output", output)
			}
			if !reflect.DeepEqual(before, importFiles(t)) {
				t.Fatal("case alias bypassed --write")
			}
			if !existing {
				if _, err := os.Stat("inventory"); !os.IsNotExist(err) {
					t.Fatal("case alias created inventory")
				}
			}
		})
	}
}

type importedFile struct {
	Content  string
	Modified time.Time
}

func importFiles(t *testing.T) map[string]importedFile {
	t.Helper()
	files := map[string]importedFile{}
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files[path] = importedFile{string(data), info.ModTime()}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func importSnapshot(t *testing.T, plugin, instance, spec string) {
	t.Helper()
	dir := filepath.Join("state", plugin)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: " + instance + "}\nspec:\n"
	for _, line := range bytes.Split([]byte(spec), []byte("\n")) {
		if len(line) > 0 {
			content += "  " + string(line) + "\n"
		}
	}
	writeImportFile(t, filepath.Join(dir, instance+".yaml"), content)
}

func writeImportFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func importCLI(t *testing.T, want int, args ...string) []byte {
	t.Helper()
	var out, stderr bytes.Buffer
	if code := run(args, &out, &stderr); code != want {
		t.Fatalf("%v: exit=%d stdout=%s stderr=%s", args, code, &out, &stderr)
	}
	return out.Bytes()
}
