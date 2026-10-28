package observation

import "testing"

func TestIdentityNormalization(t *testing.T) {
	for _, pair := range [][2]Identity{
		{{Kind: "Domain", Name: "EXAMPLE.COM."}, {Kind: "Domain", Name: "example.com"}},
		{{Kind: "Server", Address: "::ffff:192.0.2.10"}, {Kind: "Server", Address: "192.0.2.10"}},
		{{Kind: "Server", Address: "HOST.EXAMPLE.COM."}, {Kind: "Server", Address: "host.example.com"}},
		{{Kind: "Resource", Provider: "baidu", Type: "cdn", Endpoint: "*.EXAMPLE.COM."}, {Kind: "Resource", Provider: "baidu", Type: "cdn", Endpoint: "*.example.com"}},
		{{Kind: "Resource", Provider: "aliyun", Type: "cdn", Endpoint: ".EXAMPLE.COM."}, {Kind: "Resource", Provider: "aliyun", Type: "cdn", Endpoint: "*.example.com"}},
	} {
		if got := pair[0].Normalized(); got != pair[1] {
			t.Fatalf("%+v != %+v", got, pair[1])
		}
	}
	id := IdentityOf("Resource", "ignored", map[string]any{"provider": "aliyun", "resource_id": "CaseSensitive", "type": "cdn", "endpoint": "example.com"})
	if id != (Identity{Kind: "Resource", Provider: "aliyun", ResourceID: "CaseSensitive"}) {
		t.Fatal(id)
	}
}

func TestRequirementsHaveAssetMappings(t *testing.T) {
	fixtures := map[string]map[string]any{
		"aliyun": {"domains": []any{}, "cdn": []any{}}, "tencent": {"domains": []any{}, "cdn": []any{}}, "baidu": {"domains": []any{}, "cdn": []any{}}, "ctyun": {"cdn": []any{}},
		"cloudflare": {"zones": []any{}}, "whois": {"found": true, "domain": "example.com"}, "ssh": {"host": "192.0.2.10", "system": "Linux", "release": "example"},
	}
	for _, req := range Requirements() {
		spec, ok := fixtures[req.Plugin]
		if !ok {
			t.Fatalf("unknown source: %+v", req)
		}
		for _, service := range req.Services {
			if _, ok := spec[service]; !ok {
				t.Fatalf("unknown service: %+v", req)
			}
		}
		if _, _, err := Map(req.Plugin, "home", "fixture", spec); err != nil {
			t.Fatal(err)
		}
	}
}
