package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/snapshot"
	"go.yaml.in/yaml/v3"
)

func certificateFixture(t *testing.T) (*webFixture, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("testdata/certificates")); err != nil {
		t.Fatal(err)
	}
	f := newWebFixture(t, dir)
	repo, err := loadQueryRepo(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	configs := repo.Config.Value.(map[string]any)["plugins"].(map[string]any)["tls"].(map[string]any)["instances"].(map[string]any)
	stamps := map[string]string{}
	attempts := map[string]snapshot.Attempt{}
	for name, raw := range configs {
		if name == "missing" {
			continue
		}
		cfg := raw.(map[string]any)
		port := 443
		if p, ok := cfg["port"].(int); ok {
			port = p
		}
		data, err := yaml.Marshal(map[string]any{"apiVersion": "infrafile/v1alpha1", "kind": "Snapshot", "metadata": map[string]any{"name": name}, "spec": map[string]any{"host": cfg["host"], "port": port, "verified": true, "verification": "valid", "certificate": map[string]any{"not_after": "2026-12-31T12:00:00Z", "subject": "CN=synthetic.example.com"}}})
		if err != nil {
			t.Fatal(err)
		}
		writeImportFile(t, filepath.Join(dir, "state/tls", name+".yaml"), string(data))
		stamp := f.clock.Format(time.RFC3339)
		stamps[name] = stamp
		attempts[name] = snapshot.Attempt{AttemptedAt: stamp, Status: "succeeded", ConfigHash: plugin.Hash(cfg), SnapshotHash: fmt.Sprintf("%x", sha256.Sum256(data))}
	}
	data, _ := yaml.Marshal(map[string]any{"apiVersion": "infrafile/v1alpha1", "kind": "Sync", "spec": map[string]any{"tls": stamps}})
	writeImportFile(t, filepath.Join(dir, "state/_sync.yaml"), string(data))
	data, _ = yaml.Marshal(snapshot.Status{APIVersion: "infrafile/v1alpha1", Kind: "SyncStatus", Spec: map[string]map[string]snapshot.Attempt{"tls": attempts}})
	writeImportFile(t, filepath.Join(dir, "state", snapshot.StatusFile), string(data))
	return f, dir
}

func certificateResult(t *testing.T, f *webFixture, body string) map[string]any {
	t.Helper()
	out := f.send(f.request("POST", "/api/certificates", body))
	if out.Code != 200 {
		t.Fatalf("certificates: %d %s", out.Code, out.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func certificateRows(result map[string]any) map[string]map[string]any {
	rows := map[string]map[string]any{}
	for _, raw := range result["groups"].([]any) {
		group := raw.(map[string]any)
		for _, raw := range group["certificates"].([]any) {
			row := raw.(map[string]any)
			row["group_app"], row["ownership"] = group["app"], group["ownership"]
			rows[row["host"].(string)] = row
		}
	}
	for _, raw := range result["uncollected"].([]any) {
		row := raw.(map[string]any)
		rows[row["host"].(string)] = row
	}
	return rows
}

func TestWorkbenchCertificatesOwnership(t *testing.T) {
	f, dir := certificateFixture(t)
	f.login(t)
	t.Chdir(dir)
	before := mcpFiles(t)
	oldHTTP := http.DefaultTransport
	http.DefaultTransport = cloudReplay(func(*http.Request) (*http.Response, error) { t.Fatal("certificate query used HTTP"); return nil, nil })
	t.Cleanup(func() { http.DefaultTransport = oldHTTP })
	result := certificateResult(t, f, "{}")
	rows := certificateRows(result)
	for host, want := range map[string]string{"wiki.example.com": "app", "example.com": "app", "assets.example.com": "no_app", "orphan.example.net": "unassociated", "shared.example.com": "ambiguous", "192.0.2.10": "unassociated"} {
		row := rows[host]
		if row["ownership"] != want || row["data_state"] != "fresh" || row["days_left"] != float64(30) || row["expiry_state"] != "expiring" {
			t.Errorf("%s: %#v", host, row)
		}
	}
	if rows["wiki.example.com"]["group_app"] != "App/wiki" || rows["example.com"]["group_app"] != "App/blog" || rows["example.com"]["port"] != float64(8443) {
		t.Fatal(rows)
	}
	for _, host := range []string{"missing.example.com", "unconfigured.example.com"} {
		if rows[host]["data_state"] != "uncollected" || rows[host]["expiry_state"] != "unknown" {
			t.Fatal(rows[host])
		}
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("certificate query wrote files")
	}
	tools, _ := queryTools()
	for _, tool := range tools {
		if tool.name == "certificates" {
			t.Fatal("Web query leaked into MCP")
		}
	}
}

func TestWorkbenchCertificatesBoundary(t *testing.T) {
	f, dir := certificateFixture(t)
	if out := f.send(f.request("POST", "/api/certificates", "{}")); out.Code != 401 {
		t.Fatal(out.Code)
	}
	f.login(t)
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		want   int
	}{
		{"origin", func(r *http.Request) { r.Header.Set("Origin", "https://other.example.com") }, 403},
		{"missing-origin", func(r *http.Request) { r.Header.Del("Origin") }, 403},
		{"cross-site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"csrf", func(r *http.Request) { r.Header.Set("X-CSRF-Token", "wrong") }, 403},
		{"media", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"size", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(strings.Repeat(" ", 65537))) }, 413},
		{"cancel", func(r *http.Request) {
			ctx, cancel := context.WithCancel(r.Context())
			cancel()
			*r = *r.WithContext(ctx)
		}, 504},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.request("POST", "/api/certificates", "{}")
			tc.change(r)
			if out := f.send(r); out.Code != tc.want {
				t.Fatal(out.Code, out.Body.String())
			}
		})
	}
	for _, body := range []string{`{"max_age_days":-1}`, `{"max_age_days":1,"max_age_days":2}`, `{"dir":"/tmp"}`, `{"key":"App/wiki"}`, `{} {}`, `null`} {
		if out := f.send(f.request("POST", "/api/certificates", body)); out.Code != 400 {
			t.Fatal(body, out.Code)
		}
	}
	for range 4 {
		f.app.requests <- struct{}{}
	}
	if out := f.send(f.request("POST", "/api/certificates", "{}")); out.Code != 429 {
		t.Fatal(out.Code)
	}
	for range 4 {
		<-f.app.requests
	}
	writeImportFile(t, filepath.Join(dir, "inventory/apps/wiki.yaml"), "secret: synthetic-do-not-echo\n")
	out := f.send(f.request("POST", "/api/certificates", "{}"))
	if out.Code != 422 || strings.TrimSpace(out.Body.String()) != `{"error":"Unprocessable Entity","ok":false,"version":"v1"}` {
		t.Fatal(out.Code, out.Body.String())
	}
}

func TestWorkbenchCertificatesFreshnessAndEmpty(t *testing.T) {
	for _, mode := range []string{"stale", "future", "failed", "running", "missing-status", "missing-sync", "changed-config", "changed-port", "changed-snapshot", "no-snapshots", "untrusted", "hostname-mismatch", "expired-or-not-yet-valid", "expired", "today"} {
		t.Run(mode, func(t *testing.T) {
			f, dir := certificateFixture(t)
			switch mode {
			case "stale":
				f.clock = f.clock.Add(7*24*time.Hour + time.Second)
			case "future":
				f.clock = f.clock.Add(-time.Second)
			case "missing-status":
				if err := os.Remove(filepath.Join(dir, "state", snapshot.StatusFile)); err != nil {
					t.Fatal(err)
				}
			case "missing-sync":
				if err := os.Remove(filepath.Join(dir, "state/_sync.yaml")); err != nil {
					t.Fatal(err)
				}
			case "no-snapshots":
				if err := os.RemoveAll(filepath.Join(dir, "state/tls")); err != nil {
					t.Fatal(err)
				}
			case "changed-config":
				data, _ := os.ReadFile(filepath.Join(dir, "infrafile.yaml"))
				writeImportFile(t, filepath.Join(dir, "infrafile.yaml"), strings.Replace(string(data), "WIKI.EXAMPLE.COM.", "moved.example.com", 1))
			case "changed-port":
				data, _ := os.ReadFile(filepath.Join(dir, "infrafile.yaml"))
				writeImportFile(t, filepath.Join(dir, "infrafile.yaml"), strings.Replace(string(data), "host: WIKI.EXAMPLE.COM.", "host: WIKI.EXAMPLE.COM., port: 8443", 1))
			case "changed-snapshot":
				data, _ := os.ReadFile(filepath.Join(dir, "state/tls/wiki.yaml"))
				writeImportFile(t, filepath.Join(dir, "state/tls/wiki.yaml"), string(data)+"# changed\n")
			default:
				status, err := snapshot.ReadStatus(context.Background(), dir)
				if err != nil {
					t.Fatal(err)
				}
				a := status.Spec["tls"]["wiki"]
				if mode == "failed" || mode == "running" {
					f.clock = f.clock.Add(time.Minute)
					a.AttemptedAt = f.clock.Format(time.RFC3339)
					a.Status = mode
					a.SnapshotHash = ""
					if mode == "failed" {
						a.Code = "probe"
					}
				} else {
					file := filepath.Join(dir, "state/tls/wiki.yaml")
					data, _ := os.ReadFile(file)
					if mode == "untrusted" || mode == "hostname-mismatch" || mode == "expired-or-not-yet-valid" {
						data = []byte(strings.ReplaceAll(strings.ReplaceAll(string(data), "verified: true", "verified: false"), "verification: valid", "verification: "+mode))
					}
					if mode == "expired" {
						data = []byte(strings.ReplaceAll(string(data), "2026-12-31T12:00:00Z", "2026-11-30T12:00:00Z"))
					}
					if mode == "today" {
						data = []byte(strings.ReplaceAll(string(data), "2026-12-31T12:00:00Z", "2026-12-01T12:00:00Z"))
					}
					writeImportFile(t, file, string(data))
					a.SnapshotHash = fmt.Sprintf("%x", sha256.Sum256(data))
				}
				status.Spec["tls"]["wiki"] = a
				data, _ := yaml.Marshal(status)
				writeImportFile(t, filepath.Join(dir, "state", snapshot.StatusFile), string(data))
			}
			f.login(t)
			result := certificateResult(t, f, "{}")
			if mode == "no-snapshots" {
				if result["has_snapshots"] != false || len(result["groups"].([]any)) != 0 || result["message"] != "没有 tls 探测快照 · 不代表证书正常" {
					t.Fatal(result)
				}
				return
			}
			row := certificateRows(result)["wiki.example.com"]
			if mode == "changed-config" {
				row = certificateRows(result)["moved.example.com"]
			}
			want := "unverified"
			switch mode {
			case "stale":
				want = "stale"
			case "future":
				want = "future"
			case "failed", "running":
				want = mode
			case "changed-config", "changed-port":
				want = "uncollected"
			case "untrusted", "hostname-mismatch", "expired-or-not-yet-valid", "expired", "today":
				want = "fresh"
			}
			if row["data_state"] != want {
				t.Fatal(mode, row)
			}
			if want != "fresh" && row["expiry_state"] != "unknown" {
				t.Fatal("historical evidence shown as normal", row)
			}
			if mode == "stale" && row["stale"] != true {
				t.Fatal(row)
			}
			if (mode == "untrusted" || mode == "hostname-mismatch" || mode == "expired-or-not-yet-valid") && (row["verified"] != false || row["verification"] != mode || row["expiry_state"] != "expiring") {
				t.Fatal(row)
			}
			if mode == "expired" && (row["days_left"] != float64(-1) || row["expiry_state"] != "expired") {
				t.Fatal(row)
			}
			if mode == "today" && row["expiry_state"] != "today" {
				t.Fatal(row)
			}
		})
	}
}

func TestWorkbenchCertificatesTTLAndRedaction(t *testing.T) {
	f, dir := certificateFixture(t)
	f.clock = f.clock.Add(7 * 24 * time.Hour)
	f.login(t)
	if row := certificateRows(certificateResult(t, f, `{}`))["wiki.example.com"]; row["data_state"] != "fresh" {
		t.Fatal("TTL equality must be fresh", row)
	}
	if row := certificateRows(certificateResult(t, f, `{"max_age_days":6}`))["wiki.example.com"]; row["data_state"] != "stale" {
		t.Fatal(row)
	}
	file := filepath.Join(dir, "state/tls/wiki.yaml")
	data, _ := os.ReadFile(file)
	var saved map[string]any
	if err := yaml.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	spec := saved["spec"].(map[string]any)
	spec["extra"] = map[string]any{"path": "/synthetic/local/private-path", "pem": "-----BEGIN CERTIFICATE-----synthetic", "ref": "op://synthetic/item/key"}
	encoded, _ := yaml.Marshal(saved)
	writeImportFile(t, file, string(encoded))
	out := f.send(f.request("POST", "/api/certificates", "{}"))
	if out.Code != 200 || strings.Contains(out.Body.String(), "private-path") || strings.Contains(out.Body.String(), "subject") || strings.Contains(out.Body.String(), dir) || strings.Contains(out.Body.String(), "BEGIN CERTIFICATE") || strings.Contains(out.Body.String(), "op://") {
		t.Fatal(out.Code, out.Body.String())
	}
	for _, secret := range []string{"private_key", "credential_ref", "raw"} {
		spec[secret] = map[string]string{"private_key": "synthetic-private-key", "credential_ref": "sops://synthetic/key", "raw": "-----BEGIN CERTIFICATE-----synthetic"}[secret]
		encoded, _ = yaml.Marshal(saved)
		writeImportFile(t, file, string(encoded))
		out := f.send(f.request("POST", "/api/certificates", "{}"))
		if (out.Code != 200 && out.Code != 422) || (secret == "private_key" && out.Code != 422) || strings.Contains(out.Body.String(), "synthetic-private-key") || strings.Contains(out.Body.String(), "sops://") || strings.Contains(out.Body.String(), "BEGIN CERTIFICATE") || strings.Contains(out.Body.String(), dir) {
			t.Fatal(out.Code, out.Body.String())
		}
		delete(spec, secret)
	}
}

func TestWorkbenchCertificatesResultLimit(t *testing.T) {
	f, dir := certificateFixture(t)
	f.login(t)
	var domain strings.Builder
	domain.WriteString("apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.net}\nspec:\n  bindings:\n")
	for n := range 13000 {
		fmt.Fprintf(&domain, "    - {name: '%s.%s.%d', app: wiki}\n", strings.Repeat("a", 63), strings.Repeat("b", 63), n)
	}
	writeImportFile(t, filepath.Join(dir, "inventory/domains/example.net.yaml"), domain.String())
	result, err := workbenchCall(context.Background(), dir, "certificates", mcpArgs{}, f.clock)
	encoded, marshalErr := json.Marshal(result)
	if err != nil || marshalErr != nil || len(encoded) <= 4<<20 {
		t.Fatal("fixture did not reach result limit", err, marshalErr, len(encoded))
	}
	out := f.send(f.request("POST", "/api/certificates", "{}"))
	if out.Code != 422 || out.Body.Len() > 1024 {
		t.Fatal(out.Code, out.Body.Len())
	}
}
