package observation

import "testing"

func TestAliyunCDNLeadingDot(t *testing.T) {
	for _, tc := range []struct {
		plugin, service, name, want string
	}{
		{"aliyun", "cdn", ".EXAMPLE.COM.", "*.example.com"},
		{"aliyun", "cdn", "*.example.com", "*.example.com"},
		{"aliyun", "cdn", "www.example.com", "www.example.com"},
		{"aliyun", "cdn", "..example.com", ""},
		{"aliyun", "cdn", ".", ""},
		{"aliyun", "domains", ".example.com", ""},
		{"tencent", "cdn", ".example.com", ""},
	} {
		t.Run(tc.plugin+"/"+tc.service+"/"+tc.name, func(t *testing.T) {
			got, _, err := Map(tc.plugin, "main", "fixture", map[string]any{tc.service: []any{map[string]any{"name": tc.name, "id": "1234567"}}})
			if tc.want == "" {
				if err == nil {
					t.Fatal("invalid name accepted")
				}
				return
			}
			if err != nil || len(got) != 1 || got[0].Spec["endpoint"] != tc.want || got[0].Spec["resource_id"] != "1234567" {
				t.Fatalf("got %+v, err %v", got, err)
			}
		})
	}
}
