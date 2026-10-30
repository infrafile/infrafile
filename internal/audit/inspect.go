package audit

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/snapshot"
)

type InspectOptions struct {
	Now        time.Time
	MaxAgeDays *int
	// Nil selects all assets and configured instances; an empty non-nil list selects none.
	Keys []string
}

type Inspection struct {
	Version    string             `json:"version"`
	OK         bool               `json:"ok"`
	AsOf       string             `json:"as_of"`
	MaxAgeDays *int               `json:"max_age_days"`
	Assets     []InspectAsset     `json:"assets"`
	Relations  []InspectRelation  `json:"relations"`
	Execution  []InspectExecution `json:"execution"`
	Coverage   InspectCoverage    `json:"coverage"`
	Risk       InspectRisk        `json:"risk"`
	Notices    []Notice           `json:"notices"`
	History    *HistoryView       `json:"history,omitempty"`
}

type InspectAsset struct {
	Key      string `json:"key"`
	File     string `json:"file"`
	Archived bool   `json:"archived"`
}

type InspectRelation struct {
	From string `json:"from"`
	To   string `json:"to"`
	Path string `json:"path"`
}

type InspectExecution struct {
	Plugin       string  `json:"plugin"`
	Instance     string  `json:"instance"`
	Status       string  `json:"status"`
	Code         string  `json:"code"`
	AttemptedAt  string  `json:"attempted_at,omitempty"`
	ObservedAt   *string `json:"observed_at"`
	SnapshotFile string  `json:"snapshot_file"`
	ReceiptFile  string  `json:"receipt_file"`
}

// CheckScope contains no observed values. It fixes the denominator and NA identity.
type CheckScope struct {
	Rule     string `json:"rule"`
	Target   string `json:"target"`
	Plugin   string `json:"plugin"`
	Instance string `json:"instance"`
	Service  string `json:"service"`
	Aspect   string `json:"aspect"`
}

func (s CheckScope) ID() string {
	b, _ := json.Marshal(s)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

type Check struct {
	CheckScope
	ID            string            `json:"id"`
	Status        string            `json:"status"`
	Code          string            `json:"code"`
	Message       string            `json:"message"`
	Evidence      []InspectEvidence `json:"evidence"`
	NotApplicable *NotApplicable    `json:"not_applicable"`
}

type InspectEvidence struct {
	File       string  `json:"file"`
	ObservedAt *string `json:"observed_at"`
	MaxAgeDays *int    `json:"max_age_days"`
}

type NotApplicable struct {
	Check    string `json:"check"`
	Reason   string `json:"reason"`
	Owner    string `json:"owner"`
	ReviewBy string `json:"review_by"`
}

type InspectCoverage struct {
	Expected      int     `json:"expected"`
	Checked       int     `json:"checked"`
	Unknown       int     `json:"unknown"`
	NotApplicable int     `json:"not_applicable"`
	Checks        []Check `json:"checks"`
}

type InspectFinding struct {
	Finding
	Assessment string `json:"assessment"`
}

type InspectRisk struct {
	Confirmed int              `json:"confirmed"`
	Unknown   int              `json:"unknown"`
	Waived    int              `json:"waived"`
	Findings  []InspectFinding `json:"findings"`
}

type inspector struct {
	in        *Input
	report    *Inspection
	config    map[string]map[string]map[string]any
	snaps     map[string]Snapshot
	attempts  map[string]snapshot.Attempt
	watches   map[string]observation.Watch
	baselines map[string]snapshot.PresenceEntry
	selected  map[string]bool
}

// Inspect uses the audit rules, while qualifying coverage independently from findings.
// The caller validates the ledger first. This function never invokes a collector or resolves secrets.
func Inspect(ctx context.Context, repo *ledger.Repo, opts InspectOptions) (*Inspection, error) {
	i, err := inspectWithHistory(ctx, repo, opts)
	if err != nil {
		return nil, err
	}
	return i.report, nil
}

func inspectWithHistory(ctx context.Context, repo *ledger.Repo, opts InspectOptions) (*inspector, error) {
	i, err := inspect(ctx, repo, opts)
	if err != nil {
		return nil, err
	}
	var checks []Check
	if opts.Keys != nil {
		checks = i.report.Coverage.Checks
	}
	i.report.History, err = ReadHistoryView(ctx, repo.Dir, checks)
	if err != nil {
		return nil, err
	}
	return i, nil
}

func inspect(ctx context.Context, repo *ledger.Repo, opts InspectOptions) (*inspector, error) {
	in, err := prepareInspection(ctx, repo, opts)
	if err != nil {
		return nil, err
	}
	return inspectPrepared(ctx, in, opts.Keys)
}

func inspectPrepared(ctx context.Context, prepared *inspectionInput, keys []string) (*inspector, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	records, err := prepared.selectRecords(keys)
	if err != nil {
		return nil, err
	}
	in := prepared.in
	report := &Inspection{Version: "v1", AsOf: in.Now.UTC().Format(time.RFC3339), MaxAgeDays: in.MaxAge,
		Assets: []InspectAsset{}, Relations: []InspectRelation{}, Execution: []InspectExecution{},
		Coverage: InspectCoverage{Checks: []Check{}}, Risk: InspectRisk{Findings: []InspectFinding{}}, Notices: append([]Notice{}, prepared.notices...)}
	i := &inspector{in: in, report: report, config: prepared.config, snaps: prepared.snaps,
		attempts: prepared.attempts, watches: prepared.watches, baselines: prepared.baselines, selected: map[string]bool{}}
	for _, r := range records {
		key := r.Kind + "/" + r.Name
		i.selected[key] = true
		report.Assets = append(report.Assets, InspectAsset{key, r.File, r.Archived()})
	}
	for _, index := range selectedIndices(i.selected, prepared.relationsByKey) {
		report.Relations = append(report.Relations, prepared.relations[index])
	}
	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := i.assetChecks(r); err != nil {
			return nil, err
		}
	}
	if keys == nil {
		if err := i.instanceChecks(ctx); err != nil {
			return nil, err
		}
	}
	used := map[string]bool{}
	for _, c := range report.Coverage.Checks {
		if c.Plugin != "" {
			used[c.Plugin+"/"+c.Instance] = true
		}
	}
	for p, instances := range i.config {
		for name := range instances {
			if keys != nil && !used[p+"/"+name] {
				continue
			}
			s := i.snaps[p+"/"+name]
			a := i.attempts[p+"/"+name]
			code, _ := i.qualify(p, name)
			state := "unknown"
			if code == "checked" || code == "stale" || code == "ttl_unset" {
				state = "succeeded"
			}
			if code == "failed" || code == "running" {
				state = code
			}
			report.Execution = append(report.Execution, InspectExecution{p, name, state, code, a.AttemptedAt, stamp(s.ObservedAt), s.File, "state/" + snapshot.StatusFile})
		}
	}
	findings := prepared.findings
	if keys != nil {
		findings = []Finding{}
		for _, index := range selectedIndices(i.selected, prepared.findingsByKey) {
			findings = append(findings, prepared.findings[index])
		}
	}
	for _, f := range findings {
		assessment := "unknown"
		for _, c := range report.Coverage.Checks {
			if qualifiesFinding(c, f) {
				assessment = "confirmed"
			}
		}
		if f.Rule == "origin_exposure" && f.Evidence["source"] == "ledger" {
			assessment = "confirmed"
		}
		report.Risk.Findings = append(report.Risk.Findings, InspectFinding{f, assessment})
		if f.Waiver != nil {
			report.Risk.Waived++
		}
		if assessment == "unknown" {
			report.Risk.Unknown++
		} else if f.Waiver == nil {
			report.Risk.Confirmed++
		}
	}
	if err := i.applyNA(); err != nil {
		return nil, err
	}
	if keys != nil {
		selectedNotices := []Notice{}
		for _, n := range report.Notices {
			keep := i.selected[n.Target] || used[n.Target]
			for source := range used {
				keep = keep || strings.HasPrefix(n.Target, source+"/")
			}
			if keep {
				selectedNotices = append(selectedNotices, n)
			}
		}
		report.Notices = selectedNotices
	}
	for _, c := range report.Coverage.Checks {
		report.Coverage.Expected++
		switch c.Status {
		case "checked":
			report.Coverage.Checked++
		case "not_applicable":
			report.Coverage.NotApplicable++
		default:
			report.Coverage.Unknown++
		}
	}
	sort.Slice(report.Assets, func(a, b int) bool { return report.Assets[a].Key < report.Assets[b].Key })
	sort.Slice(report.Relations, func(a, b int) bool {
		x, y := report.Relations[a], report.Relations[b]
		return x.From+"\x00"+x.To+"\x00"+x.Path < y.From+"\x00"+y.To+"\x00"+y.Path
	})
	sort.Slice(report.Execution, func(a, b int) bool {
		x, y := report.Execution[a], report.Execution[b]
		return x.Plugin+"/"+x.Instance < y.Plugin+"/"+y.Instance
	})
	sort.Slice(report.Coverage.Checks, func(a, b int) bool {
		x, y := report.Coverage.Checks[a], report.Coverage.Checks[b]
		return x.Target+"\x00"+x.Rule+"\x00"+x.Aspect+"\x00"+x.Plugin+"/"+x.Instance+"/"+x.Service < y.Target+"\x00"+y.Rule+"\x00"+y.Aspect+"\x00"+y.Plugin+"/"+y.Instance+"/"+y.Service
	})
	report.OK = len(report.Assets) > 0 && report.Coverage.Checked > 0 && report.Coverage.Unknown == 0 && report.Risk.Confirmed == 0 && report.Risk.Unknown == 0
	for _, e := range report.Execution {
		report.OK = report.OK && e.Status == "succeeded" && e.Code == "checked"
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return i, nil
}

func qualifiesFinding(c Check, f Finding) bool {
	if c.Rule != f.Rule {
		return false
	}
	if f.Rule == "stale_state" {
		return c.Target == f.Target && c.Code == "stale"
	}
	if c.Code != "checked" {
		return false
	}
	if p, ok := f.Evidence["plugin"].(string); ok && (p != c.Plugin || f.Evidence["instance"] != c.Instance) {
		return false
	}
	if c.Target != f.Target && f.Rule != "orphan" && f.Rule != "zombie" {
		return false
	}
	if aspect, ok := f.Evidence["aspect"].(string); ok && aspect != c.Aspect {
		if aspect != "origin" || !strings.HasPrefix(c.Aspect, "origin/") {
			return false
		}
		differences, _ := f.Evidence["differences"].([]string)
		if !contains(differences, strings.TrimPrefix(c.Aspect, "origin/")) {
			return false
		}
	}
	if f.Rule == "origin_exposure" {
		host, _ := f.Evidence["hostname"].(string)
		return strings.HasSuffix(c.Aspect, "/"+host)
	}
	if f.Rule == "orphan" || f.Rule == "zombie" {
		id, ok := f.Evidence["identity"].(observation.Identity)
		if !ok {
			return false
		}
		switch id.Kind {
		case "Domain":
			return c.Service == "domains" || c.Plugin == "whois" && c.Service == "found"
		case "Server":
			return c.Plugin == "ssh" && c.Service == "host"
		case "Resource":
			return id.Type == "cdn" && c.Service == "cdn" || id.Type == "dns-zone" && c.Service == "zones"
		}
		return false
	}
	return true
}

func contains(list []string, value string) bool {
	for _, s := range list {
		if s == value {
			return true
		}
	}
	return false
}
func stamp(at *time.Time) *string {
	if at == nil {
		return nil
	}
	value := at.UTC().Format(time.RFC3339)
	return &value
}

func (i *inspector) add(scope CheckScope, code, message string, evidence ...Snapshot) {
	for _, existing := range i.report.Coverage.Checks {
		if existing.ID == scope.ID() {
			return
		}
	}
	c := Check{CheckScope: scope, ID: scope.ID(), Status: "unknown", Code: code, Message: message, Evidence: []InspectEvidence{}}
	if code == "checked" {
		c.Status = "checked"
	}
	for _, s := range evidence {
		c.Evidence = append(c.Evidence, InspectEvidence{s.File, stamp(s.ObservedAt), i.in.MaxAge})
	}
	i.report.Coverage.Checks = append(i.report.Coverage.Checks, c)
}

func (i *inspector) qualify(p, name string) (string, string) {
	s := i.snaps[p+"/"+name]
	a := i.attempts[p+"/"+name]
	if a.ConfigHash != plugin.Hash(i.config[p][name]) {
		return "unverified", "执行回执缺失或不匹配当前配置"
	}
	at, _ := time.Parse(time.RFC3339, a.AttemptedAt)
	if at.After(i.in.Now) {
		return "future", "执行时间在未来"
	}
	if s.ObservedAt != nil && s.ObservedAt.After(at) {
		return "unverified", "快照时间晚于执行回执"
	}
	if a.Status == "failed" {
		return "failed", "最近采集失败 [" + a.Code + "]；旧快照不能证明本轮覆盖"
	}
	if a.Status == "running" {
		return "running", "最近采集缺少结束回执"
	}
	if !s.verified || s.ObservedAt == nil || !s.ObservedAt.Equal(at) || a.Status != "succeeded" || a.SnapshotHash != s.hash {
		return "unverified", "没有与配置、成功时间和快照匹配的成功回执"
	}
	if i.in.MaxAge == nil {
		return "ttl_unset", "未设置 --max-age，不能证明新鲜覆盖"
	}
	days := *s.AgeDays(i.in.Now)
	if days > *i.in.MaxAge || days == *i.in.MaxAge && i.in.Now.Sub(*s.ObservedAt)%(24*time.Hour) > 0 {
		return "stale", "观测已超过 --max-age"
	}
	return "checked", "匹配当前配置和快照的新鲜成功证据"
}

func (i *inspector) applyNA() error {
	items, _ := field(field(i.in.Repo.Config.Value, "audit"), "not_applicable").([]any)
	seen := map[string]bool{}
	for _, item := range items {
		b, _ := json.Marshal(item)
		var n NotApplicable
		if json.Unmarshal(b, &n) != nil || seen[n.Check] {
			return fmt.Errorf("invalid or repeated NA declaration")
		}
		seen[n.Check] = true
		found := false
		for index := range i.report.Coverage.Checks {
			c := &i.report.Coverage.Checks[index]
			if c.ID != n.Check {
				continue
			}
			found = true
			c.NotApplicable = &n
			if n.ReviewBy < i.in.Now.UTC().Format(time.DateOnly) {
				i.report.Notices = append(i.report.Notices, Notice{Code: "na_expired", Rule: c.Rule, Target: c.Target, File: i.in.Repo.Config.File, Message: "NA 复核期限已过，未排除应检项"})
				continue
			}
			c.Status = "not_applicable"
		}
		if !found {
			i.report.Notices = append(i.report.Notices, Notice{Code: "na_unused", File: i.in.Repo.Config.File, Target: n.Check, Message: "NA 未匹配当前选择中的检查项；未更改分母"})
		}
	}
	return nil
}

func normalizedHost(value string) string { return strings.TrimSuffix(strings.ToLower(value), ".") }
