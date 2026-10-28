package audit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
)

var now = time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func snapshotYAML(name string) string {
	return "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata:\n  name: " + name + "\nspec: {}\n"
}

// repo 写出一个数据仓并加载；config 追加在 infrafile.yaml 的 kind 之后。
func repo(t *testing.T, config string, files map[string]string) *ledger.Repo {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\n"+config)
	for rel, content := range files {
		write(t, dir, rel, content)
	}
	r, problems, err := ledger.Load(context.Background(), dir)
	if err != nil || len(problems) != 0 {
		t.Fatalf("Load = %v, %v", problems, err)
	}
	return r
}

func days(n int) *int { return &n }

func schemaEnum(t *testing.T, file string, path ...string) []string {
	t.Helper()
	files, _ := ledger.SchemaFiles()
	var node any
	if err := json.Unmarshal(files[file], &node); err != nil {
		t.Fatal(err)
	}
	for _, key := range path {
		node = node.(map[string]any)[key]
	}
	enum := []string{}
	for _, v := range node.([]any) {
		enum = append(enum, v.(string))
	}
	sort.Strings(enum)
	return enum
}

func TestRegistryMatchesSchema(t *testing.T) {
	ids := []string{}
	for _, r := range Rules() {
		if r.ID == "" || r.Description == "" || r.Eval == nil {
			t.Fatalf("规则不完整：%+v", r)
		}
		ids = append(ids, r.ID)
	}
	all := slices.Clone(ids)
	sort.Strings(all)
	waivers := schemaEnum(t, "common.schema.json", "$defs", "metadata", "properties", "waivers", "items", "properties", "rule", "enum")
	if !slices.Equal(all, waivers) {
		t.Fatalf("已注册 %v，与 waivers 枚举 %v 不一致", ids, waivers)
	}
	ignore := schemaEnum(t, "config.schema.json", "properties", "audit", "properties", "ignore", "items", "properties", "rule", "enum")
	for _, id := range ignore {
		if !slices.Contains(ids, id) {
			t.Fatalf("audit.ignore 允许未注册的规则 %s", id)
		}
	}
}

func TestSelect(t *testing.T) {
	ids := func(rules []Rule) []string {
		out := []string{}
		for _, r := range rules {
			out = append(out, r.ID)
		}
		return out
	}
	got, err := Select(nil, &Input{})
	if err != nil || !slices.Equal(ids(got), []string{"cdn_drift", "origin_exposure", "orphan", "zombie"}) {
		t.Fatalf("未设 --max-age 时默认规则 = %v, %v", ids(got), err)
	}
	got, err = Select(nil, &Input{MaxAge: days(7)})
	if err != nil || !slices.Equal(ids(got), []string{"cdn_drift", "missing", "origin_exposure", "orphan", "stale_state", "zombie"}) {
		t.Fatalf("默认规则 = %v, %v", ids(got), err)
	}
	got, err = Select([]string{"stale_state", "stale_state"}, &Input{MaxAge: days(7)})
	if err != nil || !slices.Equal(ids(got), []string{"stale_state"}) {
		t.Fatalf("重复规则 = %v, %v", ids(got), err)
	}
	for id, want := range map[string]string{"stale_state": "--max-age", "missing": "--max-age", "nope": "未知规则"} {
		if _, err := Select([]string{id}, &Input{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Select(%s) err = %v，想要包含 %q", id, err, want)
		}
	}
}

func TestReadSnapshots(t *testing.T) {
	r := repo(t, "plugins:\n  aliyun:\n    instances:\n      home: {}\n", map[string]string{
		"state/aliyun/home.yaml":  snapshotYAML("home"),
		"state/aliyun/other.yaml": snapshotYAML("other"),
		"state/ssh/web.yaml":      "a: [\n",
		"state/_sync.yaml":        "apiVersion: infrafile/v1alpha1\nkind: Sync\nspec:\n  aliyun:\n    home: 2026-11-28T03:00:00Z\n    other: 2026-11-28T03:00:00Z\n",
	})
	snaps, notices, err := ReadSnapshots(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].Plugin != "aliyun" || snaps[0].Instance != "home" || snaps[0].File != "state/aliyun/home.yaml" {
		t.Fatalf("snapshots = %+v", snaps)
	}
	if snaps[0].ObservedAt == nil || !snaps[0].ObservedAt.Equal(time.Date(2026, 11, 28, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("observed_at = %v", snaps[0].ObservedAt)
	}
	if len(notices) != 2 || notices[0].Code != "instance_disabled" || notices[0].Target != "aliyun/other" || notices[1].File != "state/ssh/web.yaml" {
		t.Fatalf("notices = %+v", notices)
	}

	broken := repo(t, "", map[string]string{"state/_sync.yaml": "a: [\n"})
	if _, _, err := ReadSnapshots(context.Background(), broken); err == nil || !strings.Contains(err.Error(), "_sync.yaml") {
		t.Fatalf("损坏的 _sync.yaml err = %v", err)
	}
}

func TestStaleState(t *testing.T) {
	fresh := now.Add(-3 * 24 * time.Hour)
	old := now.Add(-61 * 24 * time.Hour)
	in := &Input{
		Repo: repo(t, "", nil),
		Now:  now,
		Snapshots: []Snapshot{
			{Plugin: "aliyun", Instance: "home", File: "state/aliyun/home.yaml", ObservedAt: &fresh},
			{Plugin: "cloudflare", Instance: "home", File: "state/cloudflare/home.yaml", ObservedAt: &old},
			{Plugin: "dns", Instance: "blog", File: "state/dns/blog.yaml"},
		},
		MaxAge: days(7),
	}
	rules, err := Select([]string{"stale_state"}, in)
	if err != nil {
		t.Fatal(err)
	}
	findings, notices := Run(in, rules)
	if len(notices) != 0 || len(findings) != 2 {
		t.Fatalf("findings = %+v, notices = %+v", findings, notices)
	}
	if f := findings[0]; f.Target != "cloudflare/home" || f.File != "state/cloudflare/home.yaml" || f.Evidence["age_days"] != 61 || f.Evidence["max_age_days"] != 7 {
		t.Fatalf("超期 = %+v", f)
	}
	if f := findings[1]; f.Target != "dns/blog" || f.Evidence["observed_at"] != nil || f.Evidence["age_days"] != nil {
		t.Fatalf("无同步记录 = %+v", f)
	}
}

func TestStaleStateAgeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		age   time.Duration
		limit int
		stale bool
	}{
		{"at_limit", 7 * 24 * time.Hour, 7, false},
		{"past_limit", 7*24*time.Hour + time.Nanosecond, 7, true},
		{"zero", time.Nanosecond, 0, true},
		{"future", -24 * time.Hour, 0, false},
		{"large_limit", 24 * time.Hour, 200000, false},
		{"max_int", 24 * time.Hour, int(^uint(0) >> 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := now.Add(-tc.age)
			in := &Input{Now: now, MaxAge: days(tc.limit), Snapshots: []Snapshot{{Plugin: "example", Instance: "home", ObservedAt: &at}}}
			if got := staleState.Eval(in); (len(got) > 0) != tc.stale {
				t.Fatalf("age %s, limit %d: findings = %+v", tc.age, tc.limit, got)
			}
		})
	}
}

func TestWaivers(t *testing.T) {
	old := now.Add(-30 * 24 * time.Hour)
	snaps := []Snapshot{
		{Plugin: "aliyun", Instance: "home", File: "state/aliyun/home.yaml", ObservedAt: &old},
		{Plugin: "cloudflare", Instance: "home", File: "state/cloudflare/home.yaml", ObservedAt: &old},
	}
	ignore := func(entries ...string) string {
		return "audit:\n  ignore:\n" + strings.Join(entries, "")
	}
	entry := func(plugin, until string) string {
		s := "    - rule: stale_state\n      plugin: " + plugin + "\n      instance: home\n      reason: 只在月初同步\n"
		if until != "" {
			s += "      until: " + until + "\n"
		}
		return s
	}
	record := "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata:\n  name: web1\n  waivers:\n    - rule: stale_state\n      reason: 示例\nspec: {}\n"
	cases := []struct {
		name       string
		config     string
		files      map[string]string
		rules      []string
		waived     []bool
		notices    []string
		noticeFile string
	}{
		{name: "none", waived: []bool{false, false}},
		{name: "all", config: ignore(entry("aliyun", ""), entry("cloudflare", "2026-12-01")), waived: []bool{true, true}},
		{name: "partial", config: ignore(entry("cloudflare", "")), waived: []bool{false, true}},
		{name: "expired", config: ignore(entry("cloudflare", "2026-11-30")), waived: []bool{false, false}, notices: []string{"waiver_expired"}, noticeFile: "infrafile.yaml"},
		{name: "unused", config: ignore(entry("example", "")), waived: []bool{false, false}, notices: []string{"waiver_unused"}, noticeFile: "infrafile.yaml"},
		{name: "record", files: map[string]string{"inventory/servers/web1.yaml": record}, waived: []bool{false, false}, notices: []string{"waiver_unused"}, noticeFile: "inventory/servers/web1.yaml"},
		{name: "rule-not-run", config: ignore(entry("example", "")), rules: []string{}, notices: nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := &Input{Repo: repo(t, c.config, c.files), Snapshots: snaps, Now: now, MaxAge: days(7)}
			rules, err := Select(c.rules, in)
			if c.rules != nil && len(c.rules) == 0 {
				rules = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			findings, notices := Run(in, rules)
			if len(findings) != len(c.waived) {
				t.Fatalf("findings = %+v", findings)
			}
			for i, f := range findings {
				if (f.Waiver != nil) != c.waived[i] {
					t.Errorf("%s 豁免 = %+v，想要 %v", f.Target, f.Waiver, c.waived[i])
				}
				if f.Waiver != nil && (f.Waiver.Reason != "只在月初同步" || f.Waiver.Source != "infrafile.yaml") {
					t.Errorf("waiver = %+v", f.Waiver)
				}
			}
			codes := []string{}
			for _, n := range notices {
				codes = append(codes, n.Code)
				if n.File != c.noticeFile || n.Rule != "stale_state" {
					t.Errorf("notice = %+v", n)
				}
			}
			if !slices.Equal(codes, c.notices) && len(codes)+len(c.notices) != 0 {
				t.Fatalf("notices = %v，想要 %v", codes, c.notices)
			}
		})
	}
}

func TestArchivedRecordWaiversIgnored(t *testing.T) {
	record := "apiVersion: infrafile/v1alpha1\nkind: Server\nmetadata:\n  name: web1\n  archived: 2026-01-01\n  waivers:\n    - rule: stale_state\n      reason: 示例\nspec: {}\n"
	in := &Input{Repo: repo(t, "", map[string]string{"inventory/servers/web1.yaml": record}), Now: now, MaxAge: days(7)}
	rules, _ := Select(nil, in)
	if _, notices := Run(in, rules); len(notices) != 0 {
		t.Fatalf("归档记录的豁免不应参与：%+v", notices)
	}
}
