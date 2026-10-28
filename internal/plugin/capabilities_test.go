package plugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/protocol"
)

func TestCapabilityCatalogMatchesHandshakeAndObservation(t *testing.T) {
	catalog := plugin.Capabilities()
	if len(catalog.Kinds) != 6 || len(catalog.Plugins) != 9 || catalog.ExternalDetails != plugin.Unknown {
		t.Fatal(catalog)
	}
	for _, p := range catalog.Plugins {
		t.Run(p.Plugin, func(t *testing.T) {
			client, err := plugin.Open(context.Background(), p.Plugin, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			raw, err := client.Call(context.Background(), "describe", map[string]any{})
			if err != nil || protocol.Validate("describe", raw) != nil {
				t.Fatalf("legacy v1 handshake: %s %v", raw, err)
			}
			var desc protocol.Description
			if json.Unmarshal(raw, &desc) != nil || !reflect.DeepEqual(desc.Capabilities, p.Methods) {
				t.Fatalf("methods differ: %s", raw)
			}
			for _, requirement := range observation.Requirements() {
				if requirement.Plugin != p.Plugin {
					continue
				}
				for _, service := range requirement.Services {
					found := false
					for _, entry := range p.Services {
						found = found || (entry.Service == service && entry.Stages.Observe != plugin.Unsupported)
					}
					if !found {
						t.Fatalf("missing observation mapping %s", service)
					}
				}
			}
			for _, service := range p.Services {
				if service.Verification != "automated-only" && service.Verification != "unverified" {
					t.Fatalf("overstates live acceptance: %+v", service)
				}
				if service.Action != "" && (p.Plugin != "cloudflare" || !slices.Contains([]string{"cdn-pause", "cdn-resume", "cdn-origin"}, service.Action)) {
					t.Fatalf("unexpected cloud write: %+v", service)
				}
				if p.Plugin == "tls" && (service.Stages.Register != plugin.Unsupported || service.Stages.Compare != plugin.Unsupported) {
					t.Fatal("TLS observation is not certificate inventory/audit")
				}
				if p.Plugin == "baidu" && service.Service == "cdn" && !reflect.DeepEqual(service.Fields, []string{"name"}) {
					t.Fatal("Baidu CDN list only observes names")
				}
				if p.Plugin == "cloudflare" && service.Service == "cdn-origin" && slices.Contains(service.Fields, "protocol") {
					t.Fatal("Cloudflare origin protocol is not observable")
				}
				if p.Plugin == "ctyun" && service.Service == "domains" && service.Stages.Observe != plugin.Unsupported {
					t.Fatal("Ctyun registered domain list is unsupported")
				}
			}
		})
	}
}

func TestUnsupportedCapabilitiesRejectBeforeCredentialsAndNetwork(t *testing.T) {
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unsupported operation contacted network")
		return nil, errors.New("network denied")
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	for _, tc := range []struct {
		name, method string
		params       any
	}{
		{"tls", "control", protocol.ControlParams{}},
		{"ctyun", "sync", protocol.SyncParams{Config: map[string]any{"services": []string{"domains"}}}},
		{"cloudflare", "control", protocol.ControlParams{Target: protocol.Target{ZoneID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Action: "delete-zone", Config: map[string]any{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := plugin.Open(context.Background(), tc.name, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, err = plugin.Invoke(context.Background(), client, tc.method, tc.params)
			var failure *plugin.Error
			if !errors.As(err, &failure) || (failure.Code != "config" && failure.Code != "capability") {
				t.Fatalf("did not reject before credential lookup: %v", err)
			}
		})
	}
}
