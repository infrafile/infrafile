package audit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
)

func issueRepo(t *testing.T) *ledger.Repo {
	t.Helper()
	return repo(t, `plugins:
  aliyun:
    instances:
      home: {services: [domains, cdn], collect_origin: true}
      old: {services: [cdn]}
  dns:
    instances:
      public: {host: www.example.com}
audit:
  watch:
    - {target: Domain/example.com, plugin: aliyun, instance: home, service: domains}
    - {target: Resource/edge, plugin: aliyun, instance: home, service: cdn}
`, map[string]string{
		"inventory/domains/example.com.yaml":     "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec: {bindings: [{name: www, server: web}]}\n",
		"inventory/domains/old.example.net.yaml": "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: old.example.net, archived: {date: '2026-10-01', reason: retired}}\nspec: {}\n",
		"inventory/servers/web.yaml":             "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata: {name: web}\nspec: {address: 192.0.2.10}\n",
		"inventory/resources/edge.yaml":          "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: edge}\nspec: {provider: aliyun, type: cdn, resource_id: edge-id, endpoint: edge.example.com, origin: {hosts: [origin.example.net], port: 443, protocol: https}}\n",
	})
}

func issueObservations(t *testing.T, r *ledger.Repo, at time.Time, broken bool) {
	t.Helper()
	domains := []any{map[string]any{"name": "example.com"}}
	status, origin, port, protocol := "online", "origin.example.net", 443, "https"
	address := "192.0.2.20"
	oldAt := at
	if broken {
		domains = []any{map[string]any{"name": "old.example.net"}, map[string]any{"name": "unmanaged.example.net"}}
		status, origin, port, protocol = "offline", "wrong.example.net", 80, "http"
		address = "192.0.2.10"
		oldAt = at.Add(-8 * 24 * time.Hour)
	}
	inspectEvidence(t, r, "aliyun", "home", map[string]any{"domains": domains, "cdn": []any{map[string]any{"name": "edge.example.com", "id": "edge-id", "status": status, "origin": map[string]any{"scope": "global", "sources": []any{map[string]any{"host": origin, "port": port, "protocol": protocol}}}}}}, at, "succeeded")
	inspectEvidence(t, r, "aliyun", "old", map[string]any{"cdn": []any{}}, oldAt, "succeeded")
	inspectEvidence(t, r, "dns", "public", map[string]any{"host": "www.example.com", "addresses": []any{address}}, at, "succeeded")
}

func assess(t *testing.T, r *ledger.Repo, at time.Time, known []IssueIdentity) *IssueAssessment {
	t.Helper()
	got, err := EvaluateIssues(context.Background(), r, InspectOptions{Now: at, MaxAgeDays: days(7)}, known)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func stateIDs(states []IssueState) []IssueIdentity {
	ids := []IssueIdentity{}
	for _, state := range states {
		ids = append(ids, state.Identity)
	}
	return ids
}

func advance(t *testing.T, previous []IssueState, evals []IssueEvaluation, at time.Time) ([]IssueState, []IssueChange) {
	t.Helper()
	states, changes, err := AdvanceIssues(context.Background(), previous, evals, at)
	if err != nil {
		t.Fatal(err)
	}
	return states, changes
}

func TestIssuesAllRulesThroughRealEvidenceRecoveryAndRecurrence(t *testing.T) {
	r := issueRepo(t)
	issueObservations(t, r, now, true)
	inspectBaseline(t, r)
	first := assess(t, r, now, nil)
	states, changes := advance(t, nil, first.Evaluations, now)
	if len(states) != 9 || len(changes) != 9 {
		t.Fatalf("all six rules/facets: states=%+v evals=%+v", states, first.Evaluations)
	}
	rules := map[string]bool{}
	for _, state := range states {
		rules[state.Identity.Key.Rule] = true
		if state.Lifecycle != "open" || state.Assessment != "confirmed" || state.Occurrences != 1 {
			t.Fatalf("first: %+v", state)
		}
	}
	if len(rules) != 6 {
		t.Fatal(rules)
	}
	// Merely changing messages, observation values and timestamps never changes an issue key.
	in := &Input{Repo: r, Now: now, MaxAge: days(7)}
	for _, f := range first.Inspection.Risk.Findings {
		before := f.Issues
		f.Message = "a different explanation"
		f.File = "renamed-source.yaml"
		f.Evidence["observed_at"] = "2030-01-01T00:00:00Z"
		f.Evidence["status"] = "another-value"
		f.Evidence["address"] = "198.51.100.1"
		if got := issueIdentities(in, f.Finding); !reflect.DeepEqual(before, got) {
			t.Fatalf("mutable values changed identity: %v %v", before, got)
		}
	}
	secondTime := now.Add(time.Hour)
	issueObservations(t, r, secondTime, false)
	priorBytes, _ := json.Marshal(states)
	second := assess(t, r, secondTime, stateIDs(states))
	recovered, changes := advance(t, states, second.Evaluations, secondTime)
	afterBytes, _ := json.Marshal(states)
	if string(priorBytes) != string(afterBytes) {
		t.Fatal("state transition mutated its input")
	}
	if len(recovered) != 9 || len(changes) != 9 {
		t.Fatalf("recovery: %+v %+v", recovered, second.Evaluations)
	}
	for _, state := range recovered {
		if state.Lifecycle != "recovered" || state.Assessment != "clear" || state.FirstSeen != now.Format(time.RFC3339) || state.Occurrences != 1 || state.RecoveredAt != secondTime.Format(time.RFC3339) {
			t.Fatalf("recovery: %+v", state)
		}
	}
	idempotent, changes := advance(t, recovered, second.Evaluations, secondTime)
	if !reflect.DeepEqual(idempotent, recovered) || len(changes) != 0 {
		t.Fatal("repeat is not idempotent")
	}
	thirdTime := secondTime.Add(time.Hour)
	issueObservations(t, r, thirdTime, true)
	third := assess(t, r, thirdTime, stateIDs(recovered))
	recurred, changes := advance(t, recovered, third.Evaluations, thirdTime)
	if len(recurred) != 9 || len(changes) != 9 {
		t.Fatalf("recurrence: %+v %+v", recurred, third.Evaluations)
	}
	for index, state := range recurred {
		if state.Lifecycle != "open" || state.Occurrences != 2 || state.FirstSeen != recovered[index].FirstSeen || state.RecoveredAt != recovered[index].RecoveredAt || !reflect.DeepEqual(state.Identity, recovered[index].Identity) {
			t.Fatalf("lost history: %+v", state)
		}
	}
}

func TestIssueIdentitySeparatesAccountsAspectsAndTypedNaturalIdentity(t *testing.T) {
	key := IssueKey{Rule: "cdn_drift", Target: "Resource/edge", Plugin: "aliyun", Instance: "one", Service: "cdn", Aspect: "origin/hosts"}
	for _, changed := range []IssueKey{{Rule: key.Rule, Target: key.Target, Plugin: key.Plugin, Instance: "two", Service: key.Service, Aspect: key.Aspect}, {Rule: key.Rule, Target: key.Target, Plugin: key.Plugin, Instance: key.Instance, Service: key.Service, Aspect: "origin/port"}, {Rule: key.Rule, Target: "Resource/other", Plugin: key.Plugin, Instance: key.Instance, Service: key.Service, Aspect: key.Aspect}} {
		if issueFingerprint(changed) == issueFingerprint(key) {
			t.Fatal("distinct scopes share fingerprint")
		}
	}
	a := observation.Identity{Kind: "Resource", Provider: "aliyun", ResourceID: "cdn/edge.example.com"}
	b := observation.Identity{Kind: "Resource", Provider: "aliyun", Type: "id", Endpoint: "cdn/edge.example.com"}
	key.Natural = &a
	one := issueFingerprint(key)
	key.Natural = &b
	if one == issueFingerprint(key) {
		t.Fatal("typed identity collapsed")
	}
}

func TestIssueFailedExpiredWaivedAndNAEvidenceNeverRecovers(t *testing.T) {
	for _, mode := range []string{"failed", "running", "expired", "waived", "na"} {
		t.Run(mode, func(t *testing.T) {
			r := issueRepo(t)
			issueObservations(t, r, now, true)
			inspectBaseline(t, r)
			first := assess(t, r, now, nil)
			previous, _ := advance(t, nil, first.Evaluations, now)
			at := now.Add(time.Hour)
			if mode != "expired" {
				issueObservations(t, r, at, false)
			} else {
				at = now.Add(9 * 24 * time.Hour)
			}
			if mode == "failed" || mode == "running" {
				for _, pair := range [][2]string{{"aliyun", "home"}, {"aliyun", "old"}, {"dns", "public"}} {
					data, err := os.ReadFile(filepath.Join(r.Dir, "state", pair[0], pair[1]+".yaml"))
					if err != nil {
						t.Fatal(err)
					}
					doc, problems, err := ledger.ParseDocument(context.Background(), "snapshot", data)
					if err != nil || len(problems) != 0 {
						t.Fatal(err, problems)
					}
					inspectEvidence(t, r, pair[0], pair[1], field(doc.Value, "spec").(map[string]any), at, mode)
				}
			}
			if mode == "waived" {
				audit := r.Config.Value.(map[string]any)["audit"].(map[string]any)
				ignore := []any{}
				for _, state := range previous {
					k := state.Identity.Key
					if k.Rule == "orphan" || k.Rule == "zombie" {
						ignore = append(ignore, map[string]any{"rule": k.Rule, "plugin": k.Plugin, "instance": k.Instance, "match": identityMap(k.Natural), "reason": "reviewed maintenance"})
					} else if k.Rule == "stale_state" {
						ignore = append(ignore, map[string]any{"rule": k.Rule, "plugin": k.Plugin, "instance": k.Instance, "reason": "reviewed maintenance"})
					} else {
						record := recordFor(r, k.Target)
						md := record.Value.(map[string]any)["metadata"].(map[string]any)
						list, _ := md["waivers"].([]any)
						md["waivers"] = append(list, map[string]any{"rule": k.Rule, "reason": "reviewed maintenance"})
					}
				}
				audit["ignore"] = ignore
			}
			if mode == "na" {
				current := assess(t, r, at, stateIDs(previous))
				entries := []any{}
				for _, check := range current.Inspection.Coverage.Checks {
					entries = append(entries, map[string]any{"check": check.ID, "reason": "external check", "owner": "example-ops", "review_by": "2027-01-01"})
				}
				r.Config.Value.(map[string]any)["storageVersion"] = "v1"
				r.Config.Value.(map[string]any)["audit"].(map[string]any)["not_applicable"] = entries
			}
			current := assess(t, r, at, stateIDs(previous))
			states, changes := advance(t, previous, current.Evaluations, at)
			for _, state := range states {
				if state.Lifecycle == "recovered" || state.RecoveredAt != "" {
					t.Fatalf("%s falsely recovered %+v", mode, state)
				}
			}
			for _, change := range changes {
				if change.Type == "recovered" {
					t.Fatal(change)
				}
			}
		})
	}
}

func identityMap(id *observation.Identity) map[string]any {
	data, _ := json.Marshal(id)
	var value map[string]any
	json.Unmarshal(data, &value)
	return value
}

func TestIssueScopeChangeIsSeparateFromRecovery(t *testing.T) {
	r := issueRepo(t)
	issueObservations(t, r, now, true)
	inspectBaseline(t, r)
	first := assess(t, r, now, nil)
	previous, _ := advance(t, nil, first.Evaluations, now)
	resource := recordFor(r, "Resource/edge")
	resource.Value.(map[string]any)["spec"].(map[string]any)["origin"] = map[string]any{"hosts": []any{"wrong.example.net"}, "port": 80, "protocol": "http"}
	current := assess(t, r, now.Add(time.Hour), stateIDs(previous))
	states, _ := advance(t, previous, current.Evaluations, now.Add(time.Hour))
	oldOrigins := 0
	for _, state := range states {
		if strings.HasPrefix(state.Identity.Key.Aspect, "origin/") {
			oldOrigins++
			if state.Lifecycle != "out_of_scope" || state.RecoveredAt != "" {
				t.Fatalf("changed expectation recovered: %+v", state)
			}
		}
	}
	if oldOrigins != 3 {
		t.Fatal(states)
	}
	// Comments, labels and waiver text are not scope changes.
	r = issueRepo(t)
	issueObservations(t, r, now, true)
	inspectBaseline(t, r)
	a := assess(t, r, now, nil)
	for _, record := range r.Records {
		record.Value.(map[string]any)["metadata"].(map[string]any)["description"] = "updated human note"
	}
	b := assess(t, r, now, nil)
	for index, e := range a.Evaluations {
		if !reflect.DeepEqual(e.Identity, b.Evaluations[index].Identity) {
			t.Fatal("unrelated note invalidated scope")
		}
	}
}

func TestIssueStateTransitionTable(t *testing.T) {
	key := IssueKey{Rule: "stale_state", Target: "aliyun/home", Plugin: "aliyun", Instance: "home", Aspect: "freshness"}
	id := IssueIdentity{IssueVersion, issueFingerprint(key), strings.Repeat("a", 64), key}
	for _, row := range []struct {
		lifecycle, resume string
		want              []string
	}{
		{"open", "open", []string{"open", "recovered", "open", "open", "open", "open"}},
		{"recovered", "recovered", []string{"open", "recovered", "recovered", "open", "recovered", "recovered"}},
		{"out_of_scope", "open", []string{"open", "recovered", "open", "open", "open", "open"}},
		{"out_of_scope", "recovered", []string{"open", "recovered", "recovered", "open", "recovered", "recovered"}},
	} {
		for column, result := range []string{"confirmed", "clear", "unknown", "confirmed", "clear", "unknown"} {
			name := row.lifecycle + "/" + row.resume + "/" + result
			if column >= 3 {
				name += "/waived"
			}
			t.Run(name, func(t *testing.T) {
				prior := IssueState{Identity: id, Lifecycle: row.lifecycle, Resume: row.resume, Assessment: "confirmed", FirstSeen: now.Format(time.RFC3339), LastChecked: now.Format(time.RFC3339), LastConfirmed: now.Format(time.RFC3339), Occurrences: 1}
				if row.resume == "recovered" {
					prior.RecoveredAt = now.Format(time.RFC3339)
				}
				ev := IssueEvaluation{Identity: id, Result: result, InScope: true, Found: result == "confirmed"}
				if column >= 3 {
					ev.Waiver = &Waiver{Reason: "accepted"}
				}
				states, _ := advance(t, []IssueState{prior}, []IssueEvaluation{ev}, now.Add(time.Hour))
				got := states[0]
				if got.Lifecycle != row.want[column] || got.FirstSeen != prior.FirstSeen {
					t.Fatalf("transition: %+v", got)
				}
				occurrences := 1
				if result == "confirmed" && row.resume == "recovered" {
					occurrences = 2
				}
				if got.Occurrences != occurrences {
					t.Fatalf("occurrences: %+v", got)
				}
				if result == "unknown" || column >= 3 {
					if got.RecoveredAt != prior.RecoveredAt {
						t.Fatal("unknown/waiver changed recovery")
					}
				}
			})
		}
		prior := IssueState{Identity: id, Lifecycle: row.lifecycle, Resume: row.resume, Assessment: "confirmed", FirstSeen: now.Format(time.RFC3339), LastChecked: now.Format(time.RFC3339), LastConfirmed: now.Format(time.RFC3339), Occurrences: 1}
		if row.resume == "recovered" {
			prior.RecoveredAt = now.Format(time.RFC3339)
		}
		states, _ := advance(t, []IssueState{prior}, []IssueEvaluation{{Identity: id, Result: "unknown", InScope: false}}, now.Add(time.Hour))
		if states[0].Lifecycle != "out_of_scope" || states[0].Resume != prior.Resume || states[0].RecoveredAt != prior.RecoveredAt {
			t.Fatal(states)
		}
		states, _ = advance(t, []IssueState{prior}, nil, now.Add(time.Hour))
		if states[0].Lifecycle != prior.Lifecycle || states[0].Assessment != "unknown" {
			t.Fatal("omitted check changed resolution")
		}
	}
}

func TestIssueEngineRejectsInvalidAndCancelledInput(t *testing.T) {
	r := issueRepo(t)
	issueObservations(t, r, now, true)
	inspectBaseline(t, r)
	a := assess(t, r, now, nil)
	states, _ := advance(t, nil, a.Evaluations, now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := AdvanceIssues(ctx, states, nil, now); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := EvaluateIssues(ctx, r, InspectOptions{}, nil); err != context.Canceled {
		t.Fatal(err)
	}
	bad := states[0]
	bad.Identity.Version = "v999"
	if _, _, err := AdvanceIssues(context.Background(), []IssueState{bad}, nil, now); err == nil {
		t.Fatal("unknown version accepted")
	}
	if _, _, err := AdvanceIssues(context.Background(), append(states, states[0]), nil, now); err == nil {
		t.Fatal("duplicate history accepted")
	}
	if _, _, err := AdvanceIssues(context.Background(), states, append(a.Evaluations, a.Evaluations[0]), now); err == nil {
		t.Fatal("duplicate evaluation accepted")
	}
	if _, _, err := AdvanceIssues(context.Background(), states, nil, now.Add(-time.Second)); err == nil {
		t.Fatal("backward clock accepted")
	}
	if _, err := EvaluateIssues(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: days(7)}, []IssueIdentity{bad.Identity}); err == nil {
		t.Fatal("bad known identity accepted")
	}
	for _, ev := range []IssueEvaluation{{Identity: states[0].Identity, Result: "clear", Found: true, InScope: true}, {Identity: states[0].Identity, Result: "confirmed", InScope: false, Found: true}} {
		if _, _, err := AdvanceIssues(context.Background(), nil, []IssueEvaluation{ev}, now); err == nil {
			t.Fatal("contradictory evaluation accepted")
		}
	}
	bad = states[0]
	bad.Identity.Key.Aspect = "unknown"
	bad.Identity.Fingerprint = issueFingerprint(bad.Identity.Key)
	if bad.Identity.Validate() == nil {
		t.Fatal("unknown aspect accepted")
	}
	full := states[0]
	full.Lifecycle, full.Resume = "recovered", "recovered"
	full.RecoveredAt = now.Format(time.RFC3339)
	full.Occurrences = int(^uint(0) >> 1)
	if _, _, err := AdvanceIssues(context.Background(), []IssueState{full}, []IssueEvaluation{{Identity: full.Identity, Result: "confirmed", InScope: true, Found: true}}, now.Add(time.Hour)); err == nil {
		t.Fatal("occurrence overflow accepted")
	}
}

func TestIssueIdentityGolden(t *testing.T) {
	r := issueRepo(t)
	issueObservations(t, r, now, true)
	inspectBaseline(t, r)
	assessment := assess(t, r, now, nil)
	ids := []IssueIdentity{}
	for _, e := range assessment.Evaluations {
		ids = append(ids, e.Identity)
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a].Fingerprint < ids[b].Fingerprint })
	data, _ := json.MarshalIndent(ids, "", "  ")
	data = append(data, '\n')
	path := filepath.Join("testdata", "issue-identities.json")
	if os.Getenv("INFRAFILE_UPDATE_ISSUES") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil || string(want) != string(data) {
		t.Fatalf("issue identity contract differs: %v", err)
	}
}

func TestIssueNADoesNotHidePositiveEvidence(t *testing.T) {
	r := issueRepo(t)
	issueObservations(t, r, now, true)
	inspectBaseline(t, r)
	first := assess(t, r, now, nil)
	entries := []any{}
	for _, check := range first.Inspection.Coverage.Checks {
		entries = append(entries, map[string]any{"check": check.ID, "reason": "external process", "owner": "example-ops", "review_by": "2027-01-01"})
	}
	r.Config.Value.(map[string]any)["storageVersion"] = "v1"
	r.Config.Value.(map[string]any)["audit"].(map[string]any)["not_applicable"] = entries
	got := assess(t, r, now, nil)
	if len(got.Evaluations) != 9 {
		t.Fatal(got.Evaluations)
	}
	for _, evaluation := range got.Evaluations {
		if evaluation.Result != "confirmed" {
			t.Fatalf("NA muted positive evidence: %+v", evaluation)
		}
	}
}

func TestIssueMissingProviderIDsDoNotProveRecovery(t *testing.T) {
	r := repo(t, "plugins:\n  aliyun:\n    instances:\n      home: {services: [cdn]}\n", map[string]string{"inventory/resources/old.yaml": "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: old, archived: {date: '2026-10-01', reason: retired}}\nspec: {provider: aliyun, type: cdn, resource_id: old-id, endpoint: old.example.net}"})
	inspectEvidence(t, r, "aliyun", "home", map[string]any{"cdn": []any{map[string]any{"id": "old-id", "name": "old.example.net", "status": "online"}}}, now, "succeeded")
	first := assess(t, r, now, nil)
	states, _ := advance(t, nil, first.Evaluations, now)
	if len(states) != 1 {
		t.Fatal(states)
	}
	at := now.Add(time.Hour)
	inspectEvidence(t, r, "aliyun", "home", map[string]any{"cdn": []any{map[string]any{"name": "renamed.example.net", "status": "online"}}}, at, "succeeded")
	got := assess(t, r, at, stateIDs(states))
	for _, e := range got.Evaluations {
		if e.Identity.Fingerprint == states[0].Identity.Fingerprint && (e.Result != "unknown" || e.Code != "identity_unknown") {
			t.Fatalf("missing ID was clear: %+v", e)
		}
	}
	next, changes := advance(t, states, got.Evaluations, at)
	for _, c := range changes {
		if c.Type == "recovered" {
			t.Fatal(c)
		}
	}
	at = at.Add(time.Hour)
	inspectEvidence(t, r, "aliyun", "home", map[string]any{"cdn": []any{}}, at, "succeeded")
	clear := assess(t, r, at, stateIDs(next))
	recovered, _ := advance(t, next, clear.Evaluations, at)
	for _, s := range recovered {
		if s.Lifecycle != "recovered" {
			t.Fatal(s)
		}
	}
}

func TestIssueRegionalStatusDoesNotBorrowAnotherVariant(t *testing.T) {
	for _, initial := range []string{"", "mainland"} {
		t.Run(initial, func(t *testing.T) {
			r := repo(t, `plugins:
  aliyun:
    instances:
      home: {services: [cdn]}
audit:
  watch:
    - {target: Resource/edge, plugin: aliyun, instance: home, service: cdn}
`, map[string]string{"inventory/resources/edge.yaml": "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: edge}\nspec: {provider: aliyun, type: cdn, resource_id: edge-id, endpoint: edge.example.com}"})
			inspectEvidence(t, r, "aliyun", "home", map[string]any{"cdn": []any{map[string]any{"name": "edge.example.com", "id": "edge-id", "status": "offline", "area": initial}}}, now, "succeeded")
			first := assess(t, r, now, nil)
			previous, _ := advance(t, nil, first.Evaluations, now)
			if len(previous) != 1 {
				t.Fatal(previous)
			}
			at := now.Add(time.Hour)
			inspectEvidence(t, r, "aliyun", "home", map[string]any{"cdn": []any{map[string]any{"name": "edge.example.com", "id": "edge-id", "status": "online", "area": "overseas"}}}, at, "succeeded")
			got := assess(t, r, at, stateIDs(previous))
			if len(got.Evaluations) != 1 || got.Evaluations[0].Result != "unknown" {
				t.Fatalf("different region cleared old risk: %+v", got.Evaluations)
			}
		})
	}
}

func TestIssueCloudflareLiveOwnerIsScopeExit(t *testing.T) {
	r := repo(t, "plugins:\n  cloudflare:\n    instances:\n      home: {}\n", map[string]string{"inventory/domains/example.com.yaml": "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com, archived: {date: '2026-10-01', reason: retired}}\nspec: {}"})
	inspectEvidence(t, r, "cloudflare", "home", map[string]any{"zones": []any{map[string]any{"name": "example.com", "id": "zone-id", "dns_records": []any{}}}}, now, "succeeded")
	first := assess(t, r, now, nil)
	previous, _ := advance(t, nil, first.Evaluations, now)
	if len(previous) != 1 {
		t.Fatal(previous)
	}
	write(t, r.Dir, "inventory/resources/zone.yaml", "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: zone}\nspec: {provider: cloudflare, type: dns-zone, resource_id: zone-id, endpoint: example.com}")
	r, problems, err := ledger.Load(context.Background(), r.Dir)
	if err != nil || len(problems) != 0 {
		t.Fatal(err, problems)
	}
	got := assess(t, r, now.Add(time.Hour), stateIDs(previous))
	states, _ := advance(t, previous, got.Evaluations, now.Add(time.Hour))
	if states[0].Lifecycle != "out_of_scope" || states[0].RecoveredAt != "" {
		t.Fatalf("new live owner manufactured recovery: %+v", states)
	}
}

func TestIssueEndpointWaiverIsIndependentOfDeclaredID(t *testing.T) {
	r := repo(t, `plugins:
  aliyun:
    instances:
      home: {services: [cdn]}
audit:
  ignore:
    - {rule: zombie, plugin: aliyun, instance: home, match: {kind: Resource, provider: aliyun, type: cdn, endpoint: old.example.net}, reason: reviewed}
`, map[string]string{"inventory/resources/old.yaml": "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: old, archived: {date: '2026-10-01', reason: retired}}\nspec: {provider: aliyun, type: cdn, resource_id: old-id, endpoint: old.example.net}"})
	inspectEvidence(t, r, "aliyun", "home", map[string]any{"cdn": []any{map[string]any{"name": "old.example.net", "status": "online"}}}, now, "succeeded")
	first := assess(t, r, now, nil)
	if len(first.Evaluations) != 1 || first.Evaluations[0].Waiver == nil || first.Evaluations[0].Result != "confirmed" {
		t.Fatal(first.Evaluations)
	}
	previous, _ := advance(t, nil, first.Evaluations, now)
	at := now.Add(time.Hour)
	inspectEvidence(t, r, "aliyun", "home", map[string]any{"cdn": []any{}}, at, "succeeded")
	clear := assess(t, r, at, stateIDs(previous))
	states, _ := advance(t, previous, clear.Evaluations, at)
	if states[0].Lifecycle != "open" || states[0].Waiver == nil {
		t.Fatalf("endpoint waiver manufactured recovery: %+v", states)
	}
	at = at.Add(time.Hour)
	inspectEvidence(t, r, "aliyun", "home", map[string]any{"cdn": []any{map[string]any{"name": "old.example.net", "id": "old-id", "status": "online"}}}, at, "succeeded")
	current := assess(t, r, at, stateIDs(states))
	if current.Evaluations[0].Waiver != nil {
		t.Fatal("endpoint-only waiver was expanded to a different exact identity")
	}
}

func TestIssueCloudflareOwnershipDoesNotCrossInstances(t *testing.T) {
	r := repo(t, "plugins:\n  cloudflare:\n    instances:\n      one: {}\n      two: {}\n", map[string]string{"inventory/domains/example.com.yaml": "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com, archived: {date: '2026-10-01', reason: retired}}\nspec: {}"})
	for _, instance := range []string{"one", "two"} {
		inspectEvidence(t, r, "cloudflare", instance, map[string]any{"zones": []any{map[string]any{"name": "example.com", "id": "zone-" + instance, "dns_records": []any{}}}}, now, "succeeded")
	}
	first := assess(t, r, now, nil)
	previous, _ := advance(t, nil, first.Evaluations, now)
	if len(previous) != 2 {
		t.Fatal(previous)
	}
	write(t, r.Dir, "inventory/resources/zone.yaml", "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: zone}\nspec: {provider: cloudflare, type: dns-zone, resource_id: zone-two, endpoint: example.com}")
	r, problems, err := ledger.Load(context.Background(), r.Dir)
	if err != nil || len(problems) != 0 {
		t.Fatal(err, problems)
	}
	current := assess(t, r, now.Add(time.Hour), stateIDs(previous))
	states, _ := advance(t, previous, current.Evaluations, now.Add(time.Hour))
	for _, state := range states {
		switch state.Identity.Key.Instance {
		case "one":
			if state.Lifecycle != "open" || state.Assessment != "confirmed" {
				t.Fatalf("other account claimed this issue: %+v", state)
			}
		case "two":
			if state.Lifecycle != "out_of_scope" || state.RecoveredAt != "" {
				t.Fatal(state)
			}
		}
	}
}
