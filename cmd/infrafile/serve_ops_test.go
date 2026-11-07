package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func opsFixture(t *testing.T) (*webFixture, opsOverview, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	f := newWebFixture(t, dir)
	repo, err := loadQueryRepo(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	stamp := f.clock.Add(-time.Second).Format(time.RFC3339)
	evidence := opsEvidence{Status: "PASS", Reason: "saved", ObservedAt: &stamp, Source: "fleet", MaxAgeSeconds: 60}
	o := opsOverview{Version: opsVersion, CapturedAt: stamp, AuthorityFiles: map[string]string{}, LedgerFiles: map[string]string{}, CollectionErrors: []string{}}
	for _, path := range append([]string{"infrafile.yaml"}, repo.Files...) {
		data, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		o.LedgerFiles[path] = hex.EncodeToString(hash[:])
	}
	for _, record := range repo.Records {
		if record.Archived() {
			continue
		}
		key := record.Kind + "/" + record.Name
		switch record.Kind {
		case "Server":
			o.Servers = append(o.Servers, opsServer{Key: key, Name: record.Name, Snapshot: evidence, Hardware: map[string]string{}, Applications: []string{}, Disks: []opsDisk{}})
		case "App":
			o.Applications = append(o.Applications, opsApplication{Key: key, Name: record.Name, Deployments: []string{}})
		case "Resource":
			o.Resources = append(o.Resources, opsResource{Key: key, Name: record.Name, Hosts: []string{}})
		}
	}
	for i := range o.Applications {
		id := o.Applications[i].Name + "-deploy"
		o.Deployments = append(o.Deployments, opsDeployment{ID: id, Application: o.Applications[i].Key, Host: o.Servers[0].Key, Runtime: evidence, Readiness: evidence, Health: opsEvidence{Status: "UNKNOWN", Reason: "business_health_not_verified", Source: "policy", MaxAgeSeconds: 60}, Resources: []string{}, Checks: []string{}})
		o.Applications[i].Deployments = []string{id}
	}
	active := true
	interval := 60
	o.Monitors = []opsMonitor{{ID: 1, Name: "check", Type: "push", Active: &active, Interval: &interval, Targets: []string{o.Applications[0].Key}, Configuration: evidence, State: evidence}}
	o.Summary = opsSummary{Servers: len(o.Servers), Applications: len(o.Applications), Resources: len(o.Resources), Deployments: len(o.Deployments), ExpectedMonitors: 1, ObservedMonitors: 1, RuntimeIdentified: len(o.Deployments)}
	authority := t.TempDir()
	data := []byte("current source\n")
	if err := os.WriteFile(filepath.Join(authority, "servers.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	o.AuthorityFiles["servers.yaml"] = hex.EncodeToString(hash[:])
	f.app.opsAuthority = authority
	f.app.opsAuthorityInfo, _ = os.Lstat(authority)
	path := filepath.Join(t.TempDir(), "ops.json")
	f.app.opsFile = path
	writeOps(t, path, o)
	return f, o, path
}

func writeOps(t *testing.T, path string, o opsOverview) {
	t.Helper()
	raw, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func readOps(t *testing.T, f *webFixture) opsOverview {
	t.Helper()
	out := f.send(f.request("POST", "/api/ops-overview", "{}"))
	if out.Code != 200 {
		t.Fatal("Ops read failed", out.Code, out.Body.String())
	}
	var o opsOverview
	if err := json.Unmarshal(out.Body.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOpsAuthenticationContractAndReadOnly(t *testing.T) {
	f, _, path := opsFixture(t)
	if out := f.send(f.request("POST", "/api/ops-overview", "{}")); out.Code != 401 {
		t.Fatal(out.Code)
	}
	f.login(t)
	before, _ := os.ReadFile(path)
	o := readOps(t, f)
	if !o.Enabled || o.Alignment != "matched" || o.AuthorityFiles != nil || o.LedgerFiles != nil {
		t.Fatal("source binding leaked or not matched")
	}
	if o.Deployments[0].Health.Status != "UNKNOWN" {
		t.Fatal("running evidence implied business health")
	}
	for _, body := range []string{`{"command":"restart"}`, `{"path":"/outside"}`, `{"key":"Server/web1"}`, `{"command":1,"command":2}`} {
		if out := f.send(f.request("POST", "/api/ops-overview", body)); out.Code != 400 {
			t.Fatal("unexpected input accepted", body, out.Code)
		}
	}
	r := f.request("POST", "/api/ops-overview", "{}")
	r.Header.Del("X-CSRF-Token")
	if out := f.send(r); out.Code != 403 {
		t.Fatal(out.Code)
	}
	if out := f.send(f.request("GET", "/ops", "")); out.Code != 200 || !strings.Contains(out.Body.String(), `data-ops="true"`) {
		t.Fatal("page not attached")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("read mutated snapshot")
	}
}

func TestOpsRejectsNullCollectionsBeforeReturningBrowserData(t *testing.T) {
	f, original, path := opsFixture(t)
	f.login(t)
	for name, mutate := range map[string]func(*opsOverview){
		"errors":          func(o *opsOverview) { o.CollectionErrors = nil },
		"hardware":        func(o *opsOverview) { o.Servers[0].Hardware = nil },
		"server apps":     func(o *opsOverview) { o.Servers[0].Applications = nil },
		"disks":           func(o *opsOverview) { o.Servers[0].Disks = nil },
		"app deployments": func(o *opsOverview) { o.Applications[0].Deployments = nil },
		"resource hosts":  func(o *opsOverview) { o.Resources[0].Hosts = nil },
		"dependencies":    func(o *opsOverview) { o.Deployments[0].Resources = nil },
		"checks":          func(o *opsOverview) { o.Deployments[0].Checks = nil },
		"monitor targets": func(o *opsOverview) { o.Monitors[0].Targets = nil },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			var o opsOverview
			if err := json.Unmarshal(raw, &o); err != nil {
				t.Fatal(err)
			}
			mutate(&o)
			writeOps(t, path, o)
			if out := f.send(f.request("POST", "/api/ops-overview", "{}")); out.Code != 422 {
				t.Fatal("null collection reached browser", out.Code)
			}
		})
	}
}

func TestOpsAgesAtReadTimeAndPreservesPause(t *testing.T) {
	f, o, path := opsFixture(t)
	f.login(t)
	if readOps(t, f).Monitors[0].State.Status != "PASS" {
		t.Fatal("fresh monitor lost")
	}
	f.clock = f.clock.Add(2 * time.Minute)
	read := readOps(t, f)
	if read.Monitors[0].State.Status != "STALE" || read.Servers[0].Snapshot.Status != "STALE" || read.Deployments[0].Runtime.Status != "STALE" {
		t.Fatal("old green retained")
	}
	paused := false
	o.Monitors[0].Active = &paused
	o.Monitors[0].State.Status = "PAUSED"
	writeOps(t, path, o)
	read = readOps(t, f)
	if read.Monitors[0].State.Status != "PAUSED" || read.Monitors[0].Configuration.Status != "STALE" {
		t.Fatal("paused monitor became outage or fresh")
	}
	future := f.clock.Add(time.Hour).Format(time.RFC3339)
	o.Servers[0].Snapshot.ObservedAt = &future
	writeOps(t, path, o)
	if readOps(t, f).Servers[0].Snapshot.Status != "UNKNOWN" {
		t.Fatal("future evidence accepted")
	}
}

func TestOpsRejectsMixedLedgerAndMarksAuthorityChanges(t *testing.T) {
	f, o, path := opsFixture(t)
	f.login(t)
	if err := os.WriteFile(filepath.Join(f.app.opsAuthority, "servers.yaml"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if readOps(t, f).Alignment != "changed" {
		t.Fatal("authority change hidden")
	}
	for p := range o.LedgerFiles {
		o.LedgerFiles[p] = strings.Repeat("0", 64)
		break
	}
	writeOps(t, path, o)
	if out := f.send(f.request("POST", "/api/ops-overview", "{}")); out.Code != 422 {
		t.Fatal("mixed generation accepted", out.Code)
	}
}

func TestOpsRejectsSymlinksUnknownFieldsAndUnsafeOutput(t *testing.T) {
	f, o, path := opsFixture(t)
	f.login(t)
	for _, bad := range []func(){
		func() {
			raw, _ := json.Marshal(o)
			raw = append(raw[:len(raw)-1], []byte(`,"extra":"rejected"}`)...)
			os.WriteFile(path, raw, 0600)
		},
		func() {
			bad := o
			bad.Servers = append([]opsServer{}, o.Servers...)
			bad.Servers[0].Name = "192.0.2.1"
			writeOps(t, path, bad)
		},
		func() {
			bad := o
			bad.Monitors = append([]opsMonitor{}, o.Monitors...)
			bad.Monitors[0].Targets = []string{"App/absent"}
			writeOps(t, path, bad)
		},
		func() { bad := o; bad.Summary.Servers++; writeOps(t, path, bad) },
	} {
		bad()
		if out := f.send(f.request("POST", "/api/ops-overview", "{}")); out.Code != 422 {
			t.Fatal("bad Ops data accepted", out.Code)
		}
		writeOps(t, path, o)
	}
	target := path + ".real"
	if err := os.Rename(path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if out := f.send(f.request("POST", "/api/ops-overview", "{}")); out.Code != 422 {
		t.Fatal("symlink accepted")
	}
}

func TestOpsAbsentAttachmentIsExplicit(t *testing.T) {
	f, _, _ := opsFixture(t)
	f.app.opsFile = ""
	f.login(t)
	if readOps(t, f).Enabled {
		t.Fatal("empty data became healthy overview")
	}
}

func TestOpsModeRefusesEditingAndRemoteAccessBeforeStarting(t *testing.T) {
	for _, flags := range [][]string{{"--allow-edit"}, {"--external-url", "https://ops.example.test", "--trusted-proxy", "127.0.0.1"}} {
		cmd := newRoot(time.Now())
		out := &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(out)
		cmd.SetArgs(append([]string{"serve", "--ops-overview", "unopened.json"}, flags...))
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "运维视图仅限本机回环只读") {
			t.Fatal("Ops scope expanded", err)
		}
	}
}

func TestOpsReaderRequiresLocalOverviewAndAuthorityBeforeStarting(t *testing.T) {
	for _, flags := range [][]string{{}, {"--ops-overview", "unopened.json"}} {
		cmd := newRoot(time.Now())
		out := &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(out)
		cmd.SetArgs(append([]string{"serve", "--ops-reader", "unopened-reader.json"}, flags...))
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "按需读取需要本机只读运维视图及固定权威来源") {
			t.Fatal("reader scope expanded", err)
		}
		if strings.Contains(out.String(), "配对码") {
			t.Fatal("invalid reader scope published pairing")
		}
	}
}

func TestOpsCriteriaAgeIndependentlyWithoutPromotingBusinessHealth(t *testing.T) {
	f, o, path := opsFixture(t)
	d := &o.Deployments[0]
	d.Checks = []string{"saved-service", "manual-check"}
	d.Criteria = []opsCriterion{
		{ID: "saved-service", Component: "core", Kind: "fleet_systemd", Layer: "runtime", WorkloadMode: "service", DataMode: "stateless", Selector: "core.service", Observer: d.Host, Evidence: d.Runtime},
		{ID: "manual-check", Component: "manual", Kind: "manual", Layer: "unverified", WorkloadMode: "control", DataMode: "stateful", Observer: d.Host, Evidence: opsEvidence{Status: "UNKNOWN", Reason: "manual_required", Source: "app-policy", MaxAgeSeconds: 86400}},
	}
	writeOps(t, path, o)
	f.login(t)
	read := readOps(t, f)
	if read.Deployments[0].Criteria[0].Evidence.Status != "PASS" || read.Deployments[0].Criteria[1].Evidence.Status != "UNKNOWN" || read.Deployments[0].Health.Status != "UNKNOWN" {
		t.Fatal("criterion confused policy with health")
	}
	f.clock = f.clock.Add(61 * time.Second)
	read = readOps(t, f)
	if read.Deployments[0].Criteria[0].Evidence.Status != "STALE" || read.Deployments[0].Criteria[1].Evidence.Status != "UNKNOWN" {
		t.Fatal("criterion observation age lost")
	}
}

func TestOpsCriteriaRejectWrongIdentityLayerAndConfiguredGreen(t *testing.T) {
	f, o, _ := opsFixture(t)
	d := &o.Deployments[0]
	d.Checks = []string{"configured"}
	d.Criteria = []opsCriterion{{ID: "configured", Component: "job", Kind: "systemd_timer", Layer: "schedule", WorkloadMode: "scheduled", DataMode: "stateless", Selector: "job.timer", Observer: d.Host, Evidence: opsEvidence{Status: "UNKNOWN", Reason: "remote_probe_not_requested", Source: "app-policy", MaxAgeSeconds: 86400}}}
	tests := map[string]func(*opsDeployment){
		"foreign observer":      func(d *opsDeployment) { d.Criteria[0].Observer = "Server/unknown" },
		"unchecked id":          func(d *opsDeployment) { d.Criteria[0].ID = "other" },
		"wrong layer":           func(d *opsDeployment) { d.Criteria[0].Layer = "runtime" },
		"policy supplies green": func(d *opsDeployment) { d.Criteria[0].Evidence = d.Runtime },
		"policy claims observation": func(d *opsDeployment) {
			d.Criteria[0].Evidence.Status = "PASS"
			d.Criteria[0].Evidence.ObservedAt = d.Runtime.ObservedAt
		},
		"duplicate criterion": func(d *opsDeployment) {
			d.Criteria = append(d.Criteria, d.Criteria[0])
			d.Checks = append(d.Checks, "other")
		},
		"missing detail":   func(d *opsDeployment) { d.Checks = append(d.Checks, "other") },
		"invalid workload": func(d *opsDeployment) { d.Criteria[0].WorkloadMode = "arbitrary" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(o)
			var changed opsOverview
			json.Unmarshal(raw, &changed)
			mutate(&changed.Deployments[0])
			if err := validateOps(&changed, f.clock); err == nil {
				t.Fatal("invalid criterion accepted")
			}
		})
	}
}
