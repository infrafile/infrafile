package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/importer"
)

func draftCLI(t *testing.T, want int, args ...string) importer.ReviewResult {
	t.Helper()
	data := importCLI(t, want, append(append([]string{"import"}, args...), "--json")...)
	var result importer.ReviewResult
	if err := json.Unmarshal(data, &result); err != nil || result.Version != "v1" || result.OK != (want == 0) {
		t.Fatalf("review: %s %v", data, err)
	}
	return result
}

func loadDraft(t *testing.T, path string) *importer.Draft {
	t.Helper()
	d, err := importer.ReadDraft(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func editDraft(t *testing.T, d *importer.Draft) {
	t.Helper()
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "draft.json", string(data))
}

func TestImportDraftDeterministicOfflineAndEditable(t *testing.T) {
	emptyRepo(t)
	importSnapshot(t, "aliyun", "home", "domains: [{name: example.com}]\ncdn: [{name: cdn.example.com}]\n")
	importSnapshot(t, "tls", "home", "host: example.com\nverified: false\n")
	old := http.DefaultTransport
	http.DefaultTransport = cloudReplay(func(*http.Request) (*http.Response, error) { t.Fatal("draft contacted network"); return nil, nil })
	t.Cleanup(func() { http.DefaultTransport = old })
	first := draftCLI(t, 0, "state", "--draft", "draft.json")
	draftCLI(t, 0, "state", "--draft", "second.json")
	a, _ := os.ReadFile("draft.json")
	b, _ := os.ReadFile("second.json")
	if !bytes.Equal(a, b) || len(first.Records) != 2 || len(first.Skipped) != 1 || first.Skipped[0].Reason != "observation-only" {
		t.Fatal(first)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat("draft.json")
		if info.Mode().Perm() != 0o600 {
			t.Fatal("draft permissions")
		}
	}
	before := importFiles(t)
	if review := draftCLI(t, 0, "review", "draft.json"); !reflect.DeepEqual(first, review) {
		t.Fatal("saved review differs")
	}
	if !reflect.DeepEqual(before, importFiles(t)) {
		t.Fatal("review modified files")
	}
	d := loadDraft(t, "draft.json")
	for _, candidate := range d.Candidates {
		spec := candidate.Record["spec"].(map[string]any)
		if candidate.Identity.Kind == "Domain" {
			if _, ok := spec["expires_at"]; ok {
				t.Fatal("invented date")
			}
			spec["expires_at"] = "2030-01-01"
			candidate.Record["metadata"].(map[string]any)["labels"] = map[string]any{"owner": "ops"}
		} else if _, ok := spec["resource_id"]; ok {
			t.Fatal("invented resource ID")
		}
	}
	editDraft(t, d)
	before = importFiles(t)
	review := draftCLI(t, 0, "review", "draft.json")
	for _, entry := range review.Records {
		if entry.Key == "Domain/example.com" && slices.Contains(entry.Gaps, "missing-expires_at") {
			t.Fatal(entry)
		}
	}
	if !reflect.DeepEqual(before, importFiles(t)) {
		t.Fatal("edited review wrote inventory")
	}
	importCLI(t, 2, "import", "state", "--draft", "draft.json")
	if !reflect.DeepEqual(before, importFiles(t)) {
		t.Fatal("overwrote edited draft")
	}
}

func TestImportDraftSourceAndRepositoryVersions(t *testing.T) {
	for _, change := range []string{"snapshot-comment", "observation-value", "new-source", "removed-source", "repository", "source-manifest"} {
		t.Run(change, func(t *testing.T) {
			emptyRepo(t)
			importSnapshot(t, "aliyun", "home", "domains: [{name: example.com}]\n")
			draftCLI(t, 0, "state", "--draft", "draft.json")
			originalID := loadDraft(t, "draft.json").Candidates[0].ID
			switch change {
			case "observation-value":
				importSnapshot(t, "aliyun", "home", "domains: [{name: example.com, expires_at: '2035-01-01'}]\n")
			case "snapshot-comment":
				f, _ := os.ReadFile("state/aliyun/home.yaml")
				writeImportFile(t, "state/aliyun/home.yaml", string(f)+"# source revision\n")
			case "new-source":
				importSnapshot(t, "aliyun", "other", "domains: []\n")
			case "removed-source":
				if err := os.Remove("state/aliyun/home.yaml"); err != nil {
					t.Fatal(err)
				}
			case "repository":
				if err := os.MkdirAll("inventory/accounts", 0o755); err != nil {
					t.Fatal(err)
				}
				writeImportFile(t, "inventory/accounts/demo.yaml", "apiVersion: infrafile/v1alpha1\nkind: Account\nmetadata: {name: demo}\nspec: {provider: aliyun}\n")
			case "source-manifest":
				d := loadDraft(t, "draft.json")
				d.Files[0].Path = filepath.Join(t.TempDir(), "missing-private-file")
				editDraft(t, d)
			}
			before := importFiles(t)
			r := draftCLI(t, 1, "review", "draft.json")
			if change == "repository" {
				if !r.RepoChanged || r.SourceChanged {
					t.Fatal(r)
				}
			} else if !r.SourceChanged {
				t.Fatal(r)
			}
			if !reflect.DeepEqual(before, importFiles(t)) {
				t.Fatal("stale review wrote files")
			}
			if change == "observation-value" {
				draftCLI(t, 0, "state", "--draft", "regenerated.json")
				if loadDraft(t, "regenerated.json").Candidates[0].ID != originalID {
					t.Fatal("observation value changed source identity")
				}
			}
		})
	}
}

func TestImportDraftSSHTraceAndExplicitInput(t *testing.T) {
	emptyRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh", "parts"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, filepath.Join(home, ".ssh", "parts", "one.conf"), "Host web1\n HostName 192.0.2.1\n User observer\n Port 2222\n")
	writeImportFile(t, "ssh.conf", "Include parts/*.conf\n")
	draftCLI(t, 0, "ssh-config", "--file", "ssh.conf", "--draft", "draft.json")
	d := loadDraft(t, "draft.json")
	if len(d.Files) != 2 || len(d.Candidates) != 1 || d.Candidates[0].Identity.Address != "192.0.2.1" {
		t.Fatal(d)
	}
	importCLI(t, 2, "import", "review", "draft.json")
	draftCLI(t, 0, "review", "draft.json", "--file", "ssh.conf")
	writeImportFile(t, filepath.Join(home, ".ssh", "parts", "two.conf"), "Host web2\n HostName 192.0.2.2\n")
	if r := draftCLI(t, 1, "review", "draft.json", "--file", "ssh.conf"); !r.SourceChanged {
		t.Fatal(r)
	}
}

func TestImportDraftConflictsAndExplicitExclusion(t *testing.T) {
	emptyRepo(t)
	importSnapshot(t, "aliyun", "home", "domains: [{name: example.com, expires_at: '2030-01-01'}]\n")
	importSnapshot(t, "whois", "home", "domain: example.com\nfound: true\nexpires_at: '2031-01-01'\n")
	r := draftCLI(t, 1, "state", "--draft", "draft.json")
	if len(r.Records) != 2 || r.Records[0].Status != "conflict" || r.Records[1].Status != "conflict" {
		t.Fatal(r)
	}
	d := loadDraft(t, "draft.json")
	d.Candidates[1].Selected = false
	editDraft(t, d)
	r = draftCLI(t, 0, "review", "draft.json")
	if r.Records[0].Status != "draft" || r.Records[1].Status != "excluded" {
		t.Fatal(r)
	}
	if _, err := os.Stat("inventory"); !os.IsNotExist(err) {
		t.Fatal("conflict/exclusion created inventory")
	}
}

func TestImportDraftExistingArchivedAndNaturalIdentities(t *testing.T) {
	for _, kind := range []string{"same", "different", "archived", "renamed", "renamed-archived"} {
		t.Run(kind, func(t *testing.T) {
			emptyRepo(t)
			if err := os.MkdirAll("inventory/servers", 0o755); err != nil {
				t.Fatal(err)
			}
			name, archive, address := "web1", "", "192.0.2.1"
			if strings.HasPrefix(kind, "renamed") {
				name = "canonical"
			}
			if strings.HasSuffix(kind, "archived") {
				archive = ", archived: {date: '2026-01-01', reason: retired}"
			}
			if kind == "different" {
				address = "192.0.2.2"
			}
			writeImportFile(t, "inventory/servers/"+name+".yaml", "# retain this comment\napiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: "+name+archive+"}\nspec: {address: "+address+", hardware: {os: 'Linux 6.1-example'}}\n")
			importSnapshot(t, "ssh", "web1", "host: 192.0.2.1\nsystem: Linux\nrelease: 6.1-example\n")
			want, status := 0, "exists"
			if kind == "archived" {
				status = "archived"
			} else if kind != "same" {
				want, status = 1, "conflict"
			}
			r := draftCLI(t, want, "state", "--draft", "draft.json")
			if len(r.Records) != 1 || r.Records[0].Status != status {
				t.Fatal(r)
			}
			before := importFiles(t)
			draftCLI(t, want, "review", "draft.json")
			if !reflect.DeepEqual(before, importFiles(t)) {
				t.Fatal("review changed existing/archived file")
			}
		})
	}
}

func TestImportDraftBatchReferencesAndSecrets(t *testing.T) {
	emptyRepo(t)
	importSnapshot(t, "aliyun", "home", "domains: [{name: example.com}]\n")
	importSnapshot(t, "ssh", "web1", "host: 192.0.2.1\nsystem: Linux\nrelease: 6.1-example\n")
	draftCLI(t, 0, "state", "--draft", "draft.json")
	initial, _ := os.ReadFile("draft.json")
	for _, scenario := range []string{"valid", "missing-ref", "archived-ref", "plaintext"} {
		t.Run(scenario, func(t *testing.T) {
			writeImportFile(t, "draft.json", string(initial))
			d := loadDraft(t, "draft.json")
			for i := range d.Candidates {
				c := &d.Candidates[i]
				if c.Identity.Kind == "Domain" {
					target := "web1"
					if scenario == "missing-ref" {
						target = "absent"
					}
					c.Record["spec"].(map[string]any)["bindings"] = []any{map[string]any{"name": "@", "server": target}}
					if scenario == "plaintext" {
						c.Record["spec"].(map[string]any)["x-password"] = "synthetic-do-not-echo"
					}
				} else if scenario == "archived-ref" {
					c.Record["metadata"].(map[string]any)["archived"] = map[string]any{"date": "2026-01-01", "reason": "retired"}
				}
			}
			editDraft(t, d)
			want := 1
			if scenario == "valid" {
				want = 0
			}
			r := draftCLI(t, want, "review", "draft.json")
			if scenario == "missing-ref" && !slices.Contains(r.Problems, "ref") {
				t.Fatal(r)
			}
			if scenario == "archived-ref" && !slices.Contains(r.Problems, "ref-archived") {
				t.Fatal(r)
			}
			if scenario == "plaintext" {
				encoded, _ := json.Marshal(r)
				if !slices.Contains(r.Problems, "plaintext") || bytes.Contains(encoded, []byte("synthetic-do-not-echo")) {
					t.Fatal(r)
				}
			}
		})
	}
}

func TestImportDraftInputAndDestinationBoundaries(t *testing.T) {
	emptyRepo(t)
	importSnapshot(t, "aliyun", "home", "domains: [{name: example.com}]\n")
	draftCLI(t, 0, "state", "--draft", "draft.json")
	original, _ := os.ReadFile("draft.json")
	for _, scenario := range []string{"version", "duplicate-key", "field-case", "unknown", "missing-boolean", "traversal", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			data := string(original)
			switch scenario {
			case "version":
				data = strings.Replace(data, `"version": "v1"`, `"version": "v2"`, 1)
			case "duplicate-key":
				data = strings.Replace(data, `"version": "v1"`, `"version": "v1", "version": "v1"`, 1)
			case "field-case":
				data = strings.Replace(data, `"selected": true`, `"Selected": true`, 1)
			case "unknown":
				data = strings.Replace(data, `"version": "v1"`, `"version": "v1", "execute": true`, 1)
			case "missing-boolean":
				data = strings.Replace(data, `"selected": true,`, "", 1)
			case "traversal":
				var d importer.Draft
				if err := json.Unmarshal(original, &d); err != nil {
					t.Fatal(err)
				}
				d.Candidates[0].Record["metadata"].(map[string]any)["name"] = "../../escape"
				encoded, err := json.Marshal(d)
				if err != nil {
					t.Fatal(err)
				}
				data = string(encoded)
			case "oversized":
				data = strings.Repeat(" ", 4<<20) + data
			}
			writeImportFile(t, "draft.json", data)
			if scenario == "traversal" {
				draftCLI(t, 1, "review", "draft.json")
			} else {
				importCLI(t, 2, "import", "review", "draft.json")
			}
		})
	}
	writeImportFile(t, "draft.json", string(original))
	if err := os.Symlink("draft.json", "linked.json"); err == nil {
		importCLI(t, 2, "import", "review", "linked.json")
	}
	for _, path := range []string{"inventory/new.json", "Inventory/new.json", "state/new.json", "State/new.json"} {
		importCLI(t, 2, "import", "state", "--draft", path)
	}
	importCLI(t, 2, "import", "state", "--draft", "other.json", "--write")
	importCLI(t, 2, "import", "state", "--draft", "other.json", "--output", "review")
	importCLI(t, 2, "import", "review", "draft.json", "--file", "not-opened.conf")
	if _, err := os.Stat("inventory"); !os.IsNotExist(err) {
		t.Fatal("rejected destination created inventory")
	}
}

func TestImportDraftCandidateBudget(t *testing.T) {
	emptyRepo(t)
	var spec strings.Builder
	spec.WriteString("domains:\n")
	for i := 0; i < 4097; i++ {
		fmt.Fprintf(&spec, "  - {name: d%d.example.com}\n", i)
	}
	importSnapshot(t, "aliyun", "home", spec.String())
	importCLI(t, 2, "import", "state", "--draft", "draft.json")
	if _, err := os.Stat("draft.json"); !os.IsNotExist(err) {
		t.Fatal("oversized candidate set published")
	}
}

func TestImportDraftAccountScopeIsExplicit(t *testing.T) {
	emptyRepo(t)
	if err := os.MkdirAll("inventory/accounts", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		writeImportFile(t, "inventory/accounts/"+name+".yaml", "apiVersion: infrafile/v1alpha1\nkind: Account\nmetadata: {name: "+name+"}\nspec: {provider: aliyun}\n")
	}
	for _, instance := range []string{"one", "two"} {
		importSnapshot(t, "aliyun", instance, "cdn: [{name: cdn.example.com, id: shared-id}]\n")
	}
	draftCLI(t, 1, "state", "--draft", "draft.json")
	d := loadDraft(t, "draft.json")
	for i := range d.Candidates {
		c := &d.Candidates[i]
		account := strings.TrimSuffix(strings.TrimPrefix(c.Source, "state/aliyun/"), ".yaml")
		c.Record["spec"].(map[string]any)["account"] = account
	}
	editDraft(t, d)
	r := draftCLI(t, 0, "review", "draft.json")
	if len(r.Records) != 2 || r.Records[0].Status != "draft" || r.Records[1].Status != "draft" {
		t.Fatal(r)
	}
	delete(d.Candidates[0].Record["spec"].(map[string]any), "account")
	editDraft(t, d)
	draftCLI(t, 1, "review", "draft.json")
}

func TestImportDraftCancelledDoesNotSave(t *testing.T) {
	emptyRepo(t)
	importSnapshot(t, "aliyun", "home", "domains: [{name: example.com}]\n")
	draftCLI(t, 0, "state", "--draft", "draft.json")
	d := loadDraft(t, "draft.json")
	before := importFiles(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.Save(ctx, ".", "uncreated/draft.json"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := importer.ReadDraft(ctx, "draft.json"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, importFiles(t)) {
		t.Fatal("cancelled draft operation changed files")
	}
	if _, err := os.Stat("uncreated"); !os.IsNotExist(err) {
		t.Fatal("cancelled save created directory")
	}
}

func TestImportDraftSaveRejectsPlaintext(t *testing.T) {
	emptyRepo(t)
	importSnapshot(t, "aliyun", "home", "domains: [{name: example.com}]\n")
	draftCLI(t, 0, "state", "--draft", "draft.json")
	d := loadDraft(t, "draft.json")
	d.Candidates[0].Record["spec"].(map[string]any)["x-password"] = "synthetic-private-value"
	if err := d.Save(context.Background(), ".", "not-created/unsafe.json"); err == nil || strings.Contains(err.Error(), "synthetic-private-value") {
		t.Fatal("unsafe draft saved or reflected")
	}
	if _, err := os.Stat("not-created"); !os.IsNotExist(err) {
		t.Fatal("unsafe save changed filesystem")
	}
}

func TestImportDraftKeepsIDAndEndpointIdentitiesSeparate(t *testing.T) {
	emptyRepo(t)
	if err := os.MkdirAll("inventory/resources", 0o755); err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "inventory/resources/custom.yaml", "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: custom}\nspec: {provider: aliyun, type: id, endpoint: shared-id}\n")
	importSnapshot(t, "aliyun", "home", "cdn: [{name: cdn.example.com, id: shared-id}]\n")
	r := draftCLI(t, 0, "state", "--draft", "draft.json")
	if len(r.Records) != 1 || r.Records[0].Status != "draft" {
		t.Fatal(r)
	}
}
