package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type opsRecoveryBinding struct {
	Binding  string `json:"binding"`
	Overview string `json:"overview"`
	Actor    string `json:"actor"`
	Provider string `json:"provider"`
}
type opsRecoveryProvider struct {
	binding                opsRecoveryBinding
	provider               *opsReadProvider
	registry, registryHash string
}
type opsRecoveryState struct {
	ID       string `json:"id"`
	ActionID string `json:"action_id"`
	Target   string `json:"target"`
	State    string `json:"state"`
	Enabled  bool   `json:"enabled"`
	Reason   string `json:"reason,omitempty"`
}

func (p *opsRecoveryProvider) valid() bool {
	return opsMatchesHash(p.registry, p.registryHash, 64<<10) && opsMatchesHash(p.binding.Actor, p.binding.Binding, 64<<10) && p.provider.valid(p.binding.Overview)
}

func (w *workbench) loadOpsRecoveries(ctx context.Context, path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0600 || !outsideRepo(w.dir, path) || !outsideRepo(w.opsAuthority, path) {
		return fmt.Errorf("private recovery registry required")
	}
	raw, err := opsAbsoluteRead(path, 64<<10)
	if err != nil {
		return err
	}
	var registry struct {
		Version  string               `json:"version"`
		Bindings []opsRecoveryBinding `json:"bindings"`
	}
	if opsDecode(ctx, raw, &registry) != nil || registry.Version != "infrafile-ops-recoveries/v1" || len(registry.Bindings) > 32 {
		return fmt.Errorf("invalid recovery registry")
	}
	providers := map[string]*opsRecoveryProvider{}
	for _, binding := range registry.Bindings {
		if !opsDigest.MatchString(binding.Binding) || providers[binding.Binding] != nil || !opsMatchesHash(binding.Actor, binding.Binding, 64<<10) || filepath.Dir(binding.Actor) != filepath.Dir(binding.Overview) || filepath.Base(binding.Actor) != "actor.json" || filepath.Base(binding.Overview) != "ops.json" {
			return fmt.Errorf("invalid action origin")
		}
		originalRaw, err := opsAbsoluteRead(binding.Actor, 64<<10)
		if err != nil {
			return err
		}
		var original opsReadConfig
		if opsDecode(ctx, originalRaw, &original) != nil || original.Version != "infrafile-ops-actor/v1" || !opsMatchesHash(binding.Overview, original.OverviewSHA256, 2<<20) {
			return fmt.Errorf("invalid original actor")
		}
		pkg := filepath.Dir(binding.Overview)
		frozen := filepath.Join(pkg, "bundle", "source")
		authority, err := os.Lstat(frozen)
		if err != nil || !authority.IsDir() || authority.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("invalid frozen authority")
		}
		// A distinct read-only view validates the original ledger and authority.
		// No live source or mutex is copied or relabelled as the historical source.
		origin := &workbench{dir: filepath.Join(pkg, "bundle", "candidate"), opsFile: binding.Overview, opsAuthority: frozen, opsAuthorityInfo: authority, now: w.now}
		provider, err := origin.loadOpsProvider(ctx, binding.Provider, "infrafile-ops-reconciler/v1", 45)
		if err != nil {
			return fmt.Errorf("invalid status-only recovery provider: %w", err)
		}
		if !reflect.DeepEqual(provider.config.Capabilities, original.Capabilities) || provider.config.Files[binding.Actor] != binding.Binding || provider.config.Files[binding.Overview] != original.OverviewSHA256 {
			return fmt.Errorf("recovery scope differs")
		}
		// Every frozen input used by this status adapter must remain hash pinned.
		modelRaw, err := opsAbsoluteRead(binding.Overview, 2<<20)
		if err != nil {
			return err
		}
		var model opsOverview
		if opsDecode(ctx, modelRaw, &model) != nil {
			return fmt.Errorf("invalid original view")
		}
		for name, hash := range model.AuthorityFiles {
			if provider.config.Files[filepath.Join(frozen, name)] != hash {
				return fmt.Errorf("unbound recovery source")
			}
		}
		hash := sha256.Sum256(raw)
		p := &opsRecoveryProvider{binding: binding, provider: provider, registry: path, registryHash: hex.EncodeToString(hash[:])}
		if !p.valid() {
			return fmt.Errorf("recovery program changed")
		}
		providers[binding.Binding] = p
	}
	w.opsRecoveries = providers
	return nil
}

func (w *workbench) recoveryActor(record opsActionRecord) (*opsReadProvider, string) {
	if w.opsActor != nil && record.Binding == w.opsActor.digest && w.opsActor.valid(w.opsFile) {
		return w.opsActor, w.opsFile
	}
	p := w.opsRecoveries[record.Binding]
	if p == nil || !p.valid() {
		return nil, ""
	}
	for _, cap := range p.provider.config.Capabilities {
		if cap.ID == record.ActionID && cap.Target == record.Target {
			return p.provider, p.binding.Overview
		}
	}
	return nil, ""
}

func (w *workbench) opsRecoveryStates() []opsRecoveryState {
	if w.opsActionDir == "" {
		return nil
	}
	records, err := w.actionRecords()
	if err != nil {
		return []opsRecoveryState{{State: "uncertain", Reason: "receipt_unavailable"}}
	}
	var result []opsRecoveryState
	for _, r := range records {
		if !actionUnresolved(r.State) {
			continue
		}
		if !opsReadID.MatchString(r.ActionID) || !strings.HasPrefix(r.Target, "Server/") || !opsReadID.MatchString(strings.TrimPrefix(r.Target, "Server/")) {
			return []opsRecoveryState{{State: "uncertain", Reason: "receipt_unavailable"}}
		}
		provider, _ := w.recoveryActor(r)
		state := opsRecoveryState{ID: r.ID, ActionID: r.ActionID, Target: r.Target, State: r.State, Enabled: provider != nil}
		if provider == nil {
			state.Reason = "recovery_unavailable"
		}
		result = append(result, state)
	}
	return result
}

func (w *workbench) actionRecordAllowed(record opsActionRecord) bool {
	if w.opsActor != nil {
		for _, cap := range w.opsActor.config.Capabilities {
			if cap.ID == record.ActionID && cap.Target == record.Target {
				return true
			}
		}
	}
	if p := w.opsRecoveries[record.Binding]; p != nil {
		for _, cap := range p.provider.config.Capabilities {
			if cap.ID == record.ActionID && cap.Target == record.Target {
				return true
			}
		}
	}
	return false
}
