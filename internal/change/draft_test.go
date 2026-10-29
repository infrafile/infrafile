package change

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/infrafile/infrafile/internal/importer"
	"github.com/infrafile/infrafile/internal/validate"
)

func TestEditedImportDraftUsesSameTransaction(t *testing.T) {
	dir := fixture(t)
	source := filepath.Join(t.TempDir(), "ssh-config")
	put(t, filepath.Dir(source), filepath.Base(source), "Host imported\n  HostName 192.0.2.40\n  User root\n")
	checked, err := validate.Run(testContext, dir, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	d, review, err := importer.PrepareDraft(testContext, checked.Repo, "ssh-config", source)
	if err != nil || !review.OK || len(d.Candidates) != 1 {
		t.Fatal(review, err)
	}
	d.Candidates[0].Record["metadata"].(map[string]any)["description"] = "human-reviewed import"
	draftPath := filepath.Join(t.TempDir(), "draft.json")
	if err = d.Save(testContext, dir, draftPath); err != nil {
		t.Fatal(err)
	}
	p, err := FromDraft(testContext, dir, draftPath, source)
	if err != nil || !p.OK || len(p.Files) != 1 {
		t.Fatal(p, err)
	}
	r, err := Apply(testContext, dir, p)
	outcome(t, r, err, "committed")
	b, err := os.ReadFile(filepath.Join(dir, "inventory/servers/imported.yaml"))
	if err != nil || !bytes.Contains(b, []byte("human-reviewed import")) {
		t.Fatal("edited draft was not committed")
	}
}
func TestChangedImportSourceRefused(t *testing.T) {
	dir := fixture(t)
	source := filepath.Join(t.TempDir(), "ssh-config")
	put(t, filepath.Dir(source), filepath.Base(source), "Host imported\n  HostName 192.0.2.40\n")
	checked, err := validate.Run(testContext, dir, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := importer.PrepareDraft(testContext, checked.Repo, "ssh-config", source)
	if err != nil {
		t.Fatal(err)
	}
	draftPath := filepath.Join(t.TempDir(), "draft.json")
	if err = d.Save(testContext, dir, draftPath); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Dir(source), filepath.Base(source), "Host imported\n  HostName 192.0.2.41\n")
	if _, err = FromDraft(testContext, dir, draftPath, source); err == nil {
		t.Fatal("changed source accepted")
	}
}

func TestGitWorktreeDataRepository(t *testing.T) {
	dir := fixture(t)
	worktree := filepath.Join(t.TempDir(), "data")
	gitTest(t, dir, "worktree", "add", "-b", "data", worktree, "HEAD")
	p := request(t, worktree)
	r, err := Apply(testContext, worktree, p)
	outcome(t, r, err, "committed")
	if gitTest(t, worktree, "status", "--porcelain") != "" {
		t.Fatal("dirty worktree index")
	}
	if gitTest(t, dir, "rev-parse", "HEAD") == r.Commit {
		t.Fatal("changed another worktree branch")
	}
}

func TestCorruptJournalAndActiveRefused(t *testing.T) {
	for _, mode := range []string{"version", "duplicate", "path", "active", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			dir := fixture(t)
			p := request(t, dir)
			r, err := apply(testContext, dir, p, func(point string) error {
				if point == "prepared" {
					return os.ErrClosed
				}
				return nil
			})
			outcome(t, r, err, "recovery_required")
			file := privatePath(dir, r.Transaction, ".json")
			b, _ := os.ReadFile(file)
			switch mode {
			case "version":
				b = []byte(`{"version":"v2"}`)
			case "duplicate":
				b = append([]byte(`{"version":"v1",`), b[1:]...)
			case "path":
				var tr transaction
				json.Unmarshal(b, &tr)
				tr.Data.Preview.Files[0].Path = "../outside"
				tr.Data.Preview.Digest, _ = tr.Data.Preview.sum()
				b, _ = encoded(&tr)
			case "active":
				put(t, dir, ".infrafile/active", "")
			case "symlink":
				if err = os.Remove(file); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "journal")
				put(t, filepath.Dir(outside), filepath.Base(outside), string(b))
				if err = os.Symlink(outside, file); err != nil {
					t.Skip(err)
				}
			}
			if mode != "symlink" && mode != "active" {
				if err = os.WriteFile(file, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = Recover(testContext, dir, r.Transaction); err == nil {
				t.Fatal("corrupt recovery accepted")
			}
			if _, err = Status(testContext, dir); err == nil {
				t.Fatal("corrupt status accepted")
			}
		})
	}
}
