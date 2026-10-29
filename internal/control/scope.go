package control

import (
	"context"
	"errors"
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/plugin"
)

type ScopeAsset struct {
	Key        string   `json:"key"`
	Via        []string `json:"via"`
	RecordHash string   `json:"record_hash"`
}

type Scope struct {
	Level          string       `json:"level"`
	ZoneName       string       `json:"zone_name,omitempty"`
	RuleExpression string       `json:"rule_expression,omitempty"`
	Related        []ScopeAsset `json:"related"`
	Unknown        []string     `json:"unknown"`
}

func baseScope(a Action) *Scope {
	s := &Scope{Level: "plugin_target", Related: []ScopeAsset{}, Unknown: []string{"cloud_inventory_not_complete", "traffic_and_business_not_verified", "associations_are_not_proven_impact", "transitive_dependencies_not_evaluated", "permissions_and_entitlement_not_verified"}}
	if a.Binding.Plugin != "cloudflare" {
		s.Unknown = append(s.Unknown, "plugin_scope_unknown")
		return s
	}
	s.Level = "zone"
	name, _ := a.Before["name"].(string)
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if plugin.Hostname(name) {
		s.ZoneName = name
	} else {
		s.Unknown = append(s.Unknown, "zone_name_not_observed")
	}
	s.Unknown = append(s.Unknown, "dns_delegation_not_checked")
	if a.Operation == "cdn-origin" {
		s.Level = "origin_rule"
		rules, _ := a.Before["rules"].([]any)
		for _, item := range rules {
			rule, _ := item.(map[string]any)
			if rule["id"] == a.Binding.RuleID {
				s.RuleExpression, _ = rule["expression"].(string)
			}
		}
		if s.RuleExpression == "" {
			s.Unknown = append(s.Unknown, "rule_expression_not_observed")
		}
	}
	return s
}

func zoneHostname(value, zone string) bool {
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil {
			return false
		}
		value = u.Hostname()
	} else if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.ToLower(strings.TrimSuffix(value, "."))
	return zone != "" && plugin.Hostname(value) && (value == zone || strings.HasSuffix(value, "."+zone))
}

// planScope uses live registrations and schema references only; it does not resolve DNS or probe assets.
func planScope(ctx context.Context, repo *ledger.Repo, a Action) (*Scope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := baseScope(a)
	schemas, err := ledger.LoadSchemas()
	if err != nil {
		return nil, err
	}
	byKey := map[string]*ledger.Record{}
	seeds := map[string]bool{a.Target: true}
	via := map[string]map[string]bool{}
	add := func(key, reason string) {
		if key == a.Target {
			return
		}
		if via[key] == nil {
			via[key] = map[string]bool{}
		}
		via[key][reason] = true
	}
	for _, r := range repo.Records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if r.Archived() {
			continue
		}
		key := r.Kind + "/" + r.Name
		byKey[key] = r
		spec, _ := r.Value.(map[string]any)["spec"].(map[string]any)
		var host string
		switch r.Kind {
		case "Domain":
			host = r.Name
		case "Resource":
			host, _ = spec["endpoint"].(string)
		case "Server":
			host, _ = spec["address"].(string)
		}
		if zoneHostname(host, s.ZoneName) {
			seeds[key] = true
			add(key, "zone_hostname")
		}
	}
	for key, r := range byKey {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, ref := range r.References(schemas) {
			to := ref.Kind + "/" + ref.Name
			if seeds[key] && byKey[to] != nil {
				add(to, "reference")
			}
			if seeds[to] {
				add(key, "reference")
			}
		}
	}
	if len(via) > 256 {
		return nil, errors.New("计划关联资产超过256条，请缩小登记范围；不会截断作用范围")
	}
	for key, reasons := range via {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		paths := []string{}
		for reason := range reasons {
			paths = append(paths, reason)
		}
		sort.Strings(paths)
		s.Related = append(s.Related, ScopeAsset{Key: key, Via: paths, RecordHash: plugin.Hash(byKey[key].Value)})
	}
	sort.Slice(s.Related, func(i, j int) bool { return s.Related[i].Key < s.Related[j].Key })
	return s, nil
}

func (s *Scope) validate(a Action) error {
	if len(s.Related) > 256 || s.Related == nil {
		return errors.New("计划关联范围无效或超过256条")
	}
	want := baseScope(a)
	want.Related = s.Related
	if plugin.Hash(s) != plugin.Hash(want) {
		return errors.New("计划作用范围或未知项与动作不一致")
	}
	previous := ""
	for _, r := range s.Related {
		kind, name, ok := strings.Cut(r.Key, "/")
		_, known := ledger.KindByName(kind)
		if !ok || !known || !resourceKey.MatchString("Resource/"+name) || r.Key == a.Target || r.Key <= previous || !digest.MatchString(r.RecordHash) || len(r.Via) == 0 || len(r.Via) > 2 {
			return errors.New("计划关联资产身份或顺序无效")
		}
		last := ""
		for _, via := range r.Via {
			if (via != "reference" && via != "zone_hostname") || via <= last {
				return errors.New("计划关联来源无效")
			}
			last = via
		}
		previous = r.Key
	}
	return nil
}
