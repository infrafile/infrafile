package audit

import (
	"context"
	"strings"
	"testing"
)

func TestCDNDriftStates(t *testing.T) {
	for _, tc := range []struct {
		plugin, status   string
		stopped, unknown bool
	}{
		{"aliyun", "online", false, false}, {"aliyun", "offline", true, false},
		{"aliyun", "stopping", false, false}, {"aliyun", "configure_failed", false, false},
		{"tencent", "online", false, false}, {"tencent", "offline", true, false},
		{"tencent", "closing", false, false}, {"tencent", "rejected", false, false},
		{"ctyun", "4", false, false}, {"ctyun", "6", true, false}, {"ctyun", "8", true, false},
		{"ctyun", "5", false, false}, {"ctyun", "11", false, false},
		{"aliyun", "future", false, true}, {"tencent", "future", false, true}, {"ctyun", "99", false, true},
		{"baidu", "offline", false, false},
	} {
		t.Run(tc.plugin+"/"+tc.status, func(t *testing.T) {
			in := &Input{Repo: repo(t, "", map[string]string{
				"inventory/resources/edge.yaml": "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: edge}\nspec: {provider: " + tc.plugin + ", type: cdn, resource_id: same, endpoint: static.example.com}\n",
			}), Now: now, Snapshots: []Snapshot{{Plugin: tc.plugin, Instance: "home", File: "state/" + tc.plugin + "/home.yaml", ObservedAt: &now,
				Spec: map[string]any{"cdn": []any{map[string]any{"name": "STATIC.EXAMPLE.COM.", "id": "same", "status": tc.status}}}}}}
			if err := Prepare(context.Background(), in, []Rule{cdnDrift}); err != nil {
				t.Fatal(err)
			}
			f, n := Run(in, []Rule{cdnDrift})
			if (len(f) == 1) != tc.stopped || len(f) > 1 || (len(n) == 1) != tc.unknown {
				t.Fatalf("findings=%+v notices=%+v", f, n)
			}
			if tc.stopped && (f[0].Target != "Resource/edge" || f[0].Evidence["status"] != tc.status || f[0].Evidence["observed_at"] == nil) {
				t.Fatal(f)
			}
			if tc.unknown && (n[0].Code != "cdn_status_unknown" || !strings.Contains(n[0].Message, tc.status)) {
				t.Fatal(n)
			}
		})
	}
}

func TestCDNDriftIdentityAndWaiver(t *testing.T) {
	for _, tc := range []struct {
		name, spec, metadata, id string
		want, waived             bool
	}{
		{"renamed", "{provider: aliyun, type: cdn, resource_id: same}", "", "same", true, false},
		{"fallback", "{provider: aliyun, type: cdn, resource_id: ledger, endpoint: STATIC.EXAMPLE.COM.}", "", "", true, false},
		{"other_id", "{provider: aliyun, type: cdn, resource_id: other, endpoint: static.example.com}", "", "same", false, false},
		{"other_provider", "{provider: tencent, type: cdn, resource_id: same}", "", "same", false, false},
		{"other_type", "{provider: aliyun, type: bucket, resource_id: same}", "", "same", false, false},
		{"archived", "{provider: aliyun, type: cdn, resource_id: same}", ", archived: {date: 2026-10-28, reason: retired}", "same", false, false},
		{"waived", "{provider: aliyun, type: cdn, resource_id: same}", ", waivers: [{rule: cdn_drift, reason: migration}]", "same", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := map[string]any{"name": "static.example.com", "id": tc.id, "status": "offline"}
			in := &Input{Repo: repo(t, "", map[string]string{"inventory/resources/edge.yaml": "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: edge" + tc.metadata + "}\nspec: " + tc.spec + "\n"}), Now: now,
				Snapshots: []Snapshot{{Plugin: "aliyun", Instance: "home", File: "state/aliyun/home.yaml", Spec: map[string]any{"cdn": []any{item, item}}}}}
			if err := Prepare(context.Background(), in, []Rule{cdnDrift}); err != nil {
				t.Fatal(err)
			}
			f, _ := Run(in, []Rule{cdnDrift})
			if (len(f) == 1) != tc.want || len(f) > 1 || tc.want && (f[0].Waiver != nil) != tc.waived {
				t.Fatalf("%+v", f)
			}
		})
	}
}

func TestCDNDriftMalformedAndCancelled(t *testing.T) {
	for _, value := range []any{"invalid", []any{map[string]any{"name": "static.example.com", "status": 6}}, []any{map[string]any{"name": "invalid host", "status": "offline"}}} {
		in := &Input{Repo: repo(t, "", nil), Snapshots: []Snapshot{{Plugin: "aliyun", File: "state/aliyun/home.yaml", Spec: map[string]any{"cdn": value}}}}
		if err := Prepare(context.Background(), in, []Rule{cdnDrift}); err == nil {
			t.Fatal("malformed CDN accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Prepare(ctx, &Input{}, []Rule{cdnDrift}); err == nil {
		t.Fatal("cancellation ignored")
	}
}
