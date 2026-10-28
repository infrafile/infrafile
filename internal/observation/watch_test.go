package observation

import "testing"

func TestWatchIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, plugin, service string
		target                Identity
		endpoint              Identity
		observed              map[string]any
		seen                  bool
	}{
		{"domain-normalization", "aliyun", "domains", Identity{Kind: "Domain", Name: "example.com"}, Identity{}, map[string]any{"name": "EXAMPLE.COM."}, true},
		{"cf-domain", "cloudflare", "zones", Identity{Kind: "Domain", Name: "example.com"}, Identity{}, map[string]any{"name": "example.com", "id": "zone"}, true},
		{"id", "cloudflare", "zones", Identity{Kind: "Resource", Provider: "cloudflare", ResourceID: "zone"}, Identity{}, map[string]any{"name": "example.com", "id": "zone"}, true},
		{"wrong-id", "cloudflare", "zones", Identity{Kind: "Resource", Provider: "cloudflare", ResourceID: "old-zone"}, Identity{Kind: "Resource", Provider: "cloudflare", Type: "dns-zone", Endpoint: "example.com"}, map[string]any{"name": "example.com", "id": "new-zone"}, false},
		{"no-observed-id", "aliyun", "cdn", Identity{Kind: "Resource", Provider: "aliyun", ResourceID: "edge"}, Identity{Kind: "Resource", Provider: "aliyun", Type: "cdn", Endpoint: "*.example.com"}, map[string]any{"name": ".EXAMPLE.COM."}, true},
		{"no-target-id", "cloudflare", "zones", Identity{Kind: "Resource", Provider: "cloudflare", Type: "dns-zone", Endpoint: "example.com"}, Identity{Kind: "Resource", Provider: "cloudflare", Type: "dns-zone", Endpoint: "example.com"}, map[string]any{"name": "example.com", "id": "zone"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records, complete, err := WatchRecords(tc.plugin, "home", tc.service, map[string]any{tc.service: []any{tc.observed}})
			if err != nil || !complete {
				t.Fatalf("mapping: %v %v", complete, err)
			}
			w := Watch{Plugin: tc.plugin, Identity: tc.target, Endpoint: tc.endpoint}
			if w.Seen(records) != tc.seen {
				t.Fatalf("seen should be %v", tc.seen)
			}
		})
	}
}
