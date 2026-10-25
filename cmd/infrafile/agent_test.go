package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/agent"
)

func TestAgentInstallMatchesEmbeddedGuideAndPreservesFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	guide := importCLI(t, 0, "agent", "guide")
	if !bytes.HasPrefix(guide, []byte("---\nname: infrafile\n")) || !strings.Contains(string(guide), "infrafile_plan") {
		t.Fatalf("bad guide: %s", guide)
	}
	for client, path := range map[string]string{"codex": ".agents/skills/infrafile/SKILL.md", "claude": ".claude/skills/infrafile/SKILL.md"} {
		importCLI(t, 0, "agent", "install", client, "--json")
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, guide) {
			t.Fatalf("%s: %s %v", client, got, err)
		}
		before, _ := os.Stat(path)
		importCLI(t, 0, "agent", "install", client)
		after, _ := os.Stat(path)
		if !before.ModTime().Equal(after.ModTime()) {
			t.Fatal("identical install changed mtime")
		}
		writeImportFile(t, path, "user-edited guide")
		importCLI(t, 2, "agent", "install", client)
		got, _ = os.ReadFile(path)
		if string(got) != "user-edited guide" {
			t.Fatal("overwrote user's guide")
		}
	}
	if _, err := os.Stat(filepath.Join("AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("unexpected AGENTS.md write")
	}
}

func TestAgentInstallRejectsSymlinksAndUnknownClient(t *testing.T) {
	for _, part := range []string{".agents", ".agents/skills", ".agents/skills/infrafile", ".agents/skills/infrafile/SKILL.md"} {
		t.Run(part, func(t *testing.T) {
			dir, other := t.TempDir(), t.TempDir()
			target := other
			if strings.HasSuffix(part, "SKILL.md") {
				target = filepath.Join(other, "original.md")
				writeImportFile(t, target, "original")
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, part)), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(dir, part)); err != nil {
				t.Fatal(err)
			}
			if out := importCLI(t, 2, "agent", "install", "codex", "--dir", dir, "--json"); len(out) != 0 {
				t.Fatal("failure wrote stdout")
			}
			if strings.HasSuffix(part, "SKILL.md") {
				data, _ := os.ReadFile(target)
				if string(data) != "original" {
					t.Fatal("symlink target changed")
				}
			} else if entries, _ := os.ReadDir(other); len(entries) != 0 {
				t.Fatal("wrote outside selected directory")
			}
		})
	}
	dir := t.TempDir()
	importCLI(t, 2, "agent", "install", "unknown", "--dir", dir)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("unknown client created files")
	}
	guide := agent.Guide("v0.1.0-test", "example-commit")
	if !strings.Contains(guide, "v0.1.0-test（commit example-commit）") || strings.Contains(guide, "{{") {
		t.Fatal("guide version missing")
	}
}
