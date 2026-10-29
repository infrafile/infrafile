package importer

import (
	"context"
	"errors"
	"sort"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/validate"
)

type ReviewEntry struct {
	ID     string   `json:"id"`
	Source string   `json:"source"`
	Key    string   `json:"key,omitempty"`
	Status string   `json:"status"`
	Match  string   `json:"match,omitempty"`
	Gaps   []string `json:"gaps,omitempty"`
}

type ReviewResult struct {
	Version       string        `json:"version"`
	OK            bool          `json:"ok"`
	SourceChanged bool          `json:"source_changed"`
	RepoChanged   bool          `json:"repo_changed"`
	Records       []ReviewEntry `json:"records"`
	Skipped       []Notice      `json:"skipped"`
	Problems      []string      `json:"problems"`
}

func reviewAgainst(ctx context.Context, repo *ledger.Repo, d, fresh *Draft, notices []Notice) (*ReviewResult, error) {
	r := &ReviewResult{Version: "v1", OK: true, SourceChanged: !equalJSON(d.Files, fresh.Files), RepoChanged: d.RepoHash != fresh.RepoHash, Records: []ReviewEntry{}, Skipped: append([]Notice{}, notices...), Problems: []string{}}
	if r.SourceChanged {
		r.OK = false
		r.Problems = append(r.Problems, "source-changed")
		return r, nil
	}
	if r.RepoChanged {
		r.OK = false
		r.Problems = append(r.Problems, "repository-changed")
	}
	originals := map[string]Candidate{}
	for _, c := range fresh.Candidates {
		originals[c.ID] = c
	}
	if len(d.Candidates) != len(originals) {
		return nil, errors.New("候选集合与来源不符；请用 selected: false 排除候选")
	}
	schemas, err := ledger.LoadSchemas()
	if err != nil {
		return nil, err
	}
	byKey := map[string]*ledger.Record{}
	byIdentity := map[observation.Identity][]*ledger.Record{}
	for _, record := range repo.Records {
		byKey[record.Kind+"/"+record.Name] = record
		spec := record.Value.(map[string]any)["spec"].(map[string]any)
		id := observation.IdentityOf(record.Kind, record.Name, spec)
		if record.Kind == "Domain" || record.Kind == "Server" || record.Kind == "Resource" {
			byIdentity[id] = append(byIdentity[id], record)
		}
	}
	virtual := *repo
	virtual.Records = append([]*ledger.Record{}, repo.Records...)
	batchKeys, batchIdentity := map[string]int{}, map[observation.Identity][]int{}
	seen := map[string]bool{}
	codes := map[string]bool{}
	for _, c := range d.Candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		original, ok := originals[c.ID]
		if !ok || seen[c.ID] || c.Source != original.Source || !equalJSON(c.Identity, original.Identity) {
			return nil, errors.New("候选来源或身份不符；请重新生成草稿")
		}
		seen[c.ID] = true
		entry := ReviewEntry{ID: c.ID, Source: c.Source, Status: "invalid"}
		if len(validate.PlaintextProblems(&ledger.Document{File: "candidate", Value: c.Record})) > 0 {
			codes[ledger.CodePlaintext] = true
			r.OK = false
			r.Records = append(r.Records, entry)
			continue
		}
		kind, _ := c.Record["kind"].(string)
		if kind != original.Record["kind"] || schemas.Validate(kind, c.Record) != nil {
			r.OK = false
			r.Records = append(r.Records, entry)
			continue
		}
		md := c.Record["metadata"].(map[string]any)
		name := md["name"].(string)
		if len(name+".yaml") > 255 {
			r.OK = false
			r.Records = append(r.Records, entry)
			continue
		}
		spec := c.Record["spec"].(map[string]any)
		id := observation.IdentityOf(kind, name, spec)
		entry.Key = kind + "/" + name
		entry.Gaps = gaps(kind, spec)
		if !equalJSON(id, c.Identity) {
			entry.Gaps = append(entry.Gaps, "identity-edited")
		}
		entry.Status = "draft"
		if !c.Selected {
			entry.Status = "excluded"
		} else if old := byKey[entry.Key]; old != nil {
			entry.Match = entry.Key
			switch {
			case old.Archived():
				entry.Status = "archived"
			case subset(c.Record, old.Value):
				entry.Status = "exists"
			default:
				entry.Status = "conflict"
			}
		} else {
			for _, old := range byIdentity[id] {
				oldSpec := old.Value.(map[string]any)["spec"].(map[string]any)
				// Different declared accounts are distinct; an absent account cannot prove scope.
				if kind == "Resource" && spec["account"] != nil && oldSpec["account"] != nil && spec["account"] != oldSpec["account"] {
					continue
				}
				entry.Match = old.Kind + "/" + old.Name
				entry.Status = "conflict"
				if old.Archived() {
					entry.Gaps = append(entry.Gaps, "identity-matches-archive")
				}
				break
			}
		}
		if entry.Status == "draft" {
			if previous, ok := batchKeys[entry.Key]; ok {
				if equalJSON(c.Record, d.Candidates[previous].Record) {
					entry.Status = "duplicate"
				} else {
					entry.Status = "conflict"
					r.Records[previous].Status = "conflict"
				}
			} else {
				for _, previous := range batchIdentity[id] {
					old := d.Candidates[previous].Record["spec"].(map[string]any)
					if kind == "Resource" && spec["account"] != nil && old["account"] != nil && spec["account"] != old["account"] {
						continue
					}
					entry.Status = "conflict"
					r.Records[previous].Status = "conflict"
				}
				batchKeys[entry.Key] = len(r.Records)
				batchIdentity[id] = append(batchIdentity[id], len(r.Records))
			}
			if entry.Status == "draft" {
				k, _ := ledger.KindByName(kind)
				virtual.Records = append(virtual.Records, &ledger.Record{Document: &ledger.Document{File: "inventory/" + k.Dir + "/" + name + ".yaml", Value: c.Record}, Kind: kind, Name: name})
			}
		}
		if entry.Status == "conflict" {
			r.OK = false
		}
		r.Records = append(r.Records, entry)
	}
	checked, err := validate.Check(ctx, &virtual)
	if err != nil {
		return nil, err
	}
	for _, p := range checked.Problems {
		codes[p.Code] = true
	}
	for code := range codes {
		r.Problems = append(r.Problems, code)
	}
	if len(checked.Problems) > 0 {
		r.OK = false
	}
	sort.Strings(r.Problems)
	sort.Slice(r.Records, func(i, j int) bool { return r.Records[i].ID < r.Records[j].ID })
	return r, nil
}

// Candidate fields may already be present alongside additional hand-maintained fields.
func subset(candidate, existing any) bool {
	a, ok := candidate.(map[string]any)
	if !ok {
		return equalJSON(candidate, existing)
	}
	b, ok := existing.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || !subset(value, other) {
			return false
		}
	}
	return true
}

func gaps(kind string, spec map[string]any) []string {
	fields := []string{}
	switch kind {
	case "Domain":
		for _, field := range []string{"registrar", "expires_at"} {
			if spec[field] == nil {
				fields = append(fields, "missing-"+field)
			}
		}
	case "Resource":
		if spec["resource_id"] == nil {
			fields = append(fields, "missing-resource_id")
		}
	case "Server":
		ssh, _ := spec["ssh"].(map[string]any)
		for _, field := range []string{"user", "port"} {
			if ssh[field] == nil {
				fields = append(fields, "missing-ssh."+field)
			}
		}
	}
	return fields
}
