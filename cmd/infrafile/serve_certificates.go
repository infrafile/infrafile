package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/snapshot"
)

type certificateEntry struct {
	Host         string   `json:"host"`
	Port         int      `json:"port"`
	Apps         []string `json:"apps"`
	NotAfter     *string  `json:"not_after"`
	DaysLeft     *int     `json:"days_left"`
	Verified     *bool    `json:"verified"`
	Verification *string  `json:"verification"`
	ObservedAt   *string  `json:"observed_at"`
	Stale        bool     `json:"stale"`
	DataState    string   `json:"data_state"`
	ExpiryState  string   `json:"expiry_state"`
}

type certificateGroup struct {
	App          string             `json:"app"`
	Ownership    string             `json:"ownership"`
	Certificates []certificateEntry `json:"certificates"`
}

func certificateHost(host string) string { return strings.TrimSuffix(strings.ToLower(host), ".") }

func workbenchCertificates(ctx context.Context, dir string, age *int, now time.Time) (any, error) {
	pinned, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	repo, err := loadQueryRepo(ctx, pinned)
	if err != nil {
		return nil, err
	}
	maxAge := 7
	if age != nil {
		maxAge = *age
	}
	if maxAge < 0 {
		return nil, fmt.Errorf("invalid certificate TTL")
	}
	bindings := map[string]map[string]bool{}
	for _, r := range repo.Records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if r.Kind != "Domain" || r.Archived() {
			continue
		}
		spec := r.Value.(map[string]any)["spec"].(map[string]any)
		items, _ := spec["bindings"].([]any)
		for _, raw := range items {
			binding := raw.(map[string]any)
			host := certificateHost(r.Name)
			if binding["name"] != "@" {
				host = certificateHost(binding["name"].(string) + "." + host)
			}
			if !validCertificateHost(strings.TrimPrefix(host, "*.")) {
				return nil, fmt.Errorf("invalid binding host")
			}
			if bindings[host] == nil {
				bindings[host] = map[string]bool{}
			}
			app, _ := binding["app"].(string)
			if app != "" {
				bindings[host]["App/"+app] = true
			}
		}
	}
	plugins, _ := repo.Config.Value.(map[string]any)["plugins"].(map[string]any)
	tls, _ := plugins["tls"].(map[string]any)
	configs, _ := tls["instances"].(map[string]any)
	snaps, err := snapshot.Read(ctx, pinned, func(p, instance string) bool { return p != "tls" || configs[instance] == nil })
	if err != nil {
		return nil, err
	}
	stamps, err := snapshot.ReadSync(pinned)
	if err != nil {
		return nil, err
	}
	status, err := snapshot.ReadStatus(ctx, pinned)
	if err != nil {
		return nil, err
	}
	byInstance := map[string]snapshot.Snapshot{}
	for _, s := range snaps {
		if !s.Skipped {
			byInstance[s.Instance] = s
		}
	}
	groups := map[string]*certificateGroup{}
	uncollected := []certificateEntry{}
	configuredHosts := map[string]bool{}
	add := func(row certificateEntry) {
		apps := []string{}
		for app := range bindings[row.Host] {
			apps = append(apps, app)
		}
		slices.Sort(apps)
		row.Apps = apps
		if row.DataState == "uncollected" {
			uncollected = append(uncollected, row)
			return
		}
		ownership, app := "unassociated", ""
		if net.ParseIP(row.Host) == nil {
			switch {
			case len(apps) > 1:
				ownership = "ambiguous"
			case len(apps) == 1:
				ownership, app = "app", apps[0]
			case bindings[row.Host] != nil:
				ownership = "no_app"
			}
		}
		key := ownership + "/" + app
		if groups[key] == nil {
			groups[key] = &certificateGroup{App: app, Ownership: ownership, Certificates: []certificateEntry{}}
		}
		groups[key].Certificates = append(groups[key].Certificates, row)
	}
	for _, name := range slices.Sorted(maps.Keys(configs)) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cfg := configs[name].(map[string]any)
		raw, _ := json.Marshal(cfg)
		var target struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&target) != nil {
			return nil, fmt.Errorf("invalid TLS configuration")
		}
		host := certificateHost(target.Host)
		if !validCertificateHost(host) {
			return nil, fmt.Errorf("invalid TLS host")
		}
		if target.Port == 0 {
			target.Port = 443
		}
		if target.Port < 1 || target.Port > 65535 {
			return nil, fmt.Errorf("invalid TLS port")
		}
		configuredHosts[host] = true
		row := certificateEntry{Host: host, Port: target.Port, DataState: "uncollected", ExpiryState: "unknown"}
		s, present := byInstance[name]
		if !present {
			add(row)
			continue
		}
		// Decode only the display allowlist; snapshot-only fields never reach the Web response.
		raw, _ = json.Marshal(s.Spec)
		var saved struct {
			Host         string `json:"host"`
			Port         int    `json:"port"`
			Verified     *bool  `json:"verified"`
			Verification string `json:"verification"`
			Certificate  struct {
				NotAfter string `json:"not_after"`
			} `json:"certificate"`
		}
		if json.Unmarshal(raw, &saved) != nil || saved.Verified == nil || !slices.Contains([]string{"valid", "untrusted", "hostname-mismatch", "expired-or-not-yet-valid"}, saved.Verification) || (*saved.Verified != (saved.Verification == "valid")) {
			return nil, fmt.Errorf("invalid TLS observation")
		}
		if certificateHost(saved.Host) != host || saved.Port != target.Port {
			add(row)
			continue
		}
		until, err := time.Parse(time.RFC3339, saved.Certificate.NotAfter)
		if err != nil {
			return nil, fmt.Errorf("invalid TLS expiry")
		}
		row.NotAfter = &saved.Certificate.NotAfter
		row.Verified, row.Verification = saved.Verified, &saved.Verification
		stamp, hasStamp := stamps["tls"][name]
		if hasStamp {
			at := stamp.UTC().Format(time.RFC3339)
			row.ObservedAt = &at
			row.Stale = now.Sub(stamp).Hours()/24 > float64(maxAge)
		}
		row.DataState = "unverified"
		if status != nil {
			a := status.Spec["tls"][name]
			at, _ := time.Parse(time.RFC3339, a.AttemptedAt)
			if a.ConfigHash == plugin.Hash(cfg) && !stamp.After(at) {
				switch {
				case at.After(now):
					row.DataState = "future"
				case a.Status == "failed":
					row.DataState = "failed"
				case a.Status == "running":
					row.DataState = "running"
				case hasStamp && stamp.Equal(at) && a.Status == "succeeded" && a.SnapshotHash == s.Hash:
					row.DataState = "fresh"
					if row.Stale {
						row.DataState = "stale"
					}
				}
			}
		}
		if row.DataState == "fresh" {
			days := int(math.Floor(until.Sub(now).Hours() / 24))
			row.DaysLeft = &days
			switch {
			case until.Before(now):
				row.ExpiryState = "expired"
			case days == 0:
				row.ExpiryState = "today"
			case days <= 30:
				row.ExpiryState = "expiring"
			default:
				row.ExpiryState = "not_due"
			}
		}
		add(row)
	}
	for host, apps := range bindings {
		if !configuredHosts[host] && len(apps) > 0 {
			add(certificateEntry{Host: host, Port: 443, DataState: "uncollected", ExpiryState: "unknown"})
		}
	}
	ordered := []certificateGroup{}
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		ordered = append(ordered, *groups[key])
	}
	slices.SortFunc(uncollected, func(a, b certificateEntry) int {
		if n := strings.Compare(a.Host, b.Host); n != 0 {
			return n
		}
		return a.Port - b.Port
	})
	message := ""
	if len(byInstance) == 0 {
		message = "没有 tls 探测快照 · 不代表证书正常"
	}
	return queryValue(map[string]any{"version": jsonVersion, "as_of": now.UTC().Format(time.RFC3339), "max_age_days": maxAge, "has_snapshots": len(byInstance) > 0, "message": message, "groups": ordered, "uncollected": uncollected})
}

func validCertificateHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}
