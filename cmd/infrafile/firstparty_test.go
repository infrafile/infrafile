package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/testutil"
)

type cloudReplay func(*http.Request) (*http.Response, error)

func (r cloudReplay) RoundTrip(req *http.Request) (*http.Response, error) { return r(req) }

func firstPartyRepo(t *testing.T) {
	t.Helper()
	fixtures, err := filepath.Abs("../../internal/plugin/testdata")
	if err != nil {
		t.Fatal(err)
	}
	emptyRepo(t)
	t.Setenv("INFRAFILE_TEST_CLOUD_AK", "synthetic-access-id")
	t.Setenv("INFRAFILE_TEST_CLOUD_SK", "synthetic-access-secret")
	config := "apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n"
	for _, name := range []string{"aliyun", "baidu", "ctyun", "tencent"} {
		config += fmt.Sprintf("  %s:\n    instances:\n      demo: {access_key_id: 'env://INFRAFILE_TEST_CLOUD_AK', access_key_secret: 'env://INFRAFILE_TEST_CLOUD_SK'}\n", name)
	}
	config += "  cloudflare:\n    instances:\n      demo: {api_token: 'env://INFRAFILE_TEST_CLOUD_SK'}\n"
	if err := os.WriteFile("infrafile.yaml", []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	old := http.DefaultTransport
	http.DefaultTransport = cloudReplay(func(r *http.Request) (*http.Response, error) {
		file := ""
		switch r.URL.Host {
		case "api.cloudflare.com":
			if r.URL.Path == "/client/v4/zones" {
				file = "cloudflare-zones.json"
			} else {
				file = "cloudflare-dns.json"
			}
		case "domain.aliyuncs.com":
			file = "aliyun-domains-" + r.URL.Query().Get("PageNum") + ".json"
		case "cdn.aliyuncs.com":
			file = "aliyun-cdn.json"
		case "domain.tencentcloudapi.com":
			var body struct {
				Offset int `json:"Offset"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				return nil, fmt.Errorf("invalid replay body")
			}
			file = fmt.Sprintf("tencent-domains-%d.json", body.Offset)
		case "cdn.tencentcloudapi.com":
			file = "tencent-cdn.json"
		case "bcd.baidubce.com":
			file = "baidu-domains.json"
		case "cdn.baidubce.com":
			if r.URL.Query().Get("marker") == "" {
				file = "baidu-cdn-1.json"
			} else {
				file = "baidu-cdn-2.json"
			}
		case "cdnapi-global.ctapi.ctyun.cn":
			if r.URL.Query().Get("area_scope") == "1" {
				file = "ctyun-cdn-" + r.URL.Query().Get("page") + ".json"
			} else {
				file = "ctyun-cdn-empty.json"
			}
		default:
			return nil, fmt.Errorf("network request not in replay")
		}
		data, err := os.ReadFile(filepath.Join(fixtures, file))
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
}

func TestFirstPartySyncWritesOnlyProjectedState(t *testing.T) {
	firstPartyRepo(t)
	before, err := os.ReadFile("infrafile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"sync", "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s %s", code, &out, &stderr)
	}
	files, err := filepath.Glob("state/*/*.yaml")
	if err != nil || len(files) != 5 {
		t.Fatalf("snapshot files: %v %v", files, err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, blocked := range []string{"synthetic-access", "not-retained", "synthetic-do-not-retain", "Authentication", "insert_date"} {
			if strings.Contains(string(data), blocked) {
				t.Fatalf("unwanted data in %s", file)
			}
		}
	}
	after, _ := os.ReadFile("infrafile.yaml")
	if !bytes.Equal(before, after) {
		t.Fatal("modified config")
	}
}

func TestFirstPartyCredentialTimeoutContinuesNextPlugin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("密钥命令夹具使用 POSIX sleep")
	}
	emptyRepo(t)
	testutil.IsolateKeys(t)
	ref, _, _ := testutil.EncryptedFile(t, ".")
	t.Setenv("SOPS_AGE_KEY_CMD", "sleep 1")
	t.Setenv("INFRAFILE_TEST_CLOUD_AK", "synthetic-access-id")
	config := fmt.Sprintf("apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n  aliyun:\n    instances:\n      slow: {access_key_id: 'env://INFRAFILE_TEST_CLOUD_AK', access_key_secret: '%s'}\n  example:\n    instances:\n      next: {}\n", ref)
	if err := os.WriteFile("infrafile.yaml", []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	start := time.Now()
	if code := run([]string{"sync", "--timeout", "50ms", "--json"}, &out, &stderr); code != 1 || time.Since(start) > 600*time.Millisecond {
		t.Fatalf("credential blocked sync: exit=%d elapsed=%s", code, time.Since(start))
	}
	var result struct {
		Results []struct {
			Plugin string `json:"plugin"`
			Code   string `json:"code"`
			OK     bool   `json:"ok"`
		} `json:"results"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || len(result.Results) != 2 || result.Results[0].Code != "timeout" || !result.Results[1].OK || result.Results[1].Plugin != "example" {
		t.Fatalf("timeout isolation: %s %s", &out, &stderr)
	}
	if _, err := os.Stat("state/example/next.yaml"); err != nil {
		t.Fatal("following plugin did not write its snapshot")
	}
	if _, err := os.Stat("state/aliyun/slow.yaml"); !os.IsNotExist(err) {
		t.Fatal("failed plugin wrote a snapshot")
	}
}
