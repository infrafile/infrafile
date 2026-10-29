package main

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/audit"
)

func TestIssueIdentitiesAgreeAcrossCLIAndMCP(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	before := mcpFiles(t)
	session := mcpSession(t, ".")
	for _, command := range []string{"audit", "inspect"} {
		var out, stderr bytes.Buffer
		code := runAt([]string{command, "--max-age", "45d", "--json"}, &out, &stderr, time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC))
		if code != 0 && code != 1 {
			t.Fatalf("%s: %d %s", command, code, &stderr)
		}
		var cli map[string]any
		if json.Unmarshal(out.Bytes(), &cli) != nil {
			t.Fatal("invalid CLI JSON")
		}
		got := callMCP(t, session, command, map[string]any{"max_age_days": 45}, false)
		if !reflect.DeepEqual(cli, got) {
			t.Fatalf("%s CLI/MCP identity contract differs", command)
		}
		value := cli
		if command == "inspect" {
			value = cli["risk"].(map[string]any)
		}
		findings := value["findings"].([]any)
		if len(findings) == 0 {
			t.Fatal("fixture did not exercise finding identities")
		}
		for _, raw := range findings {
			finding := raw.(map[string]any)
			identities, ok := finding["issues"].([]any)
			if !ok || len(identities) == 0 {
				t.Fatal("finding lacks identity")
			}
			for _, value := range identities {
				data, _ := json.Marshal(value)
				var id audit.IssueIdentity
				if err := json.Unmarshal(data, &id); err != nil {
					t.Fatal(err)
				}
				if err := id.Validate(); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("query wrote issue history or repository files")
	}
}
