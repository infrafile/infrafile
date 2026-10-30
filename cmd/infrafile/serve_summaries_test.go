package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWorkbenchAssetSummariesBoundary(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	f := newWebFixture(t, dir)
	if out := f.send(f.request("POST", "/api/asset-summaries", "{}")); out.Code != 401 {
		t.Fatal(out.Code)
	}
	f.login(t)
	t.Chdir(dir)
	before := mcpFiles(t)
	out := f.send(f.request("POST", "/api/asset-summaries", `{"max_age_days":7}`))
	if out.Code != 200 {
		t.Fatal("summary route missing", out.Code, out.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	rows := result["rows"].([]any)
	if len(rows) == 0 {
		t.Fatal("missing directory")
	}
	for _, raw := range rows {
		row := raw.(map[string]any)
		if _, ok := row["spec"]; ok {
			t.Fatal("summary exposed spec")
		}
		if _, ok := row["coverage"].(map[string]any)["checks"]; ok {
			t.Fatal("summary exposed full checks")
		}
	}
	if strings.Contains(out.Body.String(), "sops://") {
		t.Fatal("secret reference leaked")
	}
	for _, body := range []string{`{"key":"Server/web1","labels":{"env":"production"}}`, `{"key":"Server/web1","key":"Server/web1"}`, `{"dir":"/tmp"}`, `{"command":"sync"}`, `{"key":"../../outside"}`, `{"max_age_days":-1}`, `{"labels":{}}`} {
		if out := f.send(f.request("POST", "/api/asset-summaries", body)); out.Code != 400 {
			t.Fatal("invalid summary arguments", body, out.Code)
		}
	}
	for _, body := range []string{`{"kind":"Server","labels":{"absent":"true"}}`, `{"kind":"App"}`} {
		out := f.send(f.request("POST", "/api/asset-summaries", body))
		if out.Code != 200 {
			t.Fatal(out.Code, out.Body.String())
		}
		if strings.Contains(body, "absent") && !strings.Contains(out.Body.String(), `"rows":[]`) {
			t.Fatal("empty filter expanded")
		}
	}
	request := f.request("POST", "/api/asset-summaries", "{}")
	request.Header.Set("X-CSRF-Token", "wrong")
	if out := f.send(request); out.Code != 403 {
		t.Fatal("CSRF bypass", out.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out := f.send(f.request("POST", "/api/asset-summaries", "{}").WithContext(ctx)); out.Code != 504 {
		t.Fatal("cancel ignored", out.Code)
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("summary wrote ledger files")
	}
	definitions, err := queryTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.name == "asset-summaries" {
			t.Fatal("Web summary exposed over MCP")
		}
	}
}

func TestWorkbenchAssetSummariesGeneration(t *testing.T) {
	root := t.TempDir()
	one, two, current := filepath.Join(root, "one"), filepath.Join(root, "two"), filepath.Join(root, "current")
	for _, dir := range []string{one, two} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(two, "inventory/servers/web1.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, file, strings.Replace(string(data), "role: web", "role: web\n    generation: two", 1))
	if err := os.Symlink(one, current); err != nil {
		t.Fatal(err)
	}
	ctx := &mcpGenerationContext{Context: context.Background(), swap: func() {
		next := filepath.Join(root, "next")
		if err := os.Symlink(two, next); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(next, current); err != nil {
			t.Fatal(err)
		}
	}}
	first, err := workbenchCall(ctx, current, "asset-summaries", mcpArgs{Key: "Server/web1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := workbenchCall(context.Background(), current, "asset-summaries", mcpArgs{Key: "Server/web1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if strings.Contains(string(a), `"generation":"two"`) || !strings.Contains(string(b), `"generation":"two"`) {
		t.Fatal("mixed published generations", string(a), string(b))
	}
}

func TestWorkbenchAssetSummariesResultLimitAndSecrets(t *testing.T) {
	for _, name := range []string{"limit", "sensitive", "corrupt"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
				t.Fatal(err)
			}
			f := newWebFixture(t, dir)
			f.login(t)
			switch name {
			case "limit":
				writeImportFile(t, filepath.Join(dir, "inventory/servers/web1.yaml"), "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata:\n  name: web1\n  labels: {note: '"+strings.Repeat("<", 1<<20)+"'}\nspec: {address: 203.0.113.10}\n")
				result, err := workbenchCall(context.Background(), dir, "asset-summaries", mcpArgs{}, f.clock)
				encoded, marshalErr := json.Marshal(result)
				if err != nil || marshalErr != nil || len(encoded) <= 4<<20 {
					t.Fatal("fixture did not reach the serialized result limit", err, marshalErr, len(encoded))
				}
			case "sensitive":
				writeImportFile(t, filepath.Join(dir, "state/cloudflare/home.yaml"), "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {password: synthetic-web-private-value}\n")
			case "corrupt":
				writeImportFile(t, filepath.Join(dir, "state/.findings.json"), "corrupted history")
			}
			out := f.send(f.request("POST", "/api/asset-summaries", "{}"))
			if out.Code != 422 || out.Body.Len() > 1024 || strings.Contains(out.Body.String(), "synthetic-web-private-value") {
				t.Fatal("unsafe or partial result", out.Code, out.Body.Len())
			}
		})
	}
}
