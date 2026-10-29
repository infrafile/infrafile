package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresenceBoundary(t *testing.T) {
	good := "apiVersion: infrafile/v1alpha1\nkind: Presence\nspec:\n  Domain/example.com:\n    binding_hash: " + strings.Repeat("a", 64) + "\n    last_seen: '2026-12-01T10:00:00Z'\n"
	for name, content := range map[string]string{
		"empty": "", "envelope": strings.Replace(good, "Presence", "Snapshot", 1),
		"version":   strings.Replace(good, "infrafile/v1alpha1", "infrafile/v2", 1),
		"unknown":   good + "    secret: synthetic-private-presence-value\n",
		"duplicate": good + "    last_seen: '2026-12-02T10:00:00Z'\n",
		"multiple":  good + "---\n{}\n", "time": strings.Replace(good, "2026-12-01T10:00:00Z", "yesterday", 1),
		"hash":     strings.Replace(good, strings.Repeat("a", 64), "wrong", 1),
		"target":   strings.Replace(good, "Domain/example.com", "../escape", 1),
		"null":     "apiVersion: infrafile/v1alpha1\nkind: Presence\nspec: null\n",
		"deep":     "a: " + strings.Repeat("[", 110) + "0" + strings.Repeat("]", 110),
		"oversize": strings.Repeat(" ", (4<<20)+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePresence(context.Background(), []byte(content)); err == nil || strings.Contains(err.Error(), "synthetic-private") {
				t.Fatalf("accepted invalid input or exposed data: %v", err)
			}
		})
	}
	dir := t.TempDir()
	if got, err := ReadPresence(context.Background(), dir); err != nil || got != nil {
		t.Fatalf("legacy: %v %v", got, err)
	}
	write(t, dir, "state/"+PresenceFile, good)
	if got, err := ReadPresence(context.Background(), dir); err != nil || got.Spec["Domain/example.com"].LastSeen == "" {
		t.Fatalf("valid: %v %v", got, err)
	}
	if got, err := Read(context.Background(), dir, nil); err != nil || len(got) != 0 {
		t.Fatalf("hidden file compatibility: %v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadPresence(ctx, dir); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
	write(t, dir, "state/.sync.lock", "")
	if _, err := ReadPresence(context.Background(), dir); err == nil {
		t.Fatal("read through lock")
	}
	if err := os.Remove(filepath.Join(dir, "state/.sync.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "state", PresenceFile), filepath.Join(dir, "baseline.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "baseline.yaml"), filepath.Join(dir, "state", PresenceFile)); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := ReadPresence(context.Background(), dir); err == nil {
		t.Fatal("followed baseline symlink")
	}
}
