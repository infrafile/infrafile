//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func retentionFixture(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(root, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err = os.Mkdir(packageDir, 0700); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(packageDir, "ops.json")
}

func TestGenerationLifetimeSharedLeasesExcludeRetirementUntilEveryReaderReleases(t *testing.T) {
	root, overview := retentionFixture(t)
	first, err := opsRetainGeneration(overview)
	if err != nil {
		t.Fatal(err)
	}
	second, err := opsRetainGeneration(overview)
	if err != nil {
		first()
		t.Fatal(err)
	}
	if release, err := opsGenerationLock(filepath.Join(root, ".retention.lock")); err == nil {
		release()
		t.Fatal("retired live generation")
	}
	first()
	if release, err := opsGenerationLock(filepath.Join(root, ".retention.lock")); err == nil {
		release()
		t.Fatal("second reader lost protection")
	}
	second()
	release, err := opsGenerationLock(filepath.Join(root, ".retention.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if next, err := opsRetainGeneration(overview); err == nil {
		next()
		t.Fatal("new reader entered during retirement")
	}
}

func TestGenerationRetentionOrdinaryLegacyViewIsUnchanged(t *testing.T) {
	root, overview := retentionFixture(t)
	release, err := opsRetainGeneration(filepath.Join(root, "manual", "ops.json"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err = os.Stat(filepath.Join(root, ".retention.lock")); !os.IsNotExist(err) {
		t.Fatal("ordinary manual view gained files")
	}
	if err = os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if release, err = opsRetainGeneration(overview); err != nil {
		t.Fatal("ordinary non-private legacy view changed")
	}
	release()
}

func TestGenerationRetentionLockSymlinkIsRefused(t *testing.T) {
	root, overview := retentionFixture(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".retention.lock")); err != nil {
		t.Fatal(err)
	}
	if release, err := opsRetainGeneration(overview); err == nil {
		release()
		t.Fatal("symlink accepted")
	}
	value, err := os.ReadFile(outside)
	if err != nil || string(value) != "untouched" {
		t.Fatal("external file changed")
	}
}
