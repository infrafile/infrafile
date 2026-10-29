package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
)

type AssetEvidence struct {
	Check      string  `json:"check"`
	Plugin     string  `json:"plugin"`
	Instance   string  `json:"instance"`
	Service    string  `json:"service"`
	File       string  `json:"file"`
	ObservedAt *string `json:"observed_at"`
	Match      string  `json:"match"`
	Values     []any   `json:"values"`
}

type AssetContext struct {
	Version      string          `json:"version"`
	Inspection   *Inspection     `json:"inspection"`
	Observations []AssetEvidence `json:"observations"`
}

// Context returns only facts matched to a single selected asset's explicit check scopes.
// The inspection still decides evidence freshness; saved values never imply current success.
func Context(ctx context.Context, repo *ledger.Repo, opts InspectOptions) (*AssetContext, error) {
	if len(opts.Keys) != 1 {
		return nil, fmt.Errorf("context requires one registered asset")
	}
	i, err := inspectWithHistory(ctx, repo, opts)
	if err != nil {
		return nil, err
	}
	result := &AssetContext{Version: "v1", Inspection: i.report, Observations: []AssetEvidence{}}
	var asset *ledger.Record
	for _, r := range repo.Records {
		if r.Kind+"/"+r.Name == opts.Keys[0] {
			asset = r
		}
	}
	for _, check := range i.report.Coverage.Checks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s, ok := i.snaps[check.Plugin+"/"+check.Instance]
		if !ok {
			continue
		}
		e := AssetEvidence{Check: check.ID, Plugin: check.Plugin, Instance: check.Instance, Service: check.Service, File: s.File, ObservedAt: stamp(s.ObservedAt), Match: "unsupported", Values: []any{}}
		switch check.Service {
		case "domains", "cdn", "zones":
			w, ok := i.watches[check.Target]
			if !ok || w.Plugin != check.Plugin || w.Instance != check.Instance || w.Service != check.Service {
				break
			}
			e.Match = "not_observed"
			items, present := s.Spec[check.Service].([]any)
			if !present {
				e.Match = "incomplete"
				break
			}
			for _, item := range items {
				ids, _, err := observation.WatchRecords(w.Plugin, w.Instance, w.Service, map[string]any{w.Service: []any{item}})
				if err != nil {
					return nil, err
				}
				if w.Seen(ids) {
					e.Values = append(e.Values, item)
				}
			}
			if len(e.Values) == 1 {
				e.Match = "matched"
			}
			if len(e.Values) > 1 {
				e.Match = "ambiguous"
				e.Values = []any{}
			}
		case "host":
			if check.Plugin != "ssh" {
				break
			}
			e.Match = "identity_mismatch"
			mapped, _, err := observation.Map("ssh", check.Instance, s.File, s.Spec)
			if err != nil {
				return nil, err
			}
			if asset != nil && len(mapped) == 1 && observation.IdentityOf("Server", asset.Name, field(asset.Value, "spec").(map[string]any)) == observation.IdentityOf("Server", mapped[0].Name, mapped[0].Spec) {
				e.Match = "matched"
				e.Values = append(e.Values, s.Spec)
			}
		case "addresses", "zones.dns_records":
			host := check.Aspect[strings.LastIndex(check.Aspect, "/")+1:]
			e.Match = "not_observed"
			spec := s.Spec
			if check.Service == "zones.dns_records" {
				w, ok := i.watches[check.Target]
				if !ok || w.Plugin != check.Plugin || w.Instance != check.Instance {
					e.Match = "unsupported"
					break
				}
				zones, _ := s.Spec["zones"].([]any)
				matched := []any{}
				for _, zone := range zones {
					ids, _, err := observation.WatchRecords(w.Plugin, w.Instance, "zones", map[string]any{"zones": []any{zone}})
					if err != nil {
						return nil, err
					}
					if w.Seen(ids) {
						matched = append(matched, zone)
					}
				}
				if len(matched) != 1 {
					if len(matched) > 1 {
						e.Match = "ambiguous"
					}
					break
				}
				spec = map[string]any{"zones": matched}
			}
			if check.Plugin == "dns" {
				actual, _ := spec["host"].(string)
				if normalizedHost(actual) != host {
					e.Match = "identity_mismatch"
					break
				}
				e.Match = "matched"
			}
			rows, err := observation.AddressRecords(check.Plugin, spec)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if normalizedHost(row.Host) == host {
					e.Values = append(e.Values, map[string]any{"host": row.Host, "type": row.Type, "address": row.Address.String(), "proxied": row.Proxied})
				}
			}
			if len(e.Values) > 0 {
				e.Match = "matched"
			}
		}
		result.Observations = append(result.Observations, e)
	}
	return result, nil
}
