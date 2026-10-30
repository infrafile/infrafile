package audit

import (
	"context"
	"fmt"
	"sort"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/snapshot"
)

// One request owns these read-only inputs; each scope gets a separate inspector/report.
type inspectionInput struct {
	in             *Input
	findings       []Finding
	notices        []Notice
	config         map[string]map[string]map[string]any
	snaps          map[string]Snapshot
	attempts       map[string]snapshot.Attempt
	watches        map[string]observation.Watch
	baselines      map[string]snapshot.PresenceEntry
	records        map[string]*ledger.Record
	relations      []InspectRelation
	relationsByKey map[string][]int
	findingsByKey  map[string][]int
}

func prepareInspection(ctx context.Context, repo *ledger.Repo, opts InspectOptions) (*inspectionInput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.MaxAgeDays != nil && *opts.MaxAgeDays < 0 {
		return nil, fmt.Errorf("max-age must be nonnegative")
	}
	snaps, notices, err := ReadSnapshots(ctx, repo)
	if err != nil {
		return nil, err
	}
	in := &Input{Repo: repo, Snapshots: snaps, Now: opts.Now, MaxAge: opts.MaxAgeDays}
	rules, err := Select(nil, in)
	if err != nil {
		return nil, err
	}
	if err := Prepare(ctx, in, rules); err != nil {
		return nil, err
	}
	findings, more := Run(in, rules)
	p := &inspectionInput{in: in, findings: findings, notices: append(notices, more...),
		config: map[string]map[string]map[string]any{}, snaps: map[string]Snapshot{},
		attempts: map[string]snapshot.Attempt{}, watches: map[string]observation.Watch{}, baselines: map[string]snapshot.PresenceEntry{},
		records: map[string]*ledger.Record{}, relationsByKey: map[string][]int{}, findingsByKey: map[string][]int{}}
	schemas, err := ledger.LoadSchemas()
	if err != nil {
		return nil, err
	}
	for _, record := range repo.Records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		from := record.Kind + "/" + record.Name
		p.records[from] = record
		for _, ref := range record.References(schemas) {
			to := ref.Kind + "/" + ref.Name
			index := len(p.relations)
			p.relations = append(p.relations, InspectRelation{from, to, ref.Path})
			p.relationsByKey[from] = append(p.relationsByKey[from], index)
			if to != from {
				p.relationsByKey[to] = append(p.relationsByKey[to], index)
			}
		}
	}
	for index, finding := range findings {
		p.findingsByKey[finding.Target] = append(p.findingsByKey[finding.Target], index)
	}
	plugins, _ := field(repo.Config.Value, "plugins").(map[string]any)
	for plugin, value := range plugins {
		p.config[plugin] = map[string]map[string]any{}
		instances, _ := field(value, "instances").(map[string]any)
		for name, config := range instances {
			p.config[plugin][name], _ = config.(map[string]any)
		}
	}
	for _, s := range snaps {
		p.snaps[s.Plugin+"/"+s.Instance] = s
	}
	status, err := snapshot.ReadStatus(ctx, repo.Dir)
	if err != nil {
		return nil, err
	}
	if status != nil {
		for plugin, instances := range status.Spec {
			for name, attempt := range instances {
				p.attempts[plugin+"/"+name] = attempt
			}
		}
	}
	watches, problems := observation.Watches(repo)
	if len(problems) != 0 {
		return nil, fmt.Errorf("invalid watch")
	}
	for _, w := range watches {
		p.watches[w.Target] = w
	}
	presence, err := snapshot.ReadPresence(ctx, repo.Dir)
	if err != nil {
		return nil, err
	}
	if presence != nil {
		p.baselines = presence.Spec
	}
	return p, ctx.Err()
}

func (p *inspectionInput) selectRecords(keys []string) ([]*ledger.Record, error) {
	if keys == nil {
		return p.in.Repo.Records, nil
	}
	selected := make([]*ledger.Record, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		record := p.records[key]
		if record == nil || seen[key] {
			return nil, fmt.Errorf("unknown or repeated selection")
		}
		seen[key] = true
		selected = append(selected, record)
	}
	return selected, nil
}

// Preserve source ordering and duplicate source entries, while sharing cross-asset edges once.
func selectedIndices(selected map[string]bool, byKey map[string][]int) []int {
	seen := map[int]bool{}
	for key := range selected {
		for _, index := range byKey[key] {
			seen[index] = true
		}
	}
	indices := make([]int, 0, len(seen))
	for index := range seen {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	return indices
}
