package audit

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/plugin"
)

const IssueVersion = "v1"

// IssueKey excludes messages, times, observed values and file locations.
type IssueKey struct {
	Rule     string                `json:"rule"`
	Target   string                `json:"target"`
	Plugin   string                `json:"plugin"`
	Instance string                `json:"instance"`
	Service  string                `json:"service"`
	Aspect   string                `json:"aspect"`
	Variant  string                `json:"variant,omitempty"`
	Natural  *observation.Identity `json:"natural,omitempty"`
	Related  []string              `json:"related,omitempty"`
}

type IssueIdentity struct {
	Version     string   `json:"version"`
	Fingerprint string   `json:"fingerprint"`
	ScopeHash   string   `json:"scope_hash"`
	Key         IssueKey `json:"key"`
}

func issueFingerprint(key IssueKey) string {
	return plugin.Hash(struct {
		Version string   `json:"version"`
		Key     IssueKey `json:"key"`
	}{IssueVersion, key})
}
func (id IssueIdentity) storageKey() string { return id.Fingerprint + "/" + id.ScopeHash }

func recordFor(repo *ledger.Repo, key string) *ledger.Record {
	if repo == nil {
		return nil
	}
	for _, r := range repo.Records {
		if r.Kind+"/"+r.Name == key {
			return r
		}
	}
	return nil
}

func recordIdentity(r *ledger.Record) *observation.Identity {
	if r == nil {
		return nil
	}
	spec, _ := field(r.Value, "spec").(map[string]any)
	id := observation.IdentityOf(r.Kind, r.Name, spec)
	return &id
}

func findingService(p string, id *observation.Identity) string {
	if id == nil {
		return ""
	}
	switch p {
	case "ssh":
		return "host"
	case "whois":
		return "found"
	case "cloudflare":
		return "zones"
	}
	if id.Kind == "Domain" {
		return "domains"
	}
	if id.Kind == "Resource" {
		return "cdn"
	}
	return ""
}

func issueIdentities(in *Input, f Finding) []IssueIdentity {
	get := func(key string) string { value, _ := f.Evidence[key].(string); return value }
	key := IssueKey{Rule: f.Rule, Target: f.Target, Plugin: get("plugin"), Instance: get("instance"), Service: get("service")}
	r := recordFor(in.Repo, f.Target)
	aspects := []string{}
	switch f.Rule {
	case "stale_state":
		key.Plugin, key.Instance, _ = strings.Cut(f.Target, "/")
		aspects = []string{"freshness"}
	case "orphan", "zombie":
		if natural, ok := f.Evidence["identity"].(observation.Identity); ok {
			natural = natural.Normalized()
			key.Natural = &natural
		}
		if f.Rule == "zombie" {
			key.Natural = recordIdentity(r)
		}
		key.Service = findingService(key.Plugin, key.Natural)
		aspects = []string{"inventory"}
	case "missing":
		key.Natural = recordIdentity(r)
		aspects = []string{"presence"}
	case "cdn_drift":
		key.Natural = recordIdentity(r)
		key.Service = findingService(key.Plugin, key.Natural)
		if get("aspect") == "origin" {
			differences, _ := f.Evidence["differences"].([]string)
			for _, aspect := range differences {
				aspects = append(aspects, "origin/"+aspect)
			}
		} else {
			aspects = []string{"status"}
			key.Variant = get("area")
		}
	case "origin_exposure":
		natural := observation.Identity{Kind: "Domain", Name: normalizedHost(get("hostname"))}
		key.Natural = &natural
		key.Related = []string{get("server")}
		if get("app") != "" {
			key.Related = append(key.Related, get("app"))
		}
		slices.Sort(key.Related)
		key.Service = "addresses"
		if key.Plugin == "cloudflare" {
			key.Service = "zones.dns_records"
		}
		if get("source") == "ledger" {
			key.Service = "desired_dns"
		}
		aspects = []string{"address/" + get("record_type")}
	}
	ids := []IssueIdentity{}
	for _, aspect := range aspects {
		key.Aspect = aspect
		id, active := currentIdentity(in, key)
		// Historical observations still get an identity even if their declaration has left scope.
		if !active && id.ScopeHash == "" {
			id = IssueIdentity{IssueVersion, issueFingerprint(key), plugin.Hash(map[string]any{"inactive": key}), key}
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a].Key.Aspect < ids[b].Key.Aspect })
	return ids
}

func instanceConfig(repo *ledger.Repo, p, name string) (map[string]any, bool) {
	if repo == nil || repo.Config == nil {
		return nil, false
	}
	value := field(field(field(repo.Config.Value, "plugins"), p), "instances")
	instances, _ := value.(map[string]any)
	cfg, ok := instances[name].(map[string]any)
	return cfg, ok
}

func serviceEnabled(p, service string, cfg map[string]any) bool {
	if p == "dns" {
		return service == "addresses"
	}
	if p == "cloudflare" {
		return service == "zones" || service == "zones.dns_records"
	}
	for _, req := range observation.Requirements() {
		if req.Plugin != p || !contains(req.Services, service) {
			continue
		}
		if selected, ok := cfg["services"].([]any); ok {
			for _, s := range selected {
				if s == service {
					return true
				}
			}
			return false
		}
		return true
	}
	return false
}

// currentIdentity binds only declarations that affect the check, never observations or waiver text.
func currentIdentity(in *Input, key IssueKey) (IssueIdentity, bool) {
	id := IssueIdentity{Version: IssueVersion, Fingerprint: issueFingerprint(key), Key: key}
	scope := map[string]any{"version": IssueVersion, "rule": key.Rule, "max_age_days": in.MaxAge}
	cfg, enabled := instanceConfig(in.Repo, key.Plugin, key.Instance)
	if key.Plugin != "" {
		if !enabled || key.Service != "" && !serviceEnabled(key.Plugin, key.Service, cfg) {
			return id, false
		}
		scope["config_hash"] = plugin.Hash(cfg)
	}
	r := recordFor(in.Repo, key.Target)
	if r != nil {
		scope["natural"] = recordIdentity(r)
		spec, _ := field(r.Value, "spec").(map[string]any)
		scope["account"] = spec["account"]
	}
	switch key.Rule {
	case "stale_state":
		if !enabled {
			return id, false
		}
	case "orphan":
		if key.Natural == nil {
			return id, false
		}
	case "zombie":
		if r == nil || !r.Archived() || key.Natural == nil || recordIdentity(r) == nil || *recordIdentity(r) != *key.Natural {
			return id, false
		}
		if in.lifecycle != nil {
			for _, covered := range in.lifecycle.index[*key.Natural] {
				if !covered.Archived() {
					return id, false
				}
			}
			for _, entry := range in.lifecycle.observed {
				if entry.snapshot.Plugin != key.Plugin || entry.snapshot.Instance != key.Instance {
					continue
				}
				coveredTarget, live := false, false
				for _, covered := range entry.covered {
					coveredTarget = coveredTarget || covered.Kind+"/"+covered.Name == key.Target
					live = live || !covered.Archived()
				}
				if coveredTarget && live {
					return id, false
				}
			}
		}
	case "missing", "cdn_drift":
		if r == nil || r.Archived() || key.Natural == nil || *recordIdentity(r) != *key.Natural {
			return id, false
		}
		watches, problems := observation.Watches(in.Repo)
		if len(problems) != 0 {
			return id, false
		}
		bound := false
		for _, w := range watches {
			if w.Target == key.Target {
				scope["watch_hash"] = plugin.Hash(w)
				bound = w.Plugin == key.Plugin && w.Instance == key.Instance && w.Service == key.Service
			}
		}
		if key.Rule == "missing" && !bound {
			return id, false
		}
		if strings.HasPrefix(key.Aspect, "origin/") {
			origin, ok := field(r.Value, "spec").(map[string]any)["origin"].(map[string]any)
			if !ok || !bound {
				return id, false
			}
			var expected observation.ExpectedOrigin
			data, _ := json.Marshal(origin)
			if json.Unmarshal(data, &expected) != nil {
				return id, false
			}
			for index, host := range expected.Hosts {
				expected.Hosts[index], _ = observation.OriginHost(host)
			}
			slices.Sort(expected.Hosts)
			expected.Hosts = slices.Compact(expected.Hosts)
			if key.Aspect == "origin/port" && expected.Port == 0 || key.Aspect == "origin/protocol" && expected.Protocol == "" {
				return id, false
			}
			scope["expected_origin"] = expected
		}
	case "origin_exposure":
		if r == nil || r.Archived() || key.Natural == nil {
			return id, false
		}
		binding, related, ok := originIssueScope(in, key, r)
		if !ok {
			return id, false
		}
		if key.Plugin == "dns" {
			host, _ := cfg["host"].(string)
			if normalizedHost(host) != key.Natural.Name {
				return id, false
			}
		}
		scope["binding"] = binding
		scope["related"] = related
		if key.Service == "desired_dns" {
			if field(field(r.Value, "spec"), "dns") == nil {
				return id, false
			}
			delete(scope, "max_age_days") // Current declared intent has no observation TTL.
		}
	default:
		return id, false
	}
	id.ScopeHash = plugin.Hash(scope)
	return id, true
}

func originIssueScope(in *Input, key IssueKey, r *ledger.Record) (any, any, bool) {
	if key.Natural == nil || len(key.Related) == 0 {
		return nil, nil, false
	}
	server, app := "", ""
	for _, related := range key.Related {
		if strings.HasPrefix(related, "Server/") {
			server = strings.TrimPrefix(related, "Server/")
		}
		if strings.HasPrefix(related, "App/") {
			app = strings.TrimPrefix(related, "App/")
		}
	}
	serverRecord := recordFor(in.Repo, "Server/"+server)
	if serverRecord == nil || serverRecord.Archived() {
		return nil, nil, false
	}
	bindings, _ := field(field(r.Value, "spec"), "bindings").([]any)
	for _, binding := range bindings {
		name, _ := field(binding, "name").(string)
		if originHostname(name, r.Name) != key.Natural.Name || field(binding, "resource") != nil {
			continue
		}
		direct, _ := field(binding, "server").(string)
		application, _ := field(binding, "app").(string)
		match := direct == server && app == ""
		members := map[string]any{"Server/" + server: recordIdentity(serverRecord)}
		if direct == "" && app != "" && application == app {
			a := recordFor(in.Repo, "App/"+app)
			if a == nil || a.Archived() {
				continue
			}
			deployments, _ := field(field(a.Value, "spec"), "deployments").([]any)
			for _, d := range deployments {
				n, _ := field(d, "server").(string)
				members["Server/"+n] = recordIdentity(recordFor(in.Repo, "Server/"+n))
				match = match || n == server
			}
		}
		if match {
			return map[string]any{"hostname": key.Natural.Name, "server": direct, "app": application}, members, true
		}
	}
	return nil, nil, false
}

// Validate rejects unknown versions and noncanonical keys before a history consumer uses them.
func (id IssueIdentity) Validate() error {
	validDigest := func(value string) bool {
		if len(value) != 64 {
			return false
		}
		for _, c := range value {
			if c < '0' || c > '9' && c < 'a' || c > 'f' {
				return false
			}
		}
		return true
	}
	if id.Version != IssueVersion || !validDigest(id.ScopeHash) || id.Fingerprint != issueFingerprint(id.Key) || !slices.ContainsFunc(Rules(), func(r Rule) bool { return r.ID == id.Key.Rule }) || id.Key.Target == "" || id.Key.Aspect == "" {
		return fmt.Errorf("invalid issue identity or version")
	}
	key := id.Key
	if key.Natural != nil {
		n := *key.Natural
		if n != n.Normalized() {
			return fmt.Errorf("noncanonical natural identity")
		}
		valid := false
		switch n.Kind {
		case "Domain":
			valid = n.Name != "" && n == (observation.Identity{Kind: "Domain", Name: n.Name})
		case "Server":
			valid = n.Address != "" && n == (observation.Identity{Kind: "Server", Address: n.Address})
		case "Resource":
			valid = n.Provider != "" && (n.ResourceID != "" && n == (observation.Identity{Kind: "Resource", Provider: n.Provider, ResourceID: n.ResourceID}) || n.ResourceID == "" && n.Type != "" && n.Endpoint != "" && n == (observation.Identity{Kind: "Resource", Provider: n.Provider, Type: n.Type, Endpoint: n.Endpoint}))
		}
		if !valid {
			return fmt.Errorf("invalid natural identity shape")
		}
	}
	valid := key.Plugin != "" && key.Instance != ""
	switch key.Rule {
	case "stale_state":
		valid = valid && key.Target == key.Plugin+"/"+key.Instance && key.Aspect == "freshness" && key.Service == "" && key.Natural == nil
	case "orphan", "zombie":
		valid = valid && key.Natural != nil && key.Aspect == "inventory" && key.Service == findingService(key.Plugin, key.Natural)
	case "missing":
		valid = valid && key.Natural != nil && (key.Natural.Kind == "Domain" || key.Natural.Kind == "Resource") && key.Aspect == "presence" && contains([]string{"domains", "cdn", "zones"}, key.Service)
	case "cdn_drift":
		valid = valid && key.Natural != nil && key.Natural.Kind == "Resource" && key.Service == findingService(key.Plugin, key.Natural) && contains([]string{"status", "origin/hosts", "origin/port", "origin/protocol"}, key.Aspect)
	case "origin_exposure":
		valid = key.Natural != nil && key.Natural.Kind == "Domain" && contains([]string{"address/A", "address/AAAA"}, key.Aspect) && len(key.Related) > 0 && len(key.Related) <= 2 && slices.IsSorted(key.Related) && (key.Plugin == "" && key.Instance == "" && key.Service == "desired_dns" || key.Plugin == "dns" && key.Instance != "" && key.Service == "addresses" || key.Plugin == "cloudflare" && key.Instance != "" && key.Service == "zones.dns_records")
	}
	if !valid || key.Rule != "origin_exposure" && len(key.Related) != 0 || key.Variant != "" && !(key.Rule == "cdn_drift" && key.Aspect == "status") {
		return fmt.Errorf("invalid issue key shape")
	}
	if key.Service != "" && key.Plugin != "" && !serviceEnabled(key.Plugin, key.Service, nil) || key.Natural != nil && key.Natural.Kind == "Resource" && key.Natural.Provider != key.Plugin {
		return fmt.Errorf("invalid issue service scope")
	}
	if key.Rule == "origin_exposure" {
		servers, apps := 0, 0
		for _, related := range key.Related {
			switch {
			case strings.HasPrefix(related, "Server/") && len(related) > len("Server/"):
				servers++
			case strings.HasPrefix(related, "App/") && len(related) > len("App/"):
				apps++
			default:
				return fmt.Errorf("invalid issue relationship")
			}
		}
		if servers != 1 || apps > 1 {
			return fmt.Errorf("invalid issue relationship")
		}
	}
	return nil
}
