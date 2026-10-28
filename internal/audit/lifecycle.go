package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
)

type observedEntry struct {
	id       observation.Identity
	snapshot Snapshot
	covered  []*ledger.Record
}

type lifecycleInput struct {
	index    map[observation.Identity][]*ledger.Record
	observed []observedEntry
}

// Prepare 为选中的生命周期规则建立共享自然身份索引；只跑时效规则时不解析观测业务字段。
func prepareLifecycle(ctx context.Context, in *Input, rules []Rule) error {
	needed := map[string]bool{}
	for _, rule := range rules {
		for _, req := range rule.Observations {
			needed[req.Plugin] = true
		}
	}
	if len(needed) == 0 {
		return nil
	}
	life := &lifecycleInput{index: map[observation.Identity][]*ledger.Record{}}
	for _, r := range in.Repo.Records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.Kind != "Domain" && r.Kind != "Resource" && r.Kind != "Server" {
			continue
		}
		spec, _ := field(r.Value, "spec").(map[string]any)
		id := observation.IdentityOf(r.Kind, r.Name, spec)
		life.index[id] = append(life.index[id], r)
		if r.Kind == "Resource" && id.ResourceID != "" {
			fallback := observation.EndpointIdentity(spec)
			if fallback.Endpoint != "" {
				life.index[fallback] = append(life.index[fallback], r)
			}
		}
	}
	for _, s := range in.Snapshots {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !needed[s.Plugin] {
			continue
		}
		records, _, err := observation.Map(s.Plugin, s.Instance, s.File, s.Spec)
		if err != nil {
			return fmt.Errorf("%s: %w", s.File, err)
		}
		seen := map[observation.Identity]bool{}
		for _, r := range records {
			if err := ctx.Err(); err != nil {
				return err
			}
			id := observation.IdentityOf(r.Kind, r.Name, r.Spec)
			if seen[id] {
				continue
			}
			seen[id] = true
			covered := append([]*ledger.Record{}, life.index[id]...)
			if s.Plugin == "cloudflare" && r.Spec["type"] == "dns-zone" {
				domain := observation.IdentityOf("Domain", r.Spec["endpoint"].(string), nil)
				covered = append(covered, life.index[domain]...)
			}
			life.observed = append(life.observed, observedEntry{id, s, covered})
		}
	}
	in.lifecycle = life
	return nil
}

func lifecycleFindings(in *Input, rule string) []Finding {
	findings := []Finding{}
	if in.lifecycle == nil {
		return findings
	}
	for _, entry := range in.lifecycle.observed {
		live := false
		for _, r := range entry.covered {
			live = live || !r.Archived()
		}
		if live {
			continue
		}
		s := entry.snapshot
		var at any
		if s.ObservedAt != nil {
			at = s.ObservedAt.UTC().Format(time.RFC3339)
		}
		evidence := map[string]any{"identity": entry.id, "plugin": s.Plugin, "instance": s.Instance, "snapshot_file": s.File, "observed_at": at}
		finding := Finding{Rule: rule, File: s.File, Evidence: evidence, identity: &entry.id, plugin: s.Plugin, instance: s.Instance}
		switch rule {
		case "orphan":
			if len(entry.covered) != 0 {
				continue
			}
			finding.Target = s.Plugin + "/" + s.Instance + "/" + entry.id.String()
			finding.Message = "快照中存在，台账中没有对应记录（含归档）；可用 import state 生成待审草稿"
			findings = append(findings, finding)
		case "zombie":
			for _, r := range entry.covered {
				finding.Target, finding.File = r.Kind+"/"+r.Name, r.File
				finding.Message = "台账已归档，但快照中仍观测到该资产"
				findings = append(findings, finding)
			}
		}
	}
	return findings
}

var orphan = Rule{
	ID: "orphan", Description: "快照中存在但台账未登记的资产",
	Observations: observation.Requirements(),
	Ready:        func(*Input) error { return nil },
	Eval:         func(in *Input) []Finding { return lifecycleFindings(in, "orphan") },
}

var zombie = Rule{
	ID: "zombie", Description: "已归档但快照中仍存在的资产",
	Observations: observation.Requirements(),
	Ready:        func(*Input) error { return nil },
	Eval:         func(in *Input) []Finding { return lifecycleFindings(in, "zombie") },
}
