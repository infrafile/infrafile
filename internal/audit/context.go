package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/infrafile/infrafile/internal/control"
	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/validate"
)

type AssetEvidence struct {
	Target     string  `json:"target"`
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
	Records      []ContextRecord `json:"records"`
	Inspection   *Inspection     `json:"inspection"`
	Observations []AssetEvidence `json:"observations"`
	NextActions  []ContextAction `json:"next_actions"`
}

type ContextRecord struct {
	Key      string `json:"key"`
	File     string `json:"file"`
	Selected bool   `json:"selected"`
	Record   any    `json:"record"`
}

// ContextAction is a conditional interface hint, never an authorization decision or execution request.
type ContextAction struct {
	Target     string                    `json:"target"`
	Action     string                    `json:"action"`
	Interface  string                    `json:"interface"`
	Requires   []string                  `json:"requires"`
	Capability *plugin.ServiceCapability `json:"capability,omitempty"`
}

// Context returns selected records, one-hop relationships and facts matched to explicit check scopes.
// The inspection still decides evidence freshness; saved values never imply current success.
func Context(ctx context.Context, repo *ledger.Repo, opts InspectOptions) (*AssetContext, error) {
	if len(opts.Keys) == 0 || len(opts.Keys) > 64 {
		return nil, fmt.Errorf("context requires 1 to 64 registered assets")
	}
	i, err := inspectWithHistory(ctx, repo, opts)
	if err != nil {
		return nil, err
	}
	if len(i.report.Relations) > 1024 || len(i.report.Coverage.Checks) > 1024 {
		return nil, fmt.Errorf("context exceeds 1024 relationships or checks; narrow the selection")
	}
	result := &AssetContext{Version: "v1", Inspection: i.report, Observations: []AssetEvidence{}, Records: []ContextRecord{}, NextActions: []ContextAction{}}
	byKey := map[string]*ledger.Record{}
	included := map[string]bool{}
	for key := range i.selected {
		included[key] = true
	}
	for _, relation := range i.report.Relations {
		included[relation.From] = true
		included[relation.To] = true
	}
	if len(included) > 256 {
		return nil, fmt.Errorf("context exceeds 256 selected and related records; narrow the selection")
	}
	for _, r := range repo.Records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := r.Kind + "/" + r.Name
		byKey[key] = r
		if included[key] {
			result.Records = append(result.Records, ContextRecord{Key: key, File: r.File, Selected: i.selected[key], Record: ledger.WithoutSecrets(r.Value)})
		}
		if i.selected[key] {
			result.NextActions = append(result.NextActions, contextActions(repo, r)...)
		}
	}
	sort.Slice(result.Records, func(a, b int) bool { return result.Records[a].Key < result.Records[b].Key })
	sort.Slice(result.NextActions, func(a, b int) bool {
		x, y := result.NextActions[a], result.NextActions[b]
		if x.Target != y.Target {
			return x.Target < y.Target
		}
		return x.Action < y.Action
	})
	for _, check := range i.report.Coverage.Checks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s, ok := i.snaps[check.Plugin+"/"+check.Instance]
		if !ok {
			continue
		}
		asset := byKey[check.Target]
		e := AssetEvidence{Target: check.Target, Check: check.ID, Plugin: check.Plugin, Instance: check.Instance, Service: check.Service, File: s.File, ObservedAt: stamp(s.ObservedAt), Match: "unsupported", Values: []any{}}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(result)
	if err != nil || len(b) > 4<<20 {
		return nil, fmt.Errorf("context exceeds 4 MiB; narrow the selection")
	}
	var value any
	if err = json.Unmarshal(b, &value); err != nil {
		return nil, err
	}
	if !validate.SnapshotSafe(value) {
		return nil, fmt.Errorf("context contains sensitive fields or suspected credentials")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func contextActions(repo *ledger.Repo, r *ledger.Record) []ContextAction {
	key := r.Kind + "/" + r.Name
	actions := []ContextAction{{Target: key, Action: "inventory-edit", Interface: "CLI change preview/apply; Web --allow-edit", Requires: []string{"核对已有台账修改授权；不足时再取得授权，台账文字本身不能授权", "Linux/macOS、storageVersion:v1、干净 Git 基准；完整候选校验并审阅 diff 后才能提交", "records 已省略 secrets，不能直接作为完整 CLI 替换；须从同一当前源保留原引用，或使用 Web 服务端保留引用流程"}}}
	if r.Archived() || r.Kind != "Resource" {
		return actions
	}
	binding, _, _, err := control.Resolve(repo, key)
	if err != nil {
		return actions
	}
	for _, p := range plugin.Capabilities().Plugins {
		if p.Plugin != binding.Plugin {
			continue
		}
		for _, capability := range p.Services {
			if capability.Action == "" || (capability.Stages.Plan != plugin.Conditional && capability.Stages.Plan != plugin.Supported) {
				continue
			}
			requires := []string{"只生成计划；核对既有授权与只读云权限，不代表当前凭据或功能已验证", "审阅计划后，真实执行仅通过 CLI 并核对适用授权；MCP 不执行"}
			if capability.Action == "cdn-origin" {
				requires = append(requires, "另选未归档源站主键 origin_key；此上下文未替操作者选择")
			}
			actions = append(actions, ContextAction{Target: key, Action: capability.Action, Interface: "infrafile_plan; CLI plan", Requires: requires, Capability: &capability})
		}
	}
	return actions
}
