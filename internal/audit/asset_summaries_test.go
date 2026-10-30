package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAssetSummariesMatchSingleKey(t *testing.T) {
	for _, name := range []string{"fresh", "risk-and-unknown", "failed", "stale", "boundary", "no-ttl", "waived", "all-na", "archived", "unconfigured"} {
		t.Run(name, func(t *testing.T) {
			r := inspectRepo(t)
			age, at, state := days(7), now, "succeeded"
			observed := domainObservations()
			if name == "stale" {
				at = now.Add(-8 * 24 * time.Hour)
			}
			if name == "boundary" {
				at = now.Add(-7 * 24 * time.Hour)
			}
			if name == "no-ttl" {
				age = nil
			}
			if name == "failed" {
				state = "failed"
			}
			if name == "risk-and-unknown" || name == "waived" {
				observed["domains"] = []any{}
			}
			inspectEvidence(t, r, "aliyun", "home", observed, at, state)
			dnsState := "succeeded"
			if name == "risk-and-unknown" {
				dnsState = "failed"
			}
			inspectEvidence(t, r, "dns", "public", dnsObservations(), now, dnsState)
			inspectBaseline(t, r)
			for _, record := range r.Records {
				if record.Kind != "Domain" {
					continue
				}
				md := record.Value.(map[string]any)["metadata"].(map[string]any)
				if name == "waived" {
					md["waivers"] = []any{map[string]any{"rule": "missing", "reason": "reviewed migration"}}
				}
				if name == "archived" {
					md["archived"] = map[string]any{"date": "2026-10-29", "reason": "retired"}
				}
			}
			if name == "all-na" {
				first := inspectDomain(t, r, age)
				items := []any{}
				for _, check := range first.Coverage.Checks {
					items = append(items, map[string]any{"check": check.ID, "reason": "reviewed elsewhere", "owner": "tester", "review_by": "2026-12-10"})
				}
				r.Config.Value.(map[string]any)["audit"].(map[string]any)["not_applicable"] = items
			}
			if name == "unconfigured" {
				r.Config.Value.(map[string]any)["plugins"] = map[string]any{}
				r.Config.Value.(map[string]any)["audit"] = map[string]any{}
			}
			if name == "archived" {
				r.Config.Value.(map[string]any)["audit"].(map[string]any)["watch"] = []any{}
			}
			got, err := SummarizeAssets(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: age})
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Rows) != 2 || got.AsOf != now.Format(time.RFC3339) {
				t.Fatal(got)
			}
			previous := ""
			for _, row := range got.Rows {
				if row.Key <= previous {
					t.Fatal("rows not key-sorted")
				}
				previous = row.Key
				single, err := Inspect(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: age, Keys: []string{row.Key}})
				if err != nil {
					t.Fatal(err)
				}
				if row.OK != single.OK || row.Coverage.Expected != single.Coverage.Expected || row.Coverage.Checked != single.Coverage.Checked || row.Coverage.Unknown != single.Coverage.Unknown || row.Coverage.NotApplicable != single.Coverage.NotApplicable || row.Risk.Confirmed != single.Risk.Confirmed || row.Risk.Unknown != single.Risk.Unknown || row.Risk.Waived != single.Risk.Waived || !reflect.DeepEqual(row.Execution, single.Execution) {
					t.Fatalf("%s differs: %+v vs %+v", row.Key, row, single)
				}
				if row.Reason.Code == "" || row.Reason.Message == "" {
					t.Fatal("missing summary reason")
				}
				for _, evidence := range row.Evidence {
					found := false
					for _, check := range single.Coverage.Checks {
						for _, item := range check.Evidence {
							found = found || reflect.DeepEqual(evidence, item)
						}
					}
					if !found {
						t.Fatal("invented evidence", evidence)
					}
				}
				if row.Key == "Domain/example.com" {
					if name == "fresh" && !row.OK {
						t.Fatal("fresh domain should pass")
					}
					if name == "risk-and-unknown" && (row.Risk.Confirmed != 1 || row.Coverage.Unknown != 1 || row.OK) {
						t.Fatal("risk concealed uncertainty", row)
					}
					if name == "all-na" && (row.OK || row.Coverage.NotApplicable != 2) {
						t.Fatal("NA became green", row)
					}
					if name == "waived" && row.Risk.Waived != 1 {
						t.Fatal("waiver lost", row)
					}
				}
			}
		})
	}
}

func TestAssetSummariesDoNotBorrowAccountSuccess(t *testing.T) {
	r := repo(t, "plugins:\n  aliyun:\n    instances:\n      one: {services: [cdn]}\n      two: {services: [cdn]}\naudit:\n  watch:\n    - {target: Resource/edge, plugin: aliyun, instance: one, service: cdn}\n", map[string]string{"inventory/resources/edge.yaml": "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: edge}\nspec: {provider: aliyun, type: cdn, endpoint: edge.example.com}"})
	for _, instance := range []string{"one", "two"} {
		status := "succeeded"
		if instance == "one" {
			status = "failed"
		}
		inspectEvidence(t, r, "aliyun", instance, map[string]any{"cdn": []any{map[string]any{"name": "edge.example.com", "status": "offline"}}}, now, status)
	}
	got, err := SummarizeAssets(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: days(7)})
	if err != nil {
		t.Fatal(err)
	}
	row := got.Rows[0]
	if row.OK || row.Risk.Confirmed != 0 || row.Risk.Unknown != 2 || len(row.Execution) != 1 || row.Execution[0].Instance != "one" || row.Execution[0].Status != "failed" {
		t.Fatal("borrowed another account", row)
	}
}

func TestPreparedInspectionIsolation(t *testing.T) {
	r := inspectRepo(t)
	inspectEvidence(t, r, "aliyun", "home", domainObservations(), now, "succeeded")
	inspectEvidence(t, r, "dns", "public", dnsObservations(), now, "succeeded")
	inspectBaseline(t, r)
	opts := InspectOptions{Now: now, MaxAgeDays: days(7)}
	want, err := inspect(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: days(7), Keys: []string{"Domain/example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareInspection(context.Background(), r, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(r.Dir, "state")); err != nil {
		t.Fatal(err)
	}
	a, err := inspectPrepared(context.Background(), prepared, []string{"Domain/example.com"})
	if err != nil || !reflect.DeepEqual(a.report, want.report) {
		t.Fatal("projection reread or changed inputs", err)
	}
	before, _ := json.Marshal(a.report)
	if _, err := inspectPrepared(context.Background(), prepared, []string{"Server/web"}); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(a.report)
	if string(before) != string(after) {
		t.Fatal("another scope mutated the first report")
	}
	empty, err := inspectPrepared(context.Background(), prepared, []string{})
	if err != nil || empty.report.OK || len(empty.report.Assets) != 0 || len(empty.report.Execution) != 0 {
		t.Fatal("empty selection became global", err)
	}
	global, err := inspectPrepared(context.Background(), prepared, nil)
	if err != nil || len(global.report.Assets) != 2 || len(global.report.Execution) != 2 {
		t.Fatal("nil selection lost global scope", err)
	}
	for _, keys := range [][]string{{"Server/absent"}, {"Server/web", "Server/web"}} {
		if _, err := inspectPrepared(context.Background(), prepared, keys); err == nil {
			t.Fatal("invalid selection accepted", keys)
		}
	}
}

func TestAssetSummariesFailures(t *testing.T) {
	r := inspectRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SummarizeAssets(ctx, r, InspectOptions{Now: now}); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := SummarizeAssets(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: days(-1)}); err == nil {
		t.Fatal("accepted negative TTL")
	}
	write(t, r.Dir, "state/.findings.json", "corrupted history")
	if _, err := SummarizeAssets(context.Background(), r, InspectOptions{Now: now}); err == nil {
		t.Fatal("ignored corrupted history")
	}
	if _, err := inspect(context.Background(), r, InspectOptions{Now: now}); err != nil {
		t.Fatal("inspection for history refresh acquired a history dependency", err)
	}
}

func TestAssetSummariesUnsupportedAndEmpty(t *testing.T) {
	r := repo(t, "", map[string]string{
		"inventory/accounts/home.yaml": "apiVersion: infrafile/v1alpha1\nkind: Account\nmetadata: {name: home}\nspec: {provider: example}\n",
		"inventory/apps/blog.yaml":     "apiVersion: infrafile/v1alpha1\nkind: App\nmetadata: {name: blog}\nspec: {}\n",
		"inventory/repos/code.yaml":    "apiVersion: infrafile/v1alpha1\nkind: Repo\nmetadata: {name: code}\nspec: {remote: 'https://example.com/code.git'}\n",
	})
	got, err := SummarizeAssets(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: days(7)})
	if err != nil || len(got.Rows) != 3 {
		t.Fatal(got, err)
	}
	for _, row := range got.Rows {
		if row.OK || row.Risk.Confirmed != 0 {
			t.Fatal("unsupported asset became healthy", row)
		}
	}
	empty, err := SummarizeAssets(context.Background(), r, InspectOptions{Now: now, Keys: []string{}})
	if err != nil || empty.Rows == nil || len(empty.Rows) != 0 {
		t.Fatal("empty selection was expanded", empty, err)
	}
}

func TestAssetSummariesScale(t *testing.T) {
	for _, count := range []int{100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			files := map[string]string{}
			for n := 0; n < count; n++ {
				name := fmt.Sprintf("app-%04d", n)
				files["inventory/apps/"+name+".yaml"] = "apiVersion: infrafile/v1alpha1\nkind: App\nmetadata: {name: " + name + "}\nspec: {}\n"
			}
			r := repo(t, "", files)
			start := time.Now()
			got, err := SummarizeAssets(context.Background(), r, InspectOptions{Now: now, MaxAgeDays: days(7)})
			if err != nil || len(got.Rows) != count {
				t.Fatal("incomplete summary", err)
			}
			t.Logf("%d assets: %s", count, time.Since(start))
		})
	}
}
