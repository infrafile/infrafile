package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
)

const opsVersion = "infrafile-ops/v1"

type opsEvidence struct {
	Status         string  `json:"status"`
	Reason         string  `json:"reason"`
	ObservedAt     *string `json:"observed_at"`
	Source         string  `json:"source"`
	MaxAgeSeconds  int     `json:"max_age_seconds"`
	ObservedStatus string  `json:"observed_status,omitempty"`
	Freshness      string  `json:"freshness,omitempty"`
}
type opsServer struct {
	Key          string            `json:"key"`
	Name         string            `json:"name"`
	Role         string            `json:"role"`
	Hardware     map[string]string `json:"hardware"`
	Snapshot     opsEvidence       `json:"snapshot"`
	Disks        []opsDisk         `json:"disks"`
	Applications []string          `json:"applications"`
	Notice       string            `json:"notice"`
}
type opsDisk struct {
	Mount     string `json:"mount"`
	Total     int64  `json:"total"`
	Available int64  `json:"available"`
	Percent   int    `json:"percent"`
}
type opsApplication struct {
	Key         string         `json:"key"`
	Name        string         `json:"name"`
	Category    string         `json:"category"`
	Deployments []string       `json:"deployments"`
	Notice      string         `json:"notice"`
	Production  *opsProduction `json:"production,omitempty"`
}

// Production observations are partial facts, never capability grants.
type opsProduction struct {
	Boundary string              `json:"boundary"`
	Gates    []opsProductionGate `json:"gates"`
}
type opsProductionGate struct {
	ID           string                     `json:"id"`
	Requirements []string                   `json:"requirements"`
	Observations []opsProductionObservation `json:"observations"`
}
type opsProductionObservation struct {
	ID              string      `json:"id"`
	Hosts           []string    `json:"hosts"`
	Summary         string      `json:"summary"`
	Reference       string      `json:"reference"`
	ReferenceSHA256 string      `json:"reference_sha256"`
	Evidence        opsEvidence `json:"evidence"`
}
type opsDeployment struct {
	ID          string         `json:"id"`
	Application string         `json:"application"`
	Host        string         `json:"host"`
	Kind        string         `json:"kind"`
	Selector    string         `json:"selector"`
	Runtime     opsEvidence    `json:"runtime"`
	Readiness   opsEvidence    `json:"readiness"`
	Health      opsEvidence    `json:"health"`
	Resources   []string       `json:"resources"`
	Checks      []string       `json:"checks"`
	Criteria    []opsCriterion `json:"criteria,omitempty"`
	Notice      string         `json:"notice"`
}

// Criteria describe configured checks and their independently aged observations.
// They grant no execution permission and never roll up to full business health.
type opsCriterion struct {
	ID           string      `json:"id"`
	Component    string      `json:"component"`
	Kind         string      `json:"kind"`
	Layer        string      `json:"layer"`
	WorkloadMode string      `json:"workload_mode"`
	DataMode     string      `json:"data_mode"`
	Selector     string      `json:"selector"`
	Observer     string      `json:"observer"`
	Evidence     opsEvidence `json:"evidence"`
}
type opsResource struct {
	Key   string   `json:"key"`
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	Hosts []string `json:"hosts"`
}
type opsMonitor struct {
	ID                int         `json:"id"`
	Name              string      `json:"name"`
	Type              string      `json:"type"`
	Active            *bool       `json:"active"`
	Interval          *int        `json:"interval"`
	Targets           []string    `json:"targets"`
	AssociationReason string      `json:"association_reason"`
	Configuration     opsEvidence `json:"configuration"`
	State             opsEvidence `json:"state"`
}
type opsSummary struct {
	Servers           int `json:"servers"`
	Applications      int `json:"applications"`
	Deployments       int `json:"deployments"`
	Resources         int `json:"resources"`
	ExpectedMonitors  int `json:"expected_monitors"`
	ObservedMonitors  int `json:"observed_monitors"`
	RuntimeIdentified int `json:"runtime_identified"`
}
type opsOverview struct {
	Version            string              `json:"version"`
	CapturedAt         string              `json:"captured_at"`
	AuthorityFiles     map[string]string   `json:"authority_files,omitempty"`
	LedgerFiles        map[string]string   `json:"ledger_files,omitempty"`
	Summary            opsSummary          `json:"summary"`
	Servers            []opsServer         `json:"servers"`
	Applications       []opsApplication    `json:"applications"`
	Deployments        []opsDeployment     `json:"deployments"`
	Resources          []opsResource       `json:"resources"`
	Monitors           []opsMonitor        `json:"monitors"`
	CollectionErrors   []string            `json:"collection_errors"`
	Enabled            bool                `json:"enabled"`
	EvaluatedAt        string              `json:"evaluated_at"`
	Alignment          string              `json:"alignment"`
	ReadCapabilities   []opsReadCapability `json:"read_capabilities,omitempty"`
	ActionCapabilities []opsReadCapability `json:"action_capabilities,omitempty"`
	Refresh            *opsRefreshState    `json:"refresh,omitempty"`
	Recoveries         []opsRecoveryState  `json:"recoveries,omitempty"`
	Operations         []opsOperationRow   `json:"operations,omitempty"`
}

var opsIP = regexp.MustCompile(`(?:[0-9]{1,3}\.){3}[0-9]{1,3}`)
var opsDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var opsCriterionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Read bounded ordinary files only, including every parent below a pinned root.
func opsRead(root, relative string, limit int64) ([]byte, error) {
	rootInfo, rootErr := os.Lstat(root)
	if rootErr != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("invalid Ops root")
	}
	if filepath.IsAbs(relative) || filepath.ToSlash(filepath.Clean(relative)) != relative || relative == "." || strings.Contains(relative, "\\") || strings.HasPrefix(relative, "../") {
		return nil, fmt.Errorf("invalid Ops input")
	}
	path := root
	for _, part := range strings.Split(relative, "/") {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("invalid Ops input")
		}
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > limit {
		return nil, fmt.Errorf("invalid Ops input")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read Ops input")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("Ops input changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	current, statErr := os.Lstat(path)
	after, afterErr := f.Stat()
	if err != nil || statErr != nil || afterErr != nil || int64(len(data)) > limit || !os.SameFile(before, current) || current.Size() != before.Size() || !current.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return nil, fmt.Errorf("Ops input changed")
	}
	return data, nil
}

func opsAge(e *opsEvidence, now time.Time) error {
	switch e.Status {
	case "PASS", "WARN", "FAIL", "UNKNOWN", "STALE", "PAUSED", "NA":
	default:
		return fmt.Errorf("invalid evidence state")
	}
	if e.MaxAgeSeconds <= 0 || e.MaxAgeSeconds > 32*86400 {
		return fmt.Errorf("invalid evidence window")
	}
	e.ObservedStatus = e.Status
	e.Freshness = "missing"
	if e.ObservedAt != nil {
		stamp, err := time.Parse(time.RFC3339Nano, *e.ObservedAt)
		if err != nil {
			return fmt.Errorf("invalid observation time")
		}
		e.Freshness = "fresh"
		if stamp.After(now.Add(5 * time.Minute)) {
			e.Freshness = "future"
		} else if now.Sub(stamp) > time.Duration(e.MaxAgeSeconds)*time.Second {
			e.Freshness = "stale"
		}
	}
	if e.Status == "PAUSED" || e.Status == "NA" {
		return nil
	}
	if e.Freshness == "future" || e.Freshness == "missing" {
		e.Status = "UNKNOWN"
	} else if e.Freshness == "stale" {
		e.Status = "STALE"
	}
	return nil
}

func (w *workbench) opsOverview(ctx context.Context) (any, error) {
	if w.opsFile == "" {
		return map[string]any{"version": opsVersion, "enabled": false}, nil
	}
	data, err := opsRead(filepath.Dir(w.opsFile), filepath.Base(w.opsFile), 2<<20)
	if err != nil {
		return nil, err
	}
	if opsIP.Match(data) || bytes.Contains(data, []byte("sops://")) {
		return nil, fmt.Errorf("unsafe Ops input")
	}
	_, problems, err := ledger.ParseDocument(ctx, "ops.json", data)
	if err != nil || len(problems) != 0 {
		return nil, fmt.Errorf("invalid Ops document")
	}
	var result opsOverview
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.Version != opsVersion {
		return nil, fmt.Errorf("invalid Ops document")
	}
	if len(result.ReadCapabilities) != 0 || len(result.ActionCapabilities) != 0 || result.Refresh != nil || len(result.Recoveries) != 0 || len(result.Operations) != 0 {
		return nil, fmt.Errorf("reader capabilities must be derived from the local binding")
	}
	if _, err := time.Parse(time.RFC3339Nano, result.CapturedAt); err != nil {
		return nil, fmt.Errorf("invalid capture time")
	}
	capture, _ := time.Parse(time.RFC3339Nano, result.CapturedAt)
	if capture.After(w.now().Add(5 * time.Minute)) {
		return nil, fmt.Errorf("future Ops generation")
	}
	if len(result.AuthorityFiles) == 0 || len(result.AuthorityFiles) > 8192 || len(result.LedgerFiles) == 0 || len(result.LedgerFiles) > 8192 || len(result.Monitors) > 100 {
		return nil, fmt.Errorf("invalid Ops bounds")
	}
	// The file is an attachment to this exact ledger, never an alternative editing source.
	repo, err := loadQueryRepo(ctx, w.dir)
	if err != nil {
		return nil, err
	}
	if len(repo.Files)+1 != len(result.LedgerFiles) {
		return nil, fmt.Errorf("Ops ledger generation differs")
	}
	for _, path := range append([]string{"infrafile.yaml"}, repo.Files...) {
		expected, ok := result.LedgerFiles[path]
		if !ok || !opsDigest.MatchString(expected) {
			return nil, fmt.Errorf("invalid ledger binding")
		}
		content, err := opsRead(w.dir, path, 2<<20)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(content)
		if hex.EncodeToString(hash[:]) != expected {
			return nil, fmt.Errorf("Ops ledger generation differs")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	result.Alignment = "unverified"
	if w.opsAuthority != "" {
		result.Alignment = "matched"
		root, err := os.Lstat(w.opsAuthority)
		if err != nil || !os.SameFile(root, w.opsAuthorityInfo) {
			result.Alignment = "changed"
		}
		for path, expected := range result.AuthorityFiles {
			if !opsDigest.MatchString(expected) {
				return nil, fmt.Errorf("invalid source binding")
			}
			content, err := opsRead(w.opsAuthority, path, 64<<20)
			if err != nil {
				result.Alignment = "changed"
				continue
			}
			hash := sha256.Sum256(content)
			if hex.EncodeToString(hash[:]) != expected {
				result.Alignment = "changed"
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
	}
	if err := validateOps(&result, w.now()); err != nil {
		return nil, err
	}
	live := map[string]string{}
	counts := map[string]int{}
	for _, record := range repo.Records {
		if !record.Archived() {
			live[record.Kind+"/"+record.Name] = record.Kind
			counts[record.Kind]++
		}
	}
	if counts["Server"] != result.Summary.Servers || counts["App"] != result.Summary.Applications || counts["Resource"] != result.Summary.Resources {
		return nil, fmt.Errorf("Ops live inventory differs")
	}
	for _, row := range result.Servers {
		if live[row.Key] != "Server" {
			return nil, fmt.Errorf("Ops server binding differs")
		}
	}
	for _, row := range result.Applications {
		if live[row.Key] != "App" {
			return nil, fmt.Errorf("Ops application binding differs")
		}
	}
	for _, row := range result.Resources {
		if live[row.Key] != "Resource" {
			return nil, fmt.Errorf("Ops resource binding differs")
		}
	}
	result.Enabled = true
	result.EvaluatedAt = w.now().UTC().Format(time.RFC3339)
	result.AuthorityFiles, result.LedgerFiles = nil, nil
	result.ReadCapabilities = w.opsReadCapabilities(&result)
	result.ActionCapabilities = w.opsActionCapabilities(&result)
	result.Refresh = w.opsRefreshState()
	result.Recoveries = w.opsRecoveryStates()
	result.Operations = opsOperationCatalog(&result)
	return result, nil
}

func validateOps(o *opsOverview, now time.Time) error {
	if o.Servers == nil || o.Applications == nil || o.Deployments == nil || o.Resources == nil || o.Monitors == nil || o.CollectionErrors == nil {
		return fmt.Errorf("Ops collections must be arrays")
	}
	if o.Summary.Servers != len(o.Servers) || o.Summary.Applications != len(o.Applications) || o.Summary.Deployments != len(o.Deployments) || o.Summary.Resources != len(o.Resources) || len(o.Servers) > 4096 || len(o.Deployments) > 8192 {
		return fmt.Errorf("Ops denominator differs")
	}
	keys := map[string]string{}
	add := func(key, kind string) error {
		if !strings.HasPrefix(key, kind+"/") || keys[key] != "" {
			return fmt.Errorf("invalid Ops identity")
		}
		keys[key] = kind
		return nil
	}
	for i := range o.Servers {
		s := &o.Servers[i]
		if s.Hardware == nil || s.Applications == nil || s.Disks == nil || add(s.Key, "Server") != nil || opsAge(&s.Snapshot, now) != nil {
			return fmt.Errorf("invalid server evidence")
		}
		for k := range s.Hardware {
			if k != "cpu" && k != "memory" && k != "disk" && k != "os" {
				return fmt.Errorf("unexpected hardware field")
			}
		}
	}
	for _, a := range o.Applications {
		if a.Deployments == nil {
			return fmt.Errorf("application deployments must be an array")
		}
		if err := add(a.Key, "App"); err != nil {
			return err
		}
	}
	for _, r := range o.Resources {
		if r.Hosts == nil {
			return fmt.Errorf("resource hosts must be an array")
		}
		if err := add(r.Key, "Resource"); err != nil {
			return err
		}
	}
	deployments := map[string]string{}
	criterionIDs := map[string]bool{}
	criterionCount := 0
	for i := range o.Deployments {
		d := &o.Deployments[i]
		if d.Resources == nil || d.Checks == nil || d.ID == "" || deployments[d.ID] != "" || keys[d.Application] != "App" || keys[d.Host] != "Server" {
			return fmt.Errorf("invalid deployment relation")
		}
		deployments[d.ID] = d.Application
		for _, k := range d.Resources {
			if keys[k] != "Resource" {
				return fmt.Errorf("missing resource relation")
			}
		}
		for _, e := range []*opsEvidence{&d.Runtime, &d.Readiness, &d.Health} {
			if err := opsAge(e, now); err != nil {
				return err
			}
		}
		// Missing details in older packages remain absent, not inferred passing.
		if len(d.Criteria) > 0 && len(d.Criteria) != len(d.Checks) {
			return fmt.Errorf("Ops criteria mapping differs")
		}
		checks := map[string]bool{}
		for _, id := range d.Checks {
			if !opsCriterionID.MatchString(id) || checks[id] {
				return fmt.Errorf("invalid configured check identity")
			}
			checks[id] = true
		}
		for j := range d.Criteria {
			c := &d.Criteria[j]
			criterionCount++
			if criterionCount > 8192 || !checks[c.ID] || criterionIDs[c.ID] || len(c.Component) == 0 || len(c.Component) > 2000 || len(c.Selector) > 2000 || keys[c.Observer] != "Server" {
				return fmt.Errorf("invalid criterion relation")
			}
			criterionIDs[c.ID] = true
			layer, ok := map[string]string{
				"fleet_systemd": "runtime", "fleet_container_running": "runtime", "process_running": "runtime", "fleet_container_health": "readiness", "http_from_container": "network_path", "public_https": "network_path", "es_cluster_health": "readiness", "systemd_timer": "schedule", "backup_meta": "backup_completion", "backup_space": "capacity", "kuma_heartbeat": "progress", "nas_snapshot": "progress", "public_observer_log": "progress", "tls_certificate": "protection", "manual": "unverified",
			}[c.Kind]
			if !ok || c.Layer != layer {
				return fmt.Errorf("invalid criterion layer")
			}
			switch c.WorkloadMode {
			case "service", "worker", "scheduled", "control":
			default:
				return fmt.Errorf("invalid criterion workload")
			}
			if c.DataMode != "stateful" && c.DataMode != "stateless" {
				return fmt.Errorf("invalid criterion data mode")
			}
			if strings.HasPrefix(c.Kind, "fleet_") {
				if c.Evidence.Source != "fleet" {
					return fmt.Errorf("invalid saved criterion source")
				}
			} else if c.Evidence.Source != "app-policy" || c.Evidence.Status != "UNKNOWN" || c.Evidence.ObservedAt != nil {
				return fmt.Errorf("policy alone cannot supply an observation")
			}
			if err := opsAge(&c.Evidence, now); err != nil {
				return err
			}
		}
	}
	for _, a := range o.Applications {
		for _, id := range a.Deployments {
			if deployments[id] != a.Key {
				return fmt.Errorf("invalid application deployment")
			}
		}
	}
	if err := validateOpsProduction(o, now); err != nil {
		return err
	}
	for _, s := range o.Servers {
		for _, key := range s.Applications {
			if keys[key] != "App" {
				return fmt.Errorf("invalid server application")
			}
		}
	}
	for _, r := range o.Resources {
		for _, key := range r.Hosts {
			if keys[key] != "Server" {
				return fmt.Errorf("invalid resource host")
			}
		}
	}
	monitors := map[int]bool{}
	for i := range o.Monitors {
		m := &o.Monitors[i]
		if m.Targets == nil || m.ID <= 0 || monitors[m.ID] {
			return fmt.Errorf("invalid monitor identity")
		}
		monitors[m.ID] = true
		for _, key := range m.Targets {
			if keys[key] == "" {
				return fmt.Errorf("unknown monitor target")
			}
		}
		if opsAge(&m.State, now) != nil || opsAge(&m.Configuration, now) != nil {
			return fmt.Errorf("invalid monitor evidence")
		}
		if m.State.Status != "PAUSED" && m.Configuration.Status != "PASS" {
			m.State.Status = "UNKNOWN"
			m.State.Reason = "monitor_configuration_not_fresh"
			if m.Configuration.Status == "STALE" {
				m.State.Status = "STALE"
				m.State.Reason = "monitor_configuration_stale"
			}
		}
	}
	return nil
}
