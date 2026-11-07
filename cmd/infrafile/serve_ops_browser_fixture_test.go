package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Reuse the validated unit fixture so browser acceptance needs no private ledger.
func TestExportOpsBrowserFixture(t *testing.T) {
	root := os.Getenv("INFRAFILE_OPS_FIXTURE_DIR")
	if root == "" {
		t.Skip("only invoked by the Ops browser runner")
	}
	f, o, _ := opsFixture(t)
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"bundle/candidate", "bundle/source"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.CopyFS(filepath.Join(root, "bundle/candidate"), os.DirFS(f.app.dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(root, "bundle/source"), os.DirFS(f.app.opsAuthority)); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	saved := opsEvidence{Status: "PASS", Reason: "saved", ObservedAt: &stamp, Source: "fleet", MaxAgeSeconds: 300}
	policy := opsEvidence{Status: "UNKNOWN", Reason: "remote_probe_not_requested", Source: "app-policy", MaxAgeSeconds: 300}
	unmanaged := []byte(`{"apiVersion":"infrafile/v1alpha1","kind":"Server","metadata":{"name":"unmanaged"},"spec":{"address":"192.0.2.99"}}`)
	file := "inventory/servers/unmanaged.yaml"
	if err := os.WriteFile(filepath.Join(root, "bundle/candidate", file), unmanaged, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(unmanaged)
	o.LedgerFiles[file] = hex.EncodeToString(digest[:])
	o.Servers = append(o.Servers, opsServer{Key: "Server/unmanaged", Name: "unmanaged", Snapshot: policy, Hardware: map[string]string{}, Applications: []string{}, Disks: []opsDisk{}})
	o.Summary.Servers = len(o.Servers)
	o.CapturedAt = stamp
	for i := range o.Servers {
		o.Servers[i].Snapshot = saved
	}
	for i := range o.Deployments {
		o.Deployments[i].Runtime, o.Deployments[i].Readiness = saved, saved
	}
	o.Deployments[1].Runtime = opsEvidence{Status: "UNKNOWN", Reason: "selector_not_configured", Source: "policy", MaxAgeSeconds: 300}
	first := &o.Deployments[0]
	first.Criteria = []opsCriterion{
		{ID: "saved-container", Component: "web", Kind: "fleet_container_running", Layer: "runtime", WorkloadMode: "service", DataMode: "stateless", Observer: first.Host, Evidence: saved},
		{ID: "scheduled-job", Component: "job", Kind: "systemd_timer", Layer: "schedule", WorkloadMode: "scheduled", DataMode: "stateless", Observer: first.Host, Evidence: policy},
		{ID: "process", Component: "worker", Kind: "process_running", Layer: "runtime", WorkloadMode: "worker", DataMode: "stateless", Observer: first.Host, Evidence: policy},
		{ID: "manual", Component: "review", Kind: "manual", Layer: "unverified", WorkloadMode: "control", DataMode: "stateful", Observer: first.Host, Evidence: policy},
	}
	for _, c := range first.Criteria {
		first.Checks = append(first.Checks, c.ID)
	}
	second := *first
	second.ID, second.Host, second.Kind = "second-web", o.Servers[1].Key, "nginx-proxy"
	second.Criteria, second.Checks = nil, []string{}
	o.Deployments = append(o.Deployments, second)
	o.Applications[0].Deployments = append(o.Applications[0].Deployments, second.ID)
	o.Summary.Deployments, o.Summary.RuntimeIdentified = len(o.Deployments), len(o.Deployments)
	o.Monitors[0].Configuration, o.Monitors[0].State = saved, saved
	paused := o.Monitors[0]
	inactive := false
	paused.ID, paused.Name, paused.Active = 2, "paused-check", &inactive
	paused.State.Status = "PAUSED"
	o.Monitors = append(o.Monitors, paused)
	o.Summary.ExpectedMonitors, o.Summary.ObservedMonitors = 2, 2
	if err := validateOps(&o, time.Now()); err != nil {
		t.Fatal(err)
	}
	writeOps(t, filepath.Join(root, "ops.json"), o)
	check := newWebFixture(t, filepath.Join(root, "bundle/candidate"))
	check.clock = time.Now().UTC()
	check.app.opsFile = filepath.Join(root, "ops.json")
	check.app.opsAuthority = filepath.Join(root, "bundle/source")
	check.app.opsAuthorityInfo, _ = os.Lstat(check.app.opsAuthority)
	if _, err := check.app.opsOverview(context.Background()); err != nil {
		t.Fatal("exported browser fixture", err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(python)
	if err != nil {
		t.Fatal(err)
	}
	config := opsReadConfig{Executable: resolved, Command: []string{python}, Capabilities: []opsReadCapability{}}
	for i, server := range o.Servers[:2] {
		config.Capabilities = append(config.Capabilities, opsReadCapability{ID: []string{"host-read-web", "host-read-standby"}[i], Label: "读取 " + server.Name, Target: server.Key, RelatedKeys: []string{o.Applications[0].Key}, TimeoutSeconds: 2, MaxAgeSeconds: 300, MinIntervalSeconds: 1})
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "reader.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
