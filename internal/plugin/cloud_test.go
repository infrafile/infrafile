package plugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/testutil"
	"github.com/infrafile/infrafile/protocol"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func replayHTTP(t *testing.T, serve func(*http.Request) string) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		file := serve(r)
		if file == "" {
			return nil, fmt.Errorf("unexpected request")
		}
		body, err := os.ReadFile("testdata/" + file)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
}

func TestCloudFailuresDoNotBecomeEmptySuccess(t *testing.T) {
	t.Setenv("TEST_AK", "synthetic-access-id")
	t.Setenv("TEST_SK", "synthetic-access-secret")
	for _, provider := range []struct{ name, apiError, incomplete, emptyPage string }{
		{"cloudflare", `{"success":false,"errors":[{"message":"synthetic-private-error"}]}`, `{"success":true,"result":[]}`, `{"success":true,"result":[],"result_info":{"page":1,"total_count":1}}`},
		{"aliyun", `{"Code":"Forbidden","Message":"synthetic-private-error"}`, `{"NextPage":false,"Data":{"Domain":[]}}`, `{"NextPage":true,"TotalItemNum":1,"Data":{"Domain":[]}}`},
		{"tencent", `{"Response":{"Error":{"Code":"Forbidden","Message":"synthetic-private-error"}}}`, `{"Response":{"DomainSet":[]}}`, `{"Response":{"DomainSet":[],"TotalCount":1}}`},
		{"baidu", `{"code":"AccessDenied","message":"synthetic-private-error"}`, `{"result":[]}`, `{"totalCount":1,"result":[]}`},
		{"ctyun", `{"code":100001,"message":"synthetic-private-error"}`, `{"code":100000}`, `{"code":100000,"total_count":1,"page":1,"page_count":2,"result":[]}`},
	} {
		for _, tc := range []struct {
			name, body, code string
			status           int
		}{
			{"api", provider.apiError, "provider", 200}, {"incomplete", provider.incomplete, "protocol", 200}, {"empty-page", provider.emptyPage, "protocol", 200}, {"malformed", "{broken", "protocol", 200}, {"http403", "synthetic-private-error", "http", 403},
		} {
			t.Run(provider.name+"/"+tc.name, func(t *testing.T) {
				old := http.DefaultTransport
				http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
				})
				t.Cleanup(func() { http.DefaultTransport = old })
				config := map[string]any{"access_key_id": "env://TEST_AK", "access_key_secret": "env://TEST_SK"}
				if provider.name == "cloudflare" {
					config = map[string]any{"api_token": "env://TEST_SK"}
				}
				client, err := plugin.Open(context.Background(), provider.name, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				data, err := plugin.Sync(context.Background(), client, protocol.SyncParams{Instance: "test", Config: config})
				var failure *plugin.Error
				if !errors.As(err, &failure) || failure.Code != tc.code || data != nil || strings.Contains(err.Error(), "synthetic-private-error") {
					t.Fatalf("unsafe failure: %s %v", data, err)
				}
			})
		}
	}
}

func TestBuiltinSOPSReferenceAndKnownCredentialReflection(t *testing.T) {
	dir := t.TempDir()
	testutil.IsolateKeys(t)
	ref, value, key := testutil.EncryptedFile(t, dir)
	t.Setenv("SOPS_AGE_KEY", key)
	t.Setenv("TEST_AK", "synthetic-access-id")
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.Header.Get("Authorization"), value) {
			t.Error("signing secret appeared in Authorization")
		}
		// 域名不是凭据字段，仍不能把已知的认证原值写出。
		body := fmt.Sprintf(`{"NextPage":false,"TotalItemNum":1,"Data":{"Domain":[{"DomainName":%q}]}}`, value)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	client, err := plugin.Open(context.Background(), "aliyun", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	data, err := plugin.Sync(context.Background(), client, protocol.SyncParams{Instance: "test", Config: map[string]any{"access_key_id": "env://TEST_AK", "access_key_secret": ref, "services": []string{"domains"}}})
	var failure *plugin.Error
	if !errors.As(err, &failure) || failure.Code != "sensitive" || data != nil || strings.Contains(err.Error(), value) {
		t.Fatalf("credential reflection was not refused: %v", err)
	}
}

func TestCloudCancellation(t *testing.T) {
	t.Setenv("TEST_SK", "synthetic-access-secret")
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	t.Cleanup(func() { http.DefaultTransport = old })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	client, err := plugin.Open(ctx, "cloudflare", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = plugin.Sync(ctx, client, protocol.SyncParams{Instance: "test", Config: map[string]any{"api_token": "env://TEST_SK"}})
	var failure *plugin.Error
	if !errors.As(err, &failure) || failure.Code != "timeout" {
		t.Fatalf("HTTP cancellation: %v", err)
	}
}

func TestCloudflareTraversesZoneAndRecordPages(t *testing.T) {
	t.Setenv("TEST_CF_API", "synthetic-cloudflare-credential")
	old := http.DefaultTransport
	requests := 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		page := r.URL.Query().Get("page")
		if page != "1" && page != "2" {
			t.Fatalf("unexpected page: %s", page)
		}
		item := fmt.Sprintf(`{"id":"record-%s","name":"example.com","type":"A","content":"192.0.2.%s"}`, page, page)
		if r.URL.Path == "/client/v4/zones" {
			if r.URL.Query().Get("type") != "full,partial,secondary,internal" {
				t.Error("zone types were silently restricted")
			}
			item = fmt.Sprintf(`{"id":"zone-%s","name":"zone-%s.example.com"}`, page, page)
		}
		body := fmt.Sprintf(`{"success":true,"result":[%s],"result_info":{"page":%s,"total_count":2}}`, item, page)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	data := syncBuiltin(t, "cloudflare", map[string]any{"api_token": "env://TEST_CF_API"})
	if requests != 6 || strings.Count(string(data), `"id":"record-`) != 4 {
		t.Fatalf("incomplete pages: %d %s", requests, data)
	}
}

func TestCTYunRejectsRegisteredDomainsBeforeNetwork(t *testing.T) {
	client, err := plugin.Open(context.Background(), "ctyun", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = plugin.Sync(context.Background(), client, protocol.SyncParams{Config: map[string]any{"services": []string{"domains"}}})
	var failure *plugin.Error
	if !errors.As(err, &failure) || failure.Code != "config" {
		t.Fatalf("unsupported service must fail before resolving credentials: %v", err)
	}
}

func TestCloudflareRecordedSync(t *testing.T) {
	t.Setenv("TEST_CF_API", "synthetic-cloudflare-credential")
	requests := 0
	replayHTTP(t, func(r *http.Request) string {
		requests++
		if r.Method != "GET" || r.URL.Host != "api.cloudflare.com" || r.Header.Get("Authorization") != "Bearer synthetic-cloudflare-credential" {
			t.Errorf("unexpected Cloudflare request")
		}
		switch r.URL.Path {
		case "/client/v4/zones":
			return "cloudflare-zones.json"
		case "/client/v4/zones/00000000000000000000000000000001/dns_records":
			return "cloudflare-dns.json"
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			return ""
		}
	})
	client, err := plugin.Open(context.Background(), "cloudflare", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	data, err := plugin.Sync(context.Background(), client, protocol.SyncParams{Instance: "home", Config: map[string]any{"api_token": "env://TEST_CF_API"}})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests: %d", requests)
	}
	var result struct {
		Zones []struct {
			Name string `json:"name"`
			DNS  []struct {
				Name    string `json:"name"`
				Proxied bool   `json:"proxied"`
			} `json:"dns_records"`
		} `json:"zones"`
	}
	if json.Unmarshal(data, &result) != nil || len(result.Zones) != 1 || len(result.Zones[0].DNS) != 2 || result.Zones[0].DNS[0].Name != "example.com" || !result.Zones[0].DNS[1].Proxied {
		t.Fatalf("snapshot: %s", data)
	}
	for _, excluded := range []string{"synthetic-cloudflare-credential", "created_on", "comment", "Fictional"} {
		if strings.Contains(string(data), excluded) {
			t.Fatalf("unexpected snapshot field %s", excluded)
		}
	}
}

func TestAliyunRecordedSync(t *testing.T) {
	t.Setenv("TEST_AK", "synthetic-access-id")
	t.Setenv("TEST_SK", "synthetic-access-secret")
	requests := 0
	replayHTTP(t, func(r *http.Request) string {
		requests++
		if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Authorization"), "ACS3-HMAC-SHA256 Credential=synthetic-access-id,") {
			t.Error("invalid Aliyun authentication")
		}
		switch r.Header.Get("X-Acs-Action") {
		case "QueryDomainList":
			if r.URL.Host != "domain.aliyuncs.com" || r.Header.Get("X-Acs-Version") != "2018-01-29" {
				t.Error("invalid domain API")
			}
			return "aliyun-domains-" + r.URL.Query().Get("PageNum") + ".json"
		case "DescribeUserDomains":
			if r.URL.Host != "cdn.aliyuncs.com" || r.URL.Query().Get("CheckDomainShow") != "true" {
				t.Error("invalid CDN API")
			}
			return "aliyun-cdn.json"
		default:
			t.Error("unexpected action")
			return ""
		}
	})
	data := syncBuiltin(t, "aliyun", map[string]any{"access_key_id": "env://TEST_AK", "access_key_secret": "env://TEST_SK"})
	if requests != 3 || !strings.Contains(string(data), `"expires_at":"2030-01-01"`) || !strings.Contains(string(data), `"name":"cdn.example.com"`) || strings.Contains(string(data), "not-retained") || strings.Contains(string(data), "GmtModified") {
		t.Fatalf("Aliyun result: %s", data)
	}
	// example.com 的毫秒时间戳是 UTC 2029-12-31 16:30，即北京时间 2030-01-01 00:30。
	if strings.Count(string(data), `"expires_at":"2030-01-01"`) != 2 {
		t.Fatalf("Aliyun expiry not in Beijing time: %s", data)
	}
	if strings.Index(string(data), `"name":"example.com"`) > strings.Index(string(data), `"name":"example.net"`) {
		t.Fatal("unstable domain order")
	}
}

func TestTencentRecordedSync(t *testing.T) {
	t.Setenv("TEST_AK", "synthetic-access-id")
	t.Setenv("TEST_SK", "synthetic-access-secret")
	requests := 0
	replayHTTP(t, func(r *http.Request) string {
		requests++
		if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Authorization"), "TC3-HMAC-SHA256 Credential=synthetic-access-id/") {
			t.Error("invalid Tencent authentication")
		}
		var body struct {
			Offset int `json:"Offset"`
			Limit  int `json:"Limit"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Limit != 100 {
			t.Error("invalid Tencent pagination body")
		}
		switch r.Header.Get("X-Tc-Action") {
		case "DescribeDomainNameList":
			if r.URL.Host != "domain.tencentcloudapi.com" || r.Header.Get("X-Tc-Version") != "2018-08-08" {
				t.Error("invalid domain API")
			}
			return fmt.Sprintf("tencent-domains-%d.json", body.Offset)
		case "DescribeDomains":
			if r.URL.Host != "cdn.tencentcloudapi.com" || r.Header.Get("X-Tc-Version") != "2018-06-06" {
				t.Error("invalid CDN API")
			}
			return "tencent-cdn.json"
		default:
			t.Error("unexpected action")
			return ""
		}
	})
	data := syncBuiltin(t, "tencent", map[string]any{"access_key_id": "env://TEST_AK", "access_key_secret": "env://TEST_SK"})
	if requests != 3 || !strings.Contains(string(data), `"expires_at":"2030-01-01"`) || !strings.Contains(string(data), `"status":"offline"`) || strings.Contains(string(data), "synthetic-do-not-retain") {
		t.Fatalf("Tencent result: %s", data)
	}
}

func TestBaiduRecordedSync(t *testing.T) {
	t.Setenv("TEST_AK", "synthetic-access-id")
	t.Setenv("TEST_SK", "synthetic-access-secret")
	requests := 0
	replayHTTP(t, func(r *http.Request) string {
		requests++
		if r.Method != "GET" || r.URL.Path != "/v2/domain" || !strings.HasPrefix(r.Header.Get("Authorization"), "bce-auth-v1/synthetic-access-id/") {
			t.Error("invalid BCE request")
		}
		switch r.URL.Host {
		case "bcd.baidubce.com":
			if r.URL.Query().Get("pageNo") != "1" || r.URL.Query().Get("pageSize") != "100" {
				t.Error("invalid BCD pagination")
			}
			return "baidu-domains.json"
		case "cdn.baidubce.com":
			if marker := r.URL.Query().Get("marker"); marker == "" {
				return "baidu-cdn-1.json"
			} else if marker != "fixture+/==" {
				t.Error("marker was not preserved")
			}
			return "baidu-cdn-2.json"
		default:
			t.Error("unexpected BCE host")
			return ""
		}
	})
	data := syncBuiltin(t, "baidu", map[string]any{"access_key_id": "env://TEST_AK", "access_key_secret": "env://TEST_SK"})
	if requests != 3 || !strings.Contains(string(data), `"expires_at":"2027-01-01"`) || !strings.Contains(string(data), `"name":"media.example.com"`) || strings.Contains(string(data), "not-retained") {
		t.Fatalf("Baidu result: %s", data)
	}
}

func TestCTYunRecordedSync(t *testing.T) {
	t.Setenv("TEST_AK", "synthetic-access-id")
	t.Setenv("TEST_SK", "synthetic-access-secret")
	requests := 0
	scopes := map[string]bool{}
	replayHTTP(t, func(r *http.Request) string {
		requests++
		if r.Method != "GET" || r.URL.Host != "cdnapi-global.ctapi.ctyun.cn" || r.URL.Path != "/domain/query-domain-list" || !strings.HasPrefix(r.Header.Get("Eop-Authorization"), "synthetic-access-id Headers=ctyun-eop-request-id;eop-date Signature=") {
			t.Error("invalid CTYun request")
		}
		q := r.URL.Query()
		scopes[q.Get("area_scope")] = true
		if q.Get("page_size") != "100" {
			t.Error("invalid CTYun page size")
		}
		if q.Get("area_scope") == "1" {
			return "ctyun-cdn-" + q.Get("page") + ".json"
		}
		return "ctyun-cdn-empty.json"
	})
	data := syncBuiltin(t, "ctyun", map[string]any{"access_key_id": "env://TEST_AK", "access_key_secret": "env://TEST_SK"})
	if requests != 4 || len(scopes) != 3 || !strings.Contains(string(data), `"status":"6"`) || !strings.Contains(string(data), `"unsupported":["registered-domains"]`) || strings.Contains(string(data), "insert_date") {
		t.Fatalf("CTYun result: %s", data)
	}
}
