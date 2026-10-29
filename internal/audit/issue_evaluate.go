package audit

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
)

type IssueEvaluation struct {
	Identity IssueIdentity `json:"identity"`
	Result   string        `json:"result"`
	InScope  bool          `json:"in_scope"`
	Found    bool          `json:"found"`
	Code     string        `json:"code"`
	Waiver   *Waiver       `json:"waiver"`
}

type IssueAssessment struct {
	Inspection  *Inspection       `json:"inspection"`
	Evaluations []IssueEvaluation `json:"evaluations"`
}

// EvaluateIssues assesses current and previously known issues from the same read-only inspection.
// A missing finding alone is never proof of recovery.
func EvaluateIssues(ctx context.Context, repo *ledger.Repo, opts InspectOptions, known []IssueIdentity) (*IssueAssessment, error) {
	for _, id := range known {
		if err := id.Validate(); err != nil {
			return nil, err
		}
	}
	i, err := inspect(ctx, repo, opts)
	if err != nil {
		return nil, err
	}
	identities := map[string]IssueIdentity{}
	found := map[string]bool{}
	currentWaivers := map[string]*Waiver{}
	for _, id := range known {
		if _, duplicate := identities[id.storageKey()]; duplicate {
			return nil, fmt.Errorf("repeated known issue")
		}
		identities[id.storageKey()] = id
	}
	for _, finding := range i.report.Risk.Findings {
		for _, id := range finding.Issues {
			identities[id.storageKey()] = id
			found[id.storageKey()] = true
			currentWaivers[id.storageKey()] = finding.Waiver
		}
	}
	result := &IssueAssessment{Inspection: i.report, Evaluations: []IssueEvaluation{}}
	for _, id := range identities {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		evaluation := IssueEvaluation{Identity: id, Result: "unknown", Code: "unverified", Found: found[id.storageKey()]}
		current, active := currentIdentity(i.in, id.Key)
		selected := opts.Keys == nil || i.selected[id.Key.Target]
		if id.Key.Rule == "orphan" || id.Key.Rule == "stale_state" {
			for _, check := range i.report.Coverage.Checks {
				selected = selected || check.Plugin == id.Key.Plugin && check.Instance == id.Key.Instance
			}
		}
		evaluation.InScope = active && selected && current.ScopeHash == id.ScopeHash
		evaluation.Waiver = i.issueWaiver(id.Key)
		if evaluation.Found {
			evaluation.Waiver = currentWaivers[id.storageKey()]
		}
		if !evaluation.InScope {
			evaluation.Code = "out_of_scope"
		} else {
			qualified, code := i.issueQualified(id.Key, evaluation.Found)
			evaluation.Code = code
			if qualified {
				evaluation.Result = "clear"
				if evaluation.Found {
					evaluation.Result = "confirmed"
				}
			}
		}
		result.Evaluations = append(result.Evaluations, evaluation)
	}
	sort.Slice(result.Evaluations, func(a, b int) bool {
		return result.Evaluations[a].Identity.storageKey() < result.Evaluations[b].Identity.storageKey()
	})
	return result, nil
}

func (i *inspector) issueWaiver(key IssueKey) *Waiver {
	year, month, day := i.in.Now.Date()
	today := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	for _, entry := range waivers(i.in.Repo) {
		if entry.rule != key.Rule {
			continue
		}
		matches := entry.target == key.Target
		if entry.identity != nil {
			matches = key.Natural != nil && *entry.identity == *key.Natural && key.Plugin == entry.plugin && key.Instance == entry.instance
			if key.Rule == "zombie" && key.Plugin == entry.plugin && key.Instance == entry.instance {
				if r := recordFor(i.in.Repo, key.Target); r != nil {
					spec, _ := field(r.Value, "spec").(map[string]any)
					matches = matches || *entry.identity == observation.EndpointIdentity(spec)
				}
			}
		}
		if until, err := time.Parse(time.DateOnly, entry.waiver.Until); err == nil && until.Before(today) {
			continue
		}
		if matches {
			copy := entry.waiver
			return &copy
		}
	}
	return nil
}

func (i *inspector) issueQualified(key IssueKey, found bool) (bool, string) {
	if key.Rule == "origin_exposure" && key.Service == "desired_dns" {
		return true, "declared_intent"
	}
	for _, check := range i.report.Coverage.Checks {
		if !issueMatchesCheck(key, check) {
			continue
		}
		if check.Status == "not_applicable" && !found {
			return false, "not_applicable"
		}
		if key.Rule == "stale_state" && check.Code == "stale" {
			return true, "stale"
		}
		if check.Code != "checked" {
			return false, check.Code
		}
		if key.Rule == "orphan" || key.Rule == "zombie" || key.Rule == "cdn_drift" && key.Aspect == "status" {
			return i.inventoryIssueQualified(key, found)
		}
		return true, "checked"
	}
	return false, "not_configured"
}

func issueMatchesCheck(key IssueKey, check Check) bool {
	if check.Rule != key.Rule || check.Plugin != key.Plugin || check.Instance != key.Instance || check.Service != key.Service {
		return false
	}
	if check.Target != key.Target && !(key.Rule == "orphan" || key.Rule == "zombie") {
		return false
	}
	if key.Rule == "origin_exposure" {
		if key.Natural == nil || !strings.HasSuffix(check.Aspect, "/"+key.Natural.Name) {
			return false
		}
		// The binding must include this exact Server/App relationship, not another hostname peer.
		server, app := "", ""
		for _, related := range key.Related {
			if strings.HasPrefix(related, "Server/") {
				server = strings.TrimPrefix(related, "Server/")
			}
			if strings.HasPrefix(related, "App/") {
				app = strings.TrimPrefix(related, "App/")
			}
		}
		if app != "" {
			return strings.Contains(check.Aspect, "/app/"+app+"/")
		}
		return strings.Contains(check.Aspect, "/server/"+server+"/")
	}
	return key.Aspect == check.Aspect
}

func (i *inspector) inventoryIssueQualified(key IssueKey, found bool) (bool, string) {
	s := i.snaps[key.Plugin+"/"+key.Instance]
	value, exists := s.Spec[key.Service]
	if !exists {
		return false, "incomplete"
	}
	if key.Rule == "cdn_drift" && key.Aspect == "status" && key.Variant == "" && !found {
		items, _ := value.([]any)
		for _, item := range items {
			area, _ := field(item, "area").(string)
			if area == "" {
				continue
			}
			identities, _, err := observation.WatchRecords(key.Plugin, key.Instance, key.Service, map[string]any{key.Service: []any{item}})
			if err != nil {
				return false, "identity_unknown"
			}
			if key.Natural != nil && identities[*key.Natural] {
				return false, "variant_changed"
			}
		}
	}
	if key.Rule == "cdn_drift" && key.Aspect == "status" && key.Variant != "" {
		items, _ := value.([]any)
		seen := false
		for _, item := range items {
			if field(item, "area") != key.Variant {
				continue
			}
			identities, _, err := observation.WatchRecords(key.Plugin, key.Instance, key.Service, map[string]any{key.Service: []any{item}})
			if err != nil {
				return false, "identity_unknown"
			}
			if key.Natural != nil && identities[*key.Natural] {
				seen = true
			}
			if r := recordFor(i.in.Repo, key.Target); r != nil {
				spec, _ := field(r.Value, "spec").(map[string]any)
				seen = seen || identities[observation.EndpointIdentity(spec)]
			}
		}
		if !seen {
			return false, "variant_not_observed"
		}
	}
	if !found && (key.Rule == "orphan" || key.Rule == "zombie") && key.Natural != nil && key.Natural.Kind == "Resource" {
		items, _ := value.([]any)
		for _, item := range items {
			mapped, _, err := observation.Map(key.Plugin, key.Instance, s.File, map[string]any{key.Service: []any{item}})
			if err != nil {
				return false, "identity_unknown"
			}
			for _, record := range mapped {
				current := observation.IdentityOf(record.Kind, record.Name, record.Spec)
				// An omitted provider ID cannot prove an old ID disappeared. A newly supplied ID
				// for the same endpoint is an identity change, not evidence that the old issue cleared.
				if key.Natural.ResourceID != "" && current.ResourceID == "" || key.Natural.ResourceID == "" && current.ResourceID != "" && observation.EndpointIdentity(record.Spec) == *key.Natural {
					return false, "identity_unknown"
				}
			}
		}
	}
	return true, "checked"
}
