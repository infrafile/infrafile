package audit

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/snapshot"
	"go.yaml.in/yaml/v3"
)

const inspectConfig = `plugins:
  aliyun:
    instances:
      home: {services: [domains]}
  dns:
    instances:
      public: {host: www.example.com}
audit:
  watch:
    - {target: Domain/example.com, plugin: aliyun, instance: home, service: domains}
`

func inspectRepo(t *testing.T) *ledger.Repo {
	t.Helper()
	return repo(t, inspectConfig, map[string]string{
		"inventory/domains/example.com.yaml": "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec: {bindings: [{name: www, server: web}]}\n",
		"inventory/servers/web.yaml":         "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: web}\nspec: {address: 192.0.2.10}\n",
	})
}

func inspectEvidence(t *testing.T, r *ledger.Repo, p, name string, spec map[string]any, at time.Time, state string) {
	t.Helper()
	content, err := yaml.Marshal(map[string]any{"apiVersion": ledger.APIVersion, "kind": "Snapshot", "metadata": map[string]any{"name": name}, "spec": spec})
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.Dir, "state/"+p+"/"+name+".yaml", string(content))
	stamps, err := snapshot.ReadSync(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if stamps[p] == nil {
		stamps[p] = map[string]time.Time{}
	}
	stamps[p][name] = at
	data, _ := yaml.Marshal(map[string]any{"apiVersion": ledger.APIVersion, "kind": "Sync", "spec": stamps})
	write(t, r.Dir, "state/_sync.yaml", string(data))
	status, err := snapshot.ReadStatus(context.Background(), r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if status == nil {
		status = &snapshot.Status{APIVersion: ledger.APIVersion, Kind: "SyncStatus", Spec: map[string]map[string]snapshot.Attempt{}}
	}
	if status.Spec[p] == nil {
		status.Spec[p] = map[string]snapshot.Attempt{}
	}
	config := field(field(field(r.Config.Value, "plugins"), p), "instances").(map[string]any)[name]
	a := snapshot.Attempt{AttemptedAt: at.Format(time.RFC3339), Status: state, ConfigHash: plugin.Hash(config)}
	if state == "succeeded" {
		a.SnapshotHash = fmt.Sprintf("%x", sha256.Sum256(content))
	}
	if state == "failed" {
		a.Code = "http"
	}
	status.Spec[p][name] = a
	data, _ = yaml.Marshal(status)
	write(t, r.Dir, "state/"+snapshot.StatusFile, string(data))
}

func inspectBaseline(t *testing.T, r *ledger.Repo) {
	t.Helper()
	watches, problems := observation.Watches(r)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	data, _ := yaml.Marshal(snapshot.Presence{APIVersion: ledger.APIVersion, Kind: "Presence", Spec: map[string]snapshot.PresenceEntry{watches[0].Target: {BindingHash: plugin.Hash(watches[0]), LastSeen: now.Add(-time.Hour).Format(time.RFC3339)}}})
	write(t, r.Dir, "state/"+snapshot.PresenceFile, string(data))
}

func inspectDomain(t *testing.T, r *ledger.Repo, age *int) *Inspection {
	t.Helper()
	got, err := Inspect(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: age, Keys: []string{"Domain/example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func domainObservations() map[string]any {
	return map[string]any{"domains": []any{map[string]any{"name": "example.com"}}}
}
func dnsObservations() map[string]any {
	return map[string]any{"host": "www.example.com", "addresses": []any{"192.0.2.20"}}
}

func TestInspectCoverageStateTableAndStableDenominator(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"fresh", "checked"}, {"absent", "checked"}, {"failed", "failed"}, {"running", "running"}, {"stale", "stale"},
		{"future", "future"}, {"no-baseline", "not_baselined"}, {"incomplete", "incomplete"}, {"no-snapshot", "unverified"},
		{"changed-bytes", "unverified"}, {"no-ttl", "ttl_unset"}, {"boundary", "checked"}, {"beyond-boundary", "stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := inspectRepo(t)
			spec := domainObservations()
			at := now
			state := "succeeded"
			age := days(7)
			if tc.name == "absent" {
				spec["domains"] = []any{}
			}
			if tc.name == "incomplete" {
				spec = map[string]any{"cdn": []any{}}
			}
			if tc.name == "failed" || tc.name == "running" {
				state = tc.name
			}
			if tc.name == "stale" {
				at = now.Add(-8 * 24 * time.Hour)
			}
			if tc.name == "future" {
				at = now.Add(time.Second)
			}
			if tc.name == "boundary" {
				at = now.Add(-7 * 24 * time.Hour)
			}
			if tc.name == "beyond-boundary" {
				at = now.Add(-7*24*time.Hour - time.Second)
			}
			if tc.name == "no-ttl" {
				age = nil
			}
			inspectEvidence(t, r, "aliyun", "home", spec, at, state)
			inspectEvidence(t, r, "dns", "public", dnsObservations(), now, "succeeded")
			if tc.name != "no-baseline" {
				inspectBaseline(t, r)
				if at.Before(now.Add(-time.Hour)) {
					data, _ := os.ReadFile(filepath.Join(r.Dir, "state/"+snapshot.PresenceFile))
					write(t, r.Dir, "state/"+snapshot.PresenceFile, strings.ReplaceAll(string(data), now.Add(-time.Hour).Format(time.RFC3339), at.Add(-time.Hour).Format(time.RFC3339)))
				}
			}
			if tc.name == "no-snapshot" {
				if err := os.Remove(filepath.Join(r.Dir, "state/aliyun/home.yaml")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "changed-bytes" {
				data, _ := os.ReadFile(filepath.Join(r.Dir, "state/aliyun/home.yaml"))
				write(t, r.Dir, "state/aliyun/home.yaml", string(data)+"# user edit\n")
			}
			got := inspectDomain(t, r, age)
			if got.Coverage.Expected != 2 || len(got.Execution) != 2 || len(got.Relations) != 1 {
				t.Fatalf("contract shrank: %+v", got)
			}
			var check Check
			for _, c := range got.Coverage.Checks {
				if c.Rule == "missing" {
					check = c
				}
			}
			if check.Code != tc.code {
				t.Fatalf("code=%s want=%s report=%+v", check.Code, tc.code, got)
			}
			wantOK := tc.name == "fresh" || tc.name == "boundary"
			if got.OK != wantOK {
				t.Fatalf("ok=%t want=%t: %+v", got.OK, wantOK, got)
			}
			if tc.name == "absent" && (got.Risk.Confirmed != 1 || got.Risk.Findings[0].Rule != "missing") {
				t.Fatalf("absence: %+v", got.Risk)
			}
		})
	}
}

func TestInspectEmptyAndUnconfiguredNeverGreen(t *testing.T) {
	for _, r := range []*ledger.Repo{repo(t, "", nil), repo(t, "", map[string]string{"inventory/domains/example.com.yaml": "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec: {}"})} {
		got, err := Inspect(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: days(7)})
		if err != nil {
			t.Fatal(err)
		}
		if got.OK || got.Risk.Confirmed != 0 || got.Risk.Unknown != 0 {
			t.Fatalf("empty findings became green: %+v", got)
		}
		if len(r.Records) > 0 && (got.Coverage.Expected != 2 || got.Coverage.Unknown != 2) {
			t.Fatal(got.Coverage)
		}
	}
}

func TestInspectMultipleAccountsDoNotInferWatch(t *testing.T) {
	r := repo(t, "plugins:\n  aliyun:\n    instances:\n      one: {services: [domains]}\n      two: {services: [domains]}\n", map[string]string{"inventory/domains/example.com.yaml": "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec: {}"})
	got := inspectDomain(t, r, days(7))
	found := false
	for _, c := range got.Coverage.Checks {
		if c.Rule == "missing" {
			found = c.Code == "ambiguous_scope" && c.Plugin == "" && c.Instance == ""
		}
	}
	if !found || got.OK {
		t.Fatalf("inferred account: %+v", got)
	}
}

func TestInspectHistoricalRiskCannotBorrowAnotherAccountSuccess(t *testing.T) {
	r := repo(t, `plugins:
  aliyun:
    instances:
      one: {services: [cdn]}
      two: {services: [cdn]}
audit:
  watch:
    - {target: Resource/edge, plugin: aliyun, instance: one, service: cdn}
`, map[string]string{"inventory/resources/edge.yaml": "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: edge}\nspec: {provider: aliyun, type: cdn, endpoint: edge.example.com}"})
	for _, name := range []string{"one", "two"} {
		state := "succeeded"
		if name == "one" {
			state = "failed"
		}
		inspectEvidence(t, r, "aliyun", name, map[string]any{"cdn": []any{map[string]any{"name": "edge.example.com", "status": "offline"}}}, now, state)
	}
	got, err := Inspect(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: days(7), Keys: []string{"Resource/edge"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Risk.Unknown != 2 || got.Risk.Confirmed != 0 {
		t.Fatalf("borrowed success: %+v", got)
	}
	if len(got.Execution) != 1 || got.Execution[0].Instance != "one" || got.Execution[0].Status != "failed" {
		t.Fatal(got.Execution)
	}
}

func TestInspectNAPreservesDenominatorRiskAndExpiry(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			r := inspectRepo(t)
			inspectEvidence(t, r, "aliyun", "home", domainObservations(), now, "succeeded")
			inspectEvidence(t, r, "dns", "public", dnsObservations(), now, "succeeded")
			inspectBaseline(t, r)
			first := inspectDomain(t, r, days(7))
			id := first.Coverage.Checks[0].ID
			for _, c := range first.Coverage.Checks {
				if c.Rule == "missing" {
					id = c.ID
				}
			}
			value := r.Config.Value.(map[string]any)
			value["storageVersion"] = "v1"
			audit := value["audit"].(map[string]any)
			deadline := "2026-12-10"
			if expired {
				deadline = "2026-11-30"
			}
			audit["not_applicable"] = []any{map[string]any{"check": id, "reason": "separate reviewed process", "owner": "test maintainer", "review_by": deadline}}
			got := inspectDomain(t, r, days(7))
			if got.Coverage.Expected != first.Coverage.Expected || (got.Coverage.NotApplicable == 1) == expired {
				t.Fatal(got.Coverage)
			}
			// A valid NA cannot hide a positively confirmed missing finding.
			inspectEvidence(t, r, "aliyun", "home", map[string]any{"domains": []any{}}, now, "succeeded")
			got = inspectDomain(t, r, days(7))
			if got.OK || got.Risk.Confirmed != 1 {
				t.Fatalf("NA hid risk: %+v", got)
			}
			inspectEvidence(t, r, "aliyun", "home", domainObservations(), now, "failed")
			got = inspectDomain(t, r, days(7))
			if got.OK || got.Execution[0].Status != "failed" {
				t.Fatalf("NA hid execution failure: %+v", got)
			}
		})
	}
}

func TestInspectStableIDsAndSourceContracts(t *testing.T) {
	scope := CheckScope{"missing", "Domain/example.com", "aliyun", "one", "domains", "presence"}
	other := scope
	other.Instance = "two"
	if scope.ID() == other.ID() {
		t.Fatal("accounts share ID")
	}
	other = scope
	other.Aspect = "origin"
	if scope.ID() == other.ID() {
		t.Fatal("aspects share ID")
	}
	r := inspectRepo(t)
	inspectEvidence(t, r, "aliyun", "home", domainObservations(), now, "succeeded")
	inspectBaseline(t, r)
	inspectEvidence(t, r, "dns", "public", dnsObservations(), now, "succeeded")
	first := inspectDomain(t, r, days(7))
	spec := dnsObservations()
	spec["addresses"] = []any{"192.0.2.10"}
	inspectEvidence(t, r, "dns", "public", spec, now, "succeeded")
	second := inspectDomain(t, r, days(7))
	for index, c := range first.Coverage.Checks {
		if c.ID != second.Coverage.Checks[index].ID {
			t.Fatal("observed value changed denominator identity")
		}
	}
	if second.Risk.Confirmed != 1 || second.Risk.Findings[0].Rule != "origin_exposure" {
		t.Fatal(second.Risk)
	}
}

func TestInspectReadOnlyCancellationAndCorruption(t *testing.T) {
	r := inspectRepo(t)
	inspectEvidence(t, r, "aliyun", "home", domainObservations(), now, "succeeded")
	inspectBaseline(t, r)
	inspectEvidence(t, r, "dns", "public", dnsObservations(), now, "succeeded")
	files := func() map[string]string {
		out := map[string]string{}
		if err := filepath.WalkDir(r.Dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				out[p] = fmt.Sprintf("%x", sha256.Sum256(b))
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := files()
	inspectDomain(t, r, days(7))
	if !reflect.DeepEqual(before, files()) {
		t.Fatal("inspection wrote files")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Inspect(ctx, r, InspectOptions{}); err != context.Canceled {
		t.Fatal(err)
	}
	write(t, r.Dir, "state/"+snapshot.PresenceFile, "password: synthetic-broken-state")
	if _, err := Inspect(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: days(7)}); err == nil || strings.Contains(err.Error(), "synthetic-broken-state") {
		t.Fatalf("corruption: %v", err)
	}
}

func TestInspectFullScopeIncludesConfiguredEmptyAndMissingInstances(t *testing.T) {
	r := inspectRepo(t)
	inspectEvidence(t, r, "aliyun", "home", map[string]any{"domains": []any{}}, now, "succeeded")
	got, err := Inspect(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: days(7)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Execution) != 2 {
		t.Fatal(got.Execution)
	}
	found := false
	for _, c := range got.Coverage.Checks {
		if c.Target == "dns/public" && c.Rule == "stale_state" {
			found = c.Code == "unverified"
		}
	}
	if !found || got.OK {
		t.Fatal(got)
	}
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), `"observed_at":null`) {
		t.Fatal(string(b))
	}
}

func TestInspectPartialOriginCoverageKeepsConfirmedHostDrift(t *testing.T) {
	r := repo(t, `plugins:
  aliyun:
    instances:
      home: {services: [cdn], collect_origin: true}
audit:
  watch:
    - {target: Resource/edge, plugin: aliyun, instance: home, service: cdn}
`, map[string]string{"inventory/resources/edge.yaml": "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: edge}\nspec: {provider: aliyun, type: cdn, endpoint: edge.example.com, origin: {hosts: [origin.example.net], port: 443, protocol: https}}"})
	inspectEvidence(t, r, "aliyun", "home", map[string]any{"cdn": []any{map[string]any{"name": "edge.example.com", "status": "online", "origin": map[string]any{"scope": "global", "sources": []any{map[string]any{"host": "old.example.net"}}}}}}, now, "succeeded")
	inspectBaseline(t, r)
	got, err := Inspect(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: days(7), Keys: []string{"Resource/edge"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Coverage.Expected != 5 || got.Coverage.Checked != 3 || got.Coverage.Unknown != 2 || got.Risk.Confirmed != 1 || got.Risk.Unknown != 0 {
		t.Fatalf("partial evidence: %+v", got)
	}
	for _, c := range got.Coverage.Checks {
		if c.Aspect == "origin/hosts" && c.Code != "checked" || c.Aspect == "origin/port" && c.Code != "observation_unknown" {
			t.Fatal(c)
		}
	}
}

func TestInspectBindingReorderKeepsCheckIDs(t *testing.T) {
	r := inspectRepo(t)
	// Locate the Domain independently of directory enumeration order.
	var domain *ledger.Record
	for _, record := range r.Records {
		if record.Kind == "Domain" {
			domain = record
		}
	}
	bindings := field(field(domain.Value, "spec"), "bindings")
	list := bindings.([]any)
	list = append(list, map[string]any{"name": "api", "server": "web"})
	domain.Value.(map[string]any)["spec"].(map[string]any)["bindings"] = list
	first := inspectDomain(t, r, days(7))
	list[0], list[1] = list[1], list[0]
	second := inspectDomain(t, r, days(7))
	if len(first.Coverage.Checks) != len(second.Coverage.Checks) {
		t.Fatal("reordering changed denominator")
	}
	for index, c := range first.Coverage.Checks {
		if c.ID != second.Coverage.Checks[index].ID {
			t.Fatal("array order changed check identity")
		}
	}
}
