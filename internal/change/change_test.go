package change

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
)

var testContext = context.Background()

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}
func put(t *testing.T, dir, name, value string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("inventory transactions currently require Linux/macOS")
	}
	dir := t.TempDir()
	put(t, dir, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\nstorageVersion: v1\n")
	put(t, dir, ".gitignore", "/.infrafile/\n/state/\n")
	for _, k := range ledger.Kinds {
		put(t, dir, "inventory/"+k.Dir+"/.gitkeep", "")
	}
	put(t, dir, "inventory/servers/web.yaml", "# retain before rollback\napiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: web}\nspec: {address: 192.0.2.10}\n")
	gitTest(t, dir, "init", "-b", "main")
	for k, v := range map[string]string{"user.name": "Infrafile Test", "user.email": "infrafile@example.com", "commit.gpgSign": "false", "core.hooksPath": filepath.Join(dir, ".git", "hooks"), "core.autocrlf": "false"} {
		gitTest(t, dir, "config", k, v)
	}
	gitTest(t, dir, "add", ".")
	gitTest(t, dir, "commit", "-m", "fixture")
	return dir
}
func record(kind, name string, spec map[string]any) map[string]any {
	return map[string]any{"apiVersion": ledger.APIVersion, "kind": kind, "metadata": map[string]any{"name": name}, "spec": spec}
}
func preview(t *testing.T, dir string, records ...map[string]any) *Preview {
	t.Helper()
	p, err := Prepare(testContext, dir, Request{Version: "v1", Records: records}, false)
	if err != nil || !p.OK {
		t.Fatalf("preview: %v %#v", err, p)
	}
	return p
}
func request(t *testing.T, dir string) *Preview {
	return preview(t, dir, record("Server", "web", map[string]any{"address": "192.0.2.11"}), record("App", "blog", map[string]any{"deployments": []any{map[string]any{"server": "web"}}}))
}
func outcome(t *testing.T, got Result, err error, want string) {
	t.Helper()
	if err != nil || got.Status != want {
		t.Fatalf("result %v %v; want %s", got, err, want)
	}
}

func TestCommitOnlyReviewedChanges(t *testing.T) {
	dir := fixture(t)
	p := request(t, dir)
	old := gitTest(t, dir, "rev-parse", "HEAD")
	r, err := Apply(testContext, dir, p)
	outcome(t, r, err, "committed")
	if gitTest(t, dir, "rev-parse", "HEAD^") != old || gitTest(t, dir, "status", "--porcelain") != "" {
		t.Fatal("not exactly one clean commit")
	}
	if diff := gitTest(t, dir, "diff", "--name-only", "HEAD^", "HEAD"); diff != "inventory/apps/blog.yaml\ninventory/servers/web.yaml" {
		t.Fatal(diff)
	}
	if !strings.Contains(gitTest(t, dir, "show", "-s", "--format=%B"), "Signed-off-by: Infrafile Test <infrafile@example.com>") {
		t.Fatal("missing DCO")
	}
	checked, err := validate.Run(testContext, dir, validate.Options{})
	if err != nil || len(checked.Problems) != 0 {
		t.Fatalf("post-commit validate: %v %v", checked, err)
	}
	p = preview(t, dir, p.Request.Records...)
	if len(p.Files) != 0 {
		t.Fatal("repeated semantic edit should be a no-op")
	}
	r, err = Apply(testContext, dir, p)
	outcome(t, r, err, "unchanged")
}

func TestFullBatchValidationAndArchive(t *testing.T) {
	dir := fixture(t)
	app := record("App", "blog", map[string]any{"deployments": []any{map[string]any{"server": "missing"}}})
	p, err := Prepare(testContext, dir, Request{"v1", []map[string]any{app}}, false)
	if err != nil || p.OK || len(p.Problems) == 0 {
		t.Fatalf("missing foreign key: %v %v", p, err)
	}
	app["spec"].(map[string]any)["deployments"] = []any{map[string]any{"server": "new"}}
	p = preview(t, dir, app, record("Server", "new", map[string]any{"address": "192.0.2.12"}))
	r, err := Apply(testContext, dir, p)
	outcome(t, r, err, "committed")
	archived := record("Server", "new", map[string]any{"address": "192.0.2.12"})
	archived["metadata"].(map[string]any)["archived"] = map[string]any{"date": "2026-10-29", "reason": "retired"}
	p, err = Prepare(testContext, dir, Request{"v1", []map[string]any{archived}}, false)
	if err != nil || p.OK {
		t.Fatal("active reference to archived target accepted")
	}
	app["spec"] = map[string]any{"deployments": []any{map[string]any{"server": "web"}}}
	p = preview(t, dir, archived, app)
	r, err = Apply(testContext, dir, p)
	outcome(t, r, err, "committed")
}

func TestStaleDirtyAndForgedPreview(t *testing.T) {
	for _, scenario := range []string{"dirty", "head", "forged"} {
		t.Run(scenario, func(t *testing.T) {
			dir := fixture(t)
			p := request(t, dir)
			old := gitTest(t, dir, "rev-parse", "HEAD")
			switch scenario {
			case "dirty":
				put(t, dir, "notes.txt", "external user work")
			case "head":
				put(t, dir, "notes.txt", "committed work")
				gitTest(t, dir, "add", ".")
				gitTest(t, dir, "commit", "-m", "external")
				old = gitTest(t, dir, "rev-parse", "HEAD")
			case "forged":
				p.Files[0].Path = "../outside"
			}
			r, err := Apply(testContext, dir, p)
			outcome(t, r, err, "rejected")
			if gitTest(t, dir, "rev-parse", "HEAD") != old {
				t.Fatal("rejection changed HEAD")
			}
			if _, err = os.Stat(filepath.Join(dir, ".infrafile", "active")); !os.IsNotExist(err) {
				t.Fatal("rejection started transaction")
			}
		})
	}
}

func TestRecoverEveryInterruption(t *testing.T) {
	for _, point := range []string{"prepared", "before_write_1", "written", "ready", "published", "indexed"} {
		t.Run(point, func(t *testing.T) {
			dir := fixture(t)
			p := request(t, dir)
			old := gitTest(t, dir, "rev-parse", "HEAD")
			index, _ := os.ReadFile(filepath.Join(dir, ".git", "index"))
			r, err := apply(testContext, dir, p, func(name string) error {
				if name == point {
					return errors.New("simulated interruption")
				}
				return nil
			})
			outcome(t, r, err, "recovery_required")
			if _, _, err = ledger.Load(testContext, dir); err == nil {
				t.Fatal("reader exposed active transaction")
			}
			status, err := Status(testContext, dir)
			outcome(t, status, err, "recovery_required")
			got, err := Recover(testContext, dir, r.Transaction)
			if point == "published" || point == "indexed" {
				outcome(t, got, err, "committed")
			} else {
				outcome(t, got, err, "rolled_back")
				if gitTest(t, dir, "rev-parse", "HEAD") != old {
					t.Fatal("rollback rewound or changed HEAD")
				}
				after, _ := os.ReadFile(filepath.Join(dir, ".git", "index"))
				if !bytes.Equal(index, after) {
					t.Fatal("rollback changed index")
				}
				for _, f := range p.Files {
					b, _ := os.ReadFile(filepath.Join(dir, f.Path))
					if string(b) != f.Before {
						t.Fatal("original bytes not restored")
					}
				}
			}
			if gitTest(t, dir, "status", "--porcelain") != "" {
				t.Fatal("dirty after recovery")
			}
			status, err = Status(testContext, dir)
			outcome(t, status, err, "clean")
		})
	}
}

func TestRecoveryPreservesExternalEdits(t *testing.T) {
	dir := fixture(t)
	p := request(t, dir)
	r, err := apply(testContext, dir, p, func(name string) error {
		if name == "written" {
			return errors.New("stop")
		}
		return nil
	})
	outcome(t, r, err, "recovery_required")
	put(t, dir, p.Files[1].Path, "external editor work\n")
	before, _ := os.ReadFile(filepath.Join(dir, p.Files[0].Path))
	_, err = Recover(testContext, dir, r.Transaction)
	if err == nil {
		t.Fatal("recovery accepted an external edit")
	}
	after, _ := os.ReadFile(filepath.Join(dir, p.Files[0].Path))
	if !bytes.Equal(before, after) {
		t.Fatal("batch partially rolled back before detecting external edit")
	}
	b, _ := os.ReadFile(filepath.Join(dir, p.Files[1].Path))
	if string(b) != "external editor work\n" {
		t.Fatal("external work lost")
	}
}

func TestCommitHooksAreNotBypassed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook fixture")
	}
	for _, mode := range []string{"reject", "stage_extra", "edit_worktree"} {
		t.Run(mode, func(t *testing.T) {
			dir := fixture(t)
			p := request(t, dir)
			old := gitTest(t, dir, "rev-parse", "HEAD")
			script := "#!/bin/sh\nexit 1\n"
			if mode == "stage_extra" {
				script = "#!/bin/sh\nprintf hook > hook.txt\ngit add hook.txt\n"
			}
			if mode == "edit_worktree" {
				script = "#!/bin/sh\nprintf hook > inventory/servers/web.yaml\n"
			}
			put(t, dir, ".git/hooks/pre-commit", script)
			if err := os.Chmod(filepath.Join(dir, ".git/hooks/pre-commit"), 0700); err != nil {
				t.Fatal(err)
			}
			r, err := Apply(testContext, dir, p)
			outcome(t, r, err, "recovery_required")
			if gitTest(t, dir, "rev-parse", "HEAD") != old {
				t.Fatal("hook published an unreviewed tree")
			}
			got, err := Recover(testContext, dir, r.Transaction)
			if mode == "edit_worktree" {
				if err == nil {
					t.Fatal("hook worktree edit was overwritten")
				}
			} else {
				outcome(t, got, err, "rolled_back")
			}
			if mode == "stage_extra" {
				b, _ := os.ReadFile(filepath.Join(dir, "hook.txt"))
				if string(b) != "hook" {
					t.Fatal("external hook output lost")
				}
			}
		})
	}
}

func TestConcurrentSessions(t *testing.T) {
	dir := fixture(t)
	p := request(t, dir)
	entered := make(chan struct{})
	resume := make(chan struct{})
	done := make(chan Result)
	go func() {
		r, err := apply(testContext, dir, p, func(point string) error {
			if point == "prepared" {
				close(entered)
				<-resume
			}
			return nil
		})
		if err != nil {
			r.Code = err.Error()
		}
		done <- r
	}()
	<-entered
	second, err := Apply(testContext, dir, p)
	outcome(t, second, err, "rejected")
	close(resume)
	first := <-done
	if first.Status != "committed" {
		t.Fatal(first)
	}
}

func TestBrowserPreservesSecretReferences(t *testing.T) {
	dir := fixture(t)
	put(t, dir, "inventory/accounts/home.yaml", "apiVersion: infrafile/v1alpha1\nkind: Account\nmetadata: {name: home}\nspec: {provider: example, secrets: {token: 'env://EXAMPLE_TOKEN'}}\n")
	gitTest(t, dir, "add", ".")
	gitTest(t, dir, "commit", "-m", "reference")
	rec := record("Account", "home", map[string]any{"provider": "example", "owner": "updated"})
	p, err := Prepare(testContext, dir, Request{"v1", []map[string]any{rec}}, true)
	if err != nil || !p.OK || !strings.Contains(p.Files[0].After, "env://EXAMPLE_TOKEN") {
		t.Fatal("reference lost", p, err)
	}
	rec["spec"].(map[string]any)["secrets"] = map[string]any{"token": "env://OTHER"}
	p, err = Prepare(testContext, dir, Request{"v1", []map[string]any{rec}}, true)
	if err != nil || p.OK {
		t.Fatal("browser supplied a secret field")
	}
	rec = record("Server", "web", map[string]any{"address": "192.0.2.11", "x-password": "do-not-output"})
	p, err = Prepare(testContext, dir, Request{"v1", []map[string]any{rec}}, false)
	if err != nil || p.OK {
		t.Fatal("plaintext accepted")
	}
}

func TestProcessRestartRecovery(t *testing.T) {
	if os.Getenv("INFRAFILE_CHANGE_CHILD") != "" {
		dir := os.Getenv("INFRAFILE_CHANGE_CHILD")
		p := request(t, dir)
		_, _ = apply(testContext, dir, p, func(point string) error {
			if point == "before_write_1" {
				os.Exit(19)
			}
			return nil
		})
		os.Exit(20)
	}
	dir := fixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRestartRecovery$")
	cmd.Env = append(os.Environ(), "INFRAFILE_CHANGE_CHILD="+dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("child should stop before completing")
	}
	status, err := Status(testContext, dir)
	outcome(t, status, err, "recovery_required")
	got, err := Recover(testContext, dir, status.Transaction)
	outcome(t, got, err, "rolled_back")
	if gitTest(t, dir, "status", "--porcelain") != "" {
		t.Fatal("dirty after restart recovery")
	}
}

func TestRequestStrictParsing(t *testing.T) {
	for _, data := range []string{`{"version":"v1","records":[],"extra":true}`, `{"version":"v1","records":[],"Version":"v1"}`, `{"version":"v1","version":"v1","records":[]}`, `{"version":"v2","records":[]}`, `{"version":"v1","records":null}`} {
		if _, err := DecodeRequest(testContext, []byte(data)); err == nil {
			t.Fatal("accepted", data)
		}
	}
	b, _ := json.Marshal(Request{"v1", []map[string]any{}})
	if _, err := DecodeRequest(testContext, b); err != nil {
		t.Fatal(err)
	}
}
