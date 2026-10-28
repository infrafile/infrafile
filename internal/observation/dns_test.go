package observation

import "testing"

func TestAddressRecordsRejectMalformed(t *testing.T) {
	for _, tc := range []struct {
		name, plugin string
		spec         map[string]any
	}{
		{"no_host", "dns", map[string]any{"addresses": []any{}}},
		{"wrong_addresses", "dns", map[string]any{"host": "example.com", "addresses": "192.0.2.10"}},
		{"invalid_address", "dns", map[string]any{"host": "example.com", "addresses": []any{"invalid"}}},
		{"missing_zones", "cloudflare", map[string]any{}},
		{"missing_records", "cloudflare", map[string]any{"zones": []any{map[string]any{"name": "example.com"}}}},
		{"wrong_proxy", "cloudflare", map[string]any{"zones": []any{map[string]any{"dns_records": []any{map[string]any{"name": "example.com", "type": "A", "content": "192.0.2.10", "proxied": "false"}}}}}},
		{"wrong_family", "cloudflare", map[string]any{"zones": []any{map[string]any{"dns_records": []any{map[string]any{"name": "example.com", "type": "A", "content": "2001:db8::1", "proxied": false}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := AddressRecords(tc.plugin, tc.spec); err == nil {
				t.Fatal("malformed observation accepted")
			}
		})
	}
}
