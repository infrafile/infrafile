package plugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/protocol"
)

func originReply(t *testing.T, name string, r *http.Request) (string, bool) {
	t.Helper()
	file := ""
	switch name {
	case "aliyun":
		switch r.Header.Get("X-Acs-Action") {
		case "DescribeUserDomains":
			return `{"TotalCount":1,"Domains":{"PageData":[{"DomainName":"static.example.com","DomainStatus":"online"}]}}`, false
		case "DescribeCdnDomainDetail":
			file = "aliyun-origin.json"
		case "DescribeCdnDomainConfigs":
			file = "aliyun-origin-config.json"
		}
		if r.URL.Query().Get("DomainName") != "static.example.com" {
			t.Fatal("wrong detail target")
		}
	case "tencent":
		if r.Header.Get("X-Tc-Action") != "DescribeDomainsConfig" {
			t.Fatal("origin collection used list API")
		}
		file = "tencent-origin.json"
	case "baidu":
		if r.URL.Path == "/v2/domain" {
			return `{"isTruncated":false,"domains":[{"name":"static.example.com"}]}`, false
		}
		if r.URL.Path != "/v2/domain/static.example.com/config" {
			t.Fatal("wrong detail target")
		}
		file = "baidu-origin-config.json"
		if r.URL.Query().Has("originConfig") {
			file = "baidu-origin.json"
		}
	case "ctyun":
		if r.URL.Path == "/domain/query-domain-list" {
			if r.URL.Query().Get("area_scope") != "1" {
				return `{"code":100000,"total":0,"result":[]}`, false
			}
			return `{"code":100000,"total":1,"result":[{"domain":"static.example.com","product_code":"001","status":4,"area_scope":1}]}`, false
		}
		if r.URL.Path != "/domain/query-domain-detail" || r.URL.Query().Get("domain") != "static.example.com" || r.URL.Query().Get("product_code") != "001" {
			t.Fatal("wrong detail target")
		}
		file = "ctyun-origin.json"
	case "cloudflare":
		if r.URL.Path == "/client/v4/zones" {
			return `{"success":true,"result":[{"id":"zone","name":"example.com"}],"result_info":{"page":1,"total_count":1}}`, false
		}
		if strings.HasSuffix(r.URL.Path, "/dns_records") {
			return `{"success":true,"result":[],"result_info":{"page":1,"total_count":0}}`, false
		}
		if r.URL.Path != "/client/v4/zones/zone/rulesets/phases/http_request_origin/entrypoint" {
			t.Fatal("wrong ruleset target")
		}
		file = "cloudflare-origin.json"
	}
	data, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

func originConfig(t *testing.T, name string) map[string]any {
	t.Helper()
	t.Setenv("TEST_AK", "synthetic-access-id")
	t.Setenv("TEST_SK", "synthetic-access-secret")
	if name == "cloudflare" {
		return map[string]any{"api_token": "env://TEST_SK", "collect_origin": true}
	}
	return map[string]any{"access_key_id": "env://TEST_AK", "access_key_secret": "env://TEST_SK", "services": []string{"cdn"}, "collect_origin": true}
}

func TestOriginRecordedCollection(t *testing.T) {
	for _, name := range []string{"aliyun", "tencent", "baidu", "ctyun", "cloudflare"} {
		t.Run(name, func(t *testing.T) {
			config := originConfig(t, name)
			old := http.DefaultTransport
			details := 0
			http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, detail := originReply(t, name, r)
				if detail {
					details++
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = old })
			data := syncBuiltin(t, name, config)
			if details == 0 || strings.Contains(string(data), "synthetic-do-not-retain") || strings.Contains(string(data), "host-header") || strings.Contains(string(data), "sni.example") {
				t.Fatalf("unsafe origin projection: %s", data)
			}
			var result map[string]json.RawMessage
			var entries []struct {
				Origin observation.Origin `json:"origin"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			service := "cdn"
			want := 2
			if name == "cloudflare" {
				service, want = "zones", 1
			}
			if err := json.Unmarshal(result[service], &entries); err != nil {
				t.Fatal(err)
			}
			o := entries[0].Origin
			if o.Scope != "global" || len(o.Sources) != want {
				t.Fatalf("origin: %+v", o)
			}
			for _, source := range o.Sources {
				if source.Port != 8443 || source.Host != "192.0.2.1" && source.Host != "origin.example.com" {
					t.Fatal(source)
				}
				if name == "cloudflare" && source.Protocol != "" || name != "cloudflare" && source.Protocol != "https" {
					t.Fatal(source)
				}
			}
		})
	}
}

func TestOriginUnknownScopesAndMissingFields(t *testing.T) {
	for _, tc := range []struct{ name, from, to, scope string }{
		{"aliyun", `"Status": "success", "FunctionName": "ali_origin_port_scheme"`, `"Status":"success","ParentId":"42","FunctionName":"ali_origin_port_scheme"`, "conditional"},
		{"aliyun", `"SourceModel": [`, `"SourceModelIgnored": [`, "unknown"},
		{"tencent", `"Origins": [`, `"PathRules":[{}],"Origins": [`, "conditional"},
		{"tencent", `"RuleEngine":`, `"SpecificConfig":{"Overseas":{"Origin":{"Origins":["overseas.example.net"]}}},"RuleEngine":`, "conditional"},
		{"tencent", `"Switch": "off"`, `"Switch":"on"`, "conditional"},
		{"tencent", `"ORIGIN.EXAMPLE.COM.:8443:10"`, `"https://user:synthetic-do-not-retain@origin.example.com"`, "unknown"},
		{"baidu", `"pageRules_originConfig": []`, `"pageRules_originConfig":[{"config":{"originConfig":[{"addr":"regional.example.net"}]}}]`, "conditional"},
		{"baidu", `"pageRules_originConfig": []`, `"pageRules_origins":[{}]`, "conditional"},
		{"baidu", `"type": "DOMAIN"`, `"type":"DOMAIN","isp":"ct"`, "conditional"},
		{"ctyun", `"xos_origin_is": 0`, `"xos_origin_is":1`, "unknown"},
		{"ctyun", `"area_scope": 1`, `"area_scope":2`, "unknown"},
		{"cloudflare", `"expression": "true"`, `"expression":"http.host eq \"example.com\""`, "conditional"},
		{"cloudflare", `"host": "ORIGIN.EXAMPLE.COM."`, `"ignored_host":"ORIGIN.EXAMPLE.COM."`, "unknown"},
	} {
		t.Run(tc.name+"/"+tc.from, func(t *testing.T) {
			config := originConfig(t, tc.name)
			old := http.DefaultTransport
			changed := false
			http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, detail := originReply(t, tc.name, r)
				if detail && strings.Contains(body, tc.from) {
					body = strings.ReplaceAll(body, tc.from, tc.to)
					changed = true
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = old })
			data := syncBuiltin(t, tc.name, config)
			if !changed || !strings.Contains(string(data), `"scope":"`+tc.scope+`"`) || strings.Contains(string(data), "synthetic-do-not-retain") {
				t.Fatalf("scope: %s", data)
			}
		})
	}
}

func TestOriginDetailFailureIsNotPartialSuccess(t *testing.T) {
	for _, name := range []string{"aliyun", "tencent", "baidu", "ctyun", "cloudflare"} {
		for _, mode := range []string{"forbidden", "timeout", "malformed", "oversize"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				config := originConfig(t, name)
				old := http.DefaultTransport
				http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					body, detail := originReply(t, name, r)
					status := 200
					if detail {
						switch mode {
						case "forbidden":
							status, body = 403, "synthetic-private-error"
						case "timeout":
							<-r.Context().Done()
							return nil, r.Context().Err()
						case "malformed":
							body = "{synthetic-private-error"
						case "oversize":
							body = strings.Repeat(" ", 4*1024*1024+1)
						}
					}
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})
				t.Cleanup(func() { http.DefaultTransport = old })
				client, err := plugin.Open(context.Background(), name, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				data, err := plugin.Sync(ctx, client, protocol.SyncParams{Instance: "home", Config: config})
				var failure *plugin.Error
				if !errors.As(err, &failure) || data != nil || strings.Contains(err.Error(), "synthetic-private-error") {
					t.Fatalf("partial/unsafe success: %s %v", data, err)
				}
			})
		}
	}
}
