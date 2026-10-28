package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func statusYAML() string {
	return "apiVersion: infrafile/v1alpha1\nkind: SyncStatus\nspec:\n  example:\n    home:\n      attempted_at: '2026-12-01T12:00:00Z'\n      status: failed\n      config_hash: " + strings.Repeat("a", 64) + "\n      code: timeout\n"
}

func TestReadStatus(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadStatus(context.Background(), dir)
	if err != nil || got != nil {
		t.Fatalf("old repository: %v %v", got, err)
	}
	write(t, dir, "state/"+StatusFile, statusYAML())
	got, err = ReadStatus(context.Background(), dir)
	if err != nil || got.Spec["example"]["home"].Code != "timeout" {
		t.Fatalf("status: %+v %v", got, err)
	}
	// v0.2 also skips dotfiles; execution metadata must not become a fake plugin directory.
	if snaps, err := Read(context.Background(), dir, nil); err != nil || len(snaps) != 0 {
		t.Fatalf("metadata treated as snapshot: %+v %v", snaps, err)
	}
	write(t, dir, "state/.sync.lock", "")
	if _, err := ReadStatus(context.Background(), dir); err == nil {
		t.Fatal("read while sync lock held")
	}
}

func TestParseStatusRejectsUnsafeAndInconsistentInput(t *testing.T) {
	good := statusYAML()
	cases := map[string]string{
		"empty":                "",
		"envelope":             strings.Replace(good, "SyncStatus", "Snapshot", 1),
		"version":              strings.Replace(good, "infrafile/v1alpha1", "infrafile/v2", 1),
		"unknown-root":         good + "password: synthetic-private-status-value\n",
		"unknown-field":        good + "      message: synthetic-private-status-value\n",
		"duplicate":            good + "      code: protocol\n",
		"multiple":             good + "---\n{}\n",
		"time":                 strings.Replace(good, "2026-12-01T12:00:00Z", "yesterday", 1),
		"config-hash":          strings.Replace(good, strings.Repeat("a", 64), "wrong", 1),
		"status":               strings.Replace(good, "status: failed", "status: unknown", 1),
		"failure-code":         strings.Replace(good, "code: timeout", "code: synthetic-private-status-value", 1),
		"failure-without-code": strings.Replace(good, "      code: timeout\n", "", 1),
		"failure-with-hash":    good + "      snapshot_hash: " + strings.Repeat("b", 64) + "\n",
		"running-with-code":    strings.Replace(good, "status: failed", "status: running", 1),
		"success-without-hash": strings.ReplaceAll(strings.Replace(good, "status: failed", "status: succeeded", 1), "      code: timeout\n", ""),
		"bad-plugin":           strings.Replace(good, "  example:", "  ../escape:", 1),
		"long-instance":        strings.Replace(good, "    home:", "    "+strings.Repeat("x", 65)+":", 1),
		"null-instances":       "apiVersion: infrafile/v1alpha1\nkind: SyncStatus\nspec: {example: null}\n",
		"null-spec":            "apiVersion: infrafile/v1alpha1\nkind: SyncStatus\nspec: null\n",
		"cycle":                "a: &cycle [*cycle]\n",
		"deep":                 "a: " + strings.Repeat("[", 110) + "0" + strings.Repeat("]", 110),
		"oversize":             strings.Repeat(" ", (4<<20)+1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseStatus(context.Background(), []byte(input))
			if err == nil || strings.Contains(err.Error(), "synthetic-private-status-value") {
				t.Fatalf("accepted invalid input or leaked its content: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParseStatus(ctx, []byte(good)); err != context.Canceled {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestReadStatusRejectsLinks(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "private.yaml", statusYAML())
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "private.yaml"), filepath.Join(dir, "state", StatusFile)); err != nil {
		t.Skip("system does not support symlinks")
	}
	if _, err := ReadStatus(context.Background(), dir); err == nil {
		t.Fatal("followed status symlink")
	}
}
