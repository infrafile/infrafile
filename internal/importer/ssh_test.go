package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHRepeatedIncludesAreBounded(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 8; i++ {
		body := "Host sample\n HostName 192.0.2.1\n"
		if i < 7 {
			next := filepath.Join(dir, fmt.Sprintf("%d.conf", i+1))
			body = "Include " + strings.Repeat(fmt.Sprintf("%q ", next), 4) + "\n"
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.conf", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := SSHConfig(context.Background(), filepath.Join(dir, "0.conf"))
	if err == nil || !strings.Contains(err.Error(), "1024") || entries != nil {
		t.Fatalf("repeated includes: entries=%d err=%v", len(entries), err)
	}
}

func TestSSHRepeatedIncludesCountBytes(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child.conf")
	// Each file fits the limit; reading the same file twice must exhaust it.
	body := strings.Repeat("#"+strings.Repeat("x", 1022)+"\n", maxSSHBytes/1024/2)
	if err := os.WriteFile(child, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "config")
	if err := os.WriteFile(root, []byte(fmt.Sprintf("Include %q %q\n", child, child)), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := SSHConfig(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "8 MiB") || entries != nil {
		t.Fatalf("repeated bytes: entries=%d err=%v", len(entries), err)
	}
}

func TestSSHEvaluationBudgetIsShared(t *testing.T) {
	b := &sshBudget{ctx: context.Background()}
	ds := []directive{{key: "user", values: []string{"observer"}}}
	for i := 0; i < maxSSHEvaluations/2; i++ {
		if _, err := b.evaluateSSH(ds, "sample", map[string]string{}); err != nil {
			t.Fatalf("budget exhausted early: %v", err)
		}
	}
	if _, err := b.evaluateSSH(ds, "another", map[string]string{}); err == nil || !strings.Contains(err.Error(), "1000000") {
		t.Fatalf("evaluation budget was reset: %v", err)
	}
}

func TestSSHCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if entries, err := SSHConfig(ctx, filepath.Join(t.TempDir(), "missing")); !errors.Is(err, context.Canceled) || entries != nil {
		t.Fatalf("cancelled read: entries=%d err=%v", len(entries), err)
	}
	b := &sshBudget{ctx: ctx}
	if _, err := b.evaluateSSH([]directive{{key: "user", values: []string{"observer"}}}, "sample", map[string]string{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled evaluation: %v", err)
	}
}
