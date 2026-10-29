package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/protocol"
)

func scopeRecord(kind, name string, spec map[string]any) *ledger.Record {
	return &ledger.Record{Document: &ledger.Document{Value: map[string]any{"kind": kind, "metadata": map[string]any{"name": name}, "spec": spec}}, Kind: kind, Name: name}
}
func scopeFixture() (*ledger.Repo, Action) {
	repo := &ledger.Repo{Records: []*ledger.Record{
		scopeRecord("Resource", "edge", map[string]any{"account": "cf", "provider": "cloudflare", "type": "cdn"}),
		scopeRecord("Account", "cf", map[string]any{"secrets": map[string]any{"api": "env://PRIVATE_SCOPE_REF"}}),
		scopeRecord("Domain", "example.test", map[string]any{}),
		scopeRecord("Resource", "hosted", map[string]any{"endpoint": "https://cdn.example.test:443/path"}),
		scopeRecord("Server", "login", map[string]any{"address": "ssh.example.test:22"}),
		scopeRecord("App", "consumer", map[string]any{"resources": []any{"edge"}}),
		scopeRecord("Resource", "other-account-resource", map[string]any{"account": "cf", "endpoint": "example.test.evil"}),
		scopeRecord("Server", "unrelated", map[string]any{"address": "192.0.2.10"}),
	}}
	a := Action{Target: "Resource/edge", Binding: Binding{Plugin: "cloudflare", Target: protocol.Target{ZoneID: "example-zone"}}, Operation: "cdn-pause", Before: map[string]any{"paused": false, "name": "EXAMPLE.TEST."}}
	return repo, a
}

func TestScopeShowsZoneAssociationsWithoutClaimingImpact(t *testing.T) {
	repo, a := scopeFixture()
	s, err := planScope(context.Background(), repo, a)
	if err != nil || s.Level != "zone" || s.ZoneName != "example.test" || len(s.Unknown) == 0 || s.validate(a) != nil {
		t.Fatal(s, err)
	}
	keys := []string{}
	for _, r := range s.Related {
		keys = append(keys, r.Key)
		if !digest.MatchString(r.RecordHash) {
			t.Fatal("missing registration binding")
		}
	}
	if got := strings.Join(keys, ","); got != "Account/cf,App/consumer,Domain/example.test,Resource/hosted,Server/login" {
		t.Fatal("scope leaked unrelated account peers or missed associations", got)
	}
	data, _ := json.Marshal(s)
	if strings.Contains(string(data), "PRIVATE_SCOPE_REF") || strings.Contains(string(data), "env://") {
		t.Fatal("scope copied secret references")
	}
	before := plugin.Hash(s)
	repo.Records[1].Value.(map[string]any)["metadata"].(map[string]any)["description"] = "changed account registration"
	next, err := planScope(context.Background(), repo, a)
	if err != nil || plugin.Hash(next) == before {
		t.Fatal("related metadata was not bound", err)
	}
	if plugin.Hash(s) == plugin.Hash(next) {
		t.Fatal("mutated returned scope")
	}
}

func TestScopeOriginRuleAndUnknownExternalPlugin(t *testing.T) {
	repo, a := scopeFixture()
	a.Operation = "cdn-origin"
	a.Binding.RulesetID = "ruleset"
	a.Binding.RuleID = "rule"
	a.Before["rules"] = []any{map[string]any{"id": "rule", "expression": "http.host eq \"cdn.example.test\""}}
	s, err := planScope(context.Background(), repo, a)
	if err != nil || s.Level != "origin_rule" || s.RuleExpression != "http.host eq \"cdn.example.test\"" || s.validate(a) != nil {
		t.Fatal(s, err)
	}
	s.Level = "zone"
	if s.validate(a) == nil {
		t.Fatal("rule scope broadened without detection")
	}
	a.Binding.Plugin = "external"
	s, err = planScope(context.Background(), repo, a)
	if err != nil || s.Level != "plugin_target" || s.ZoneName != "" || s.RuleExpression != "" {
		t.Fatal("assumed external plugin scope", s, err)
	}
	if !strings.Contains(strings.Join(s.Unknown, ","), "plugin_scope_unknown") {
		t.Fatal("external scope advertised as known")
	}
}

func TestScopeIsBoundedAndCancellationRejectsWholeResult(t *testing.T) {
	repo, a := scopeFixture()
	for i := 0; i < 257; i++ {
		repo.Records = append(repo.Records, scopeRecord("Domain", fmt.Sprintf("d%d.example.test", i), map[string]any{}))
	}
	if s, err := planScope(context.Background(), repo, a); err == nil || s != nil {
		t.Fatal("scope silently truncated")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s, err := planScope(ctx, repo, a); err == nil || s != nil {
		t.Fatal("ignored scope cancellation")
	}
}

func TestScopeReadValidationRejectsForgedAssociationsAndMissingUnknowns(t *testing.T) {
	for _, mode := range []string{"unknown", "kind", "hash", "via", "order"} {
		t.Run(mode, func(t *testing.T) {
			repo, a := scopeFixture()
			s, err := planScope(context.Background(), repo, a)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "unknown":
				s.Unknown = []string{}
			case "kind":
				s.Related[0].Key = "Unregistered/item"
			case "hash":
				s.Related[0].RecordHash = "invalid"
			case "via":
				s.Related[0].Via = []string{"execute"}
			case "order":
				s.Related[0], s.Related[1] = s.Related[1], s.Related[0]
			}
			if s.validate(a) == nil {
				t.Fatal("forged scope accepted", mode)
			}
		})
	}
}
