package plugin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/protocol"
)

func TestCloudflareControlPause(t *testing.T) {
	t.Setenv("CF_CONTROL_TEST", "synthetic-control-value")
	paused := false
	writes := 0
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.cloudflare.com" || r.URL.Path != "/client/v4/zones/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || r.Header.Get("Authorization") != "Bearer synthetic-control-value" {
			t.Fatalf("unexpected request: %s", r.URL)
		}
		if r.Method == "PATCH" {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 1 || body["paused"] != true {
				t.Fatalf("wrong patch: %v", body)
			}
			paused = true
			writes++
		} else if r.Method != "GET" {
			t.Fatal(r.Method)
		}
		data, _ := json.Marshal(map[string]any{"success": true, "result": map[string]any{"id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": "example.com", "paused": paused, "account": map[string]any{"id": "account-example"}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	c, _ := plugin.Open(context.Background(), "cloudflare", t.TempDir())
	defer c.Close()
	p := protocol.ControlParams{Instance: "home", Config: map[string]any{"api_token": "env://CF_CONTROL_TEST", "collect_origin": true}, Target: protocol.Target{ZoneID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Action: "cdn-pause", Parameters: map[string]any{}}
	data, err := plugin.Invoke(context.Background(), c, "probe", p)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := plugin.Object(data)
	p.ExpectedHash = plugin.Hash(before)
	if _, err := plugin.Invoke(context.Background(), c, "control", p); err != nil {
		t.Fatal(err)
	}
	if writes != 1 || !paused {
		t.Fatal("control did not update")
	}
	if _, err := plugin.Invoke(context.Background(), c, "control", p); err == nil || writes != 1 {
		t.Fatal("stale control was not refused")
	}
}

func TestCloudflareOriginKeepsRuleDefinition(t *testing.T) {
	t.Setenv("CF_CONTROL_TEST", "synthetic-control-value")
	fixture, err := os.ReadFile("testdata/cloudflare-origin-rules.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	json.Unmarshal(fixture, &envelope)
	ruleset := envelope["result"].(map[string]any)
	writes := 0
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body any
		switch r.Method + " " + r.URL.Path {
		case "GET /client/v4/zones/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":
			body = map[string]any{"id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": "example.com", "paused": false}
		case "GET /client/v4/zones/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/rulesets/phases/http_request_origin/entrypoint":
			body = ruleset
		case "PATCH /client/v4/zones/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/rulesets/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/rules/cccccccccccccccccccccccccccccccc":
			var patch map[string]any
			if json.NewDecoder(r.Body).Decode(&patch) != nil {
				t.Fatal("invalid JSON")
			}
			want := map[string]any{"action": "route", "expression": `http.host eq "assets.example.com"`, "description": "Fictional assets origin", "enabled": true, "ref": "assets_origin", "action_parameters": map[string]any{"host_header": "static.example.net", "origin": map[string]any{"host": "new-origin.example.net", "port": float64(8443)}, "sni": map[string]any{"value": "tls.example.net"}}}
			if !reflect.DeepEqual(patch, want) {
				t.Fatalf("changed unrelated definition: %#v", patch)
			}
			patch["id"] = "cccccccccccccccccccccccccccccccc"
			patch["version"] = "5"
			ruleset["rules"].([]any)[0] = patch
			ruleset["version"] = "8"
			writes++
			body = ruleset
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		data, _ := json.Marshal(map[string]any{"success": true, "result": body})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	c, _ := plugin.Open(context.Background(), "cloudflare", t.TempDir())
	defer c.Close()
	p := protocol.ControlParams{Instance: "home", Config: map[string]any{"api_token": "env://CF_CONTROL_TEST"}, Target: protocol.Target{ZoneID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RulesetID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", RuleID: "cccccccccccccccccccccccccccccccc"}, Action: "cdn-origin", Parameters: map[string]any{"host": "new-origin.example.net"}}
	data, err := plugin.Invoke(context.Background(), c, "probe", p)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := plugin.Object(data)
	after, err := plugin.Desired(before, p.Target, p.Action, p.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	p.ExpectedHash = plugin.Hash(before)
	if _, err := plugin.Invoke(context.Background(), c, "control", p); err != nil {
		t.Fatal(err)
	}
	data, err = plugin.Invoke(context.Background(), c, "probe", p)
	if err != nil {
		t.Fatal(err)
	}
	observed, _ := plugin.Object(data)
	if writes != 1 || plugin.Hash(after) != plugin.Hash(observed) {
		t.Fatalf("wrong readback: %s", data)
	}
}
