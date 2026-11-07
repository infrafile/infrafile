package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func productionFixture(t *testing.T) (*webFixture, opsOverview, string) {
	t.Helper()
	f, o, path := opsFixture(t)
	stamp := f.clock.Add(-time.Second).Format(time.RFC3339)
	p := &opsProduction{Boundary: "one reviewed instance"}
	for _, axis := range []string{"readiness", "traffic", "replicas", "recovery", "release"} {
		p.Gates = append(p.Gates, opsProductionGate{ID: axis, Requirements: []string{"not yet proven"}, Observations: []opsProductionObservation{}})
	}
	p.Gates[0].Observations = []opsProductionObservation{{ID: "constant-query", Hosts: []string{o.Deployments[0].Host}, Summary: "constant query only", Reference: "reviewed sample", ReferenceSHA256: strings.Repeat("a", 64), Evidence: opsEvidence{Status: "PASS", Reason: "partial_observation_not_admission", ObservedAt: &stamp, Source: "reviewed-observation", MaxAgeSeconds: 60}}}
	o.Applications[0].Production = p
	writeOps(t, path, o)
	return f, o, path
}

func TestOpsProductionFactsAgeIndependentlyWithoutGrantingActions(t *testing.T) {
	f, o, path := productionFixture(t)
	f.login(t)
	r := readOps(t, f)
	fact := r.Applications[0].Production.Gates[0].Observations[0]
	if fact.Evidence.Status != "PASS" || len(r.ActionCapabilities) != 0 {
		t.Fatal("partial fact changed authority")
	}
	f.clock = f.clock.Add(2 * time.Minute)
	r = readOps(t, f)
	old := r.Applications[0].Production.Gates[0].Observations[0]
	if old.Evidence.Status != "STALE" || old.Evidence.ObservedStatus != "PASS" || *old.Evidence.ObservedAt != *fact.Evidence.ObservedAt {
		t.Fatal("historical sample retimed or retained green")
	}
	future := f.clock.Add(time.Hour).Format(time.RFC3339)
	o.Applications[0].Production.Gates[0].Observations[0].Evidence.ObservedAt = &future
	writeOps(t, path, o)
	if readOps(t, f).Applications[0].Production.Gates[0].Observations[0].Evidence.Status != "UNKNOWN" {
		t.Fatal("future sample green")
	}
}

func TestOpsProductionRejectsScopeStateAndOriginMismatch(t *testing.T) {
	_, original, _ := productionFixture(t)
	for _, test := range []struct {
		name   string
		mutate func(*opsProduction)
	}{
		{"missing axis", func(p *opsProduction) { p.Gates = p.Gates[:4] }},
		{"duplicate axis", func(p *opsProduction) { p.Gates[1].ID = p.Gates[0].ID }},
		{"no requirement", func(p *opsProduction) { p.Gates[0].Requirements = nil }},
		{"null observations", func(p *opsProduction) { p.Gates[0].Observations = nil }},
		{"foreign host", func(p *opsProduction) { p.Gates[0].Observations[0].Hosts = []string{"Server/foreign"} }},
		{"duplicate sample", func(p *opsProduction) { p.Gates[1].Observations = p.Gates[0].Observations }},
		{"pause bypass", func(p *opsProduction) { p.Gates[0].Observations[0].Evidence.Status = "PAUSED" }},
		{"no timestamp", func(p *opsProduction) { p.Gates[0].Observations[0].Evidence.ObservedAt = nil }},
		{"bad provenance", func(p *opsProduction) { p.Gates[0].Observations[0].ReferenceSHA256 = "bad" }},
		{"policy claims fact", func(p *opsProduction) { p.Gates[0].Observations[0].Evidence.Source = "policy" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			var o opsOverview
			if err := json.Unmarshal(raw, &o); err != nil {
				t.Fatal(err)
			}
			test.mutate(o.Applications[0].Production)
			if validateOpsProduction(&o, time.Now()) == nil {
				t.Fatal("invalid production evidence accepted")
			}
		})
	}
}
