package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
)

// 仅明确关闭/删除的终态报偏差；官方状态表及来源见 docs/json/audit.md。
var cdnStates = map[string]map[string]bool{
	"aliyun":  {"online": false, "offline": true, "configuring": false, "configure_failed": false, "checking": false, "check_failed": false, "stopping": false, "deleting": false},
	"tencent": {"online": false, "offline": true, "rejected": false, "processing": false, "closing": false},
	"ctyun":   {"1": false, "2": false, "3": false, "4": false, "5": false, "6": true, "7": false, "8": true, "9": false, "10": false, "11": false, "12": false},
}

func prepareCDN(ctx context.Context, in *Input) error {
	index := map[observation.Identity][]*ledger.Record{}
	for _, r := range in.Repo.Records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.Kind != "Resource" || r.Archived() {
			continue
		}
		spec, _ := field(r.Value, "spec").(map[string]any)
		if spec["type"] != "cdn" {
			continue
		}
		id := observation.IdentityOf(r.Kind, r.Name, spec)
		index[id] = append(index[id], r)
		if id.ResourceID != "" {
			fallback := observation.EndpointIdentity(spec)
			if fallback.Endpoint != "" {
				index[fallback] = append(index[fallback], r)
			}
		}
	}
	in.cdnFindings, in.cdnNotices = []Finding{}, []Notice{}
	for _, s := range in.Snapshots {
		if err := ctx.Err(); err != nil {
			return err
		}
		states, supported := cdnStates[s.Plugin]
		value, exists := s.Spec["cdn"]
		if !supported || !exists {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: CDN 观测必须是数组", s.File)
		}
		type key struct {
			id           observation.Identity
			status, area string
		}
		seen := map[key]bool{}
		for _, item := range items {
			if err := ctx.Err(); err != nil {
				return err
			}
			mapped, _, err := observation.Map(s.Plugin, s.Instance, s.File, map[string]any{"cdn": []any{item}})
			if err != nil {
				return fmt.Errorf("%s: %w", s.File, err)
			}
			raw := item.(map[string]any)
			status, ok := raw["status"].(string)
			if _, exists := raw["status"]; exists && !ok {
				return fmt.Errorf("%s: CDN status 必须是字符串", s.File)
			}
			area, _ := raw["area"].(string)
			id := observation.IdentityOf("Resource", mapped[0].Name, mapped[0].Spec)
			k := key{id, status, area}
			if seen[k] {
				continue
			}
			seen[k] = true
			stopped, known := states[status]
			if !known {
				in.cdnNotices = append(in.cdnNotices, Notice{Code: "cdn_status_unknown", File: s.File, Target: s.Plugin + "/" + s.Instance + "/" + id.String(), Rule: "cdn_drift", Message: fmt.Sprintf("CDN 状态 %q 未纳入官方状态映射，未判断停用漂移", status)})
				continue
			}
			if !stopped {
				continue
			}
			var at any
			if s.ObservedAt != nil {
				at = s.ObservedAt.UTC().Format(time.RFC3339)
			}
			for _, r := range index[id] {
				evidence := map[string]any{"aspect": "status", "plugin": s.Plugin, "instance": s.Instance, "snapshot_file": s.File, "observed_at": at, "identity": id, "status": status}
				if area != "" {
					evidence["area"] = area
				}
				in.cdnFindings = append(in.cdnFindings, Finding{Rule: "cdn_drift", Target: "Resource/" + r.Name, File: r.File, Message: fmt.Sprintf("台账 CDN 在用，但快照状态为 %s（已停用或已删除）", status), Evidence: evidence})
			}
		}
	}
	return prepareCDNOrigins(ctx, in)
}

var cdnDrift = Rule{
	ID: "cdn_drift", Description: "在用 CDN 的停用状态或显式期望回源与观测不一致",
	Observations: []observation.Requirement{{Plugin: "aliyun", Services: []string{"cdn"}}, {Plugin: "tencent", Services: []string{"cdn"}}, {Plugin: "baidu", Services: []string{"cdn"}}, {Plugin: "ctyun", Services: []string{"cdn"}}, {Plugin: "cloudflare", Services: []string{"zones"}}},
	Ready:        func(*Input) error { return nil }, Eval: func(in *Input) []Finding { return in.cdnFindings },
}
