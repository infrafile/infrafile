package audit

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/snapshot"
	"go.yaml.in/yaml/v3"
)

const missingConfig = `plugins:
  aliyun:
    instances:
      home: {services: [domains]}
      other: {services: [domains]}
audit:
  watch:
    - {target: Domain/example.com, plugin: aliyun, instance: home, service: domains}
`

func TestMissingRequiresNonnegativeAge(t *testing.T) {
	for _, limit := range []*int{nil, days(-1)} {
		if _, err := Select([]string{"missing"}, &Input{MaxAge: limit}); err == nil {
			t.Fatal("missing accepted an unspecified or negative age")
		}
	}
}

func TestMissingEvidence(t *testing.T) {
	for _, name := range []string{"absent", "present", "no-baseline", "binding-changed", "legacy", "failed", "running", "config-changed", "hash-changed", "time-changed", "no-snapshot", "service-absent", "service-broken", "baseline-broken", "stale", "future", "baseline-future", "boundary", "beyond-boundary", "huge-max-age", "other-account-baseline", "zero-age", "zero-age-expired"} {
		t.Run(name, func(t *testing.T) {
			r := repo(t, missingConfig, map[string]string{"inventory/domains/example.com.yaml": "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec: {}\n"})
			watches, problems := observation.Watches(r)
			if len(problems) != 0 {
				t.Fatal(problems)
			}
			w := watches[0]
			stamp := now
			limit := 7
			if strings.HasPrefix(name, "zero-age") {
				limit = 0
				if name == "zero-age-expired" {
					stamp = now.Add(-time.Second)
				}
			}
			if name == "stale" {
				stamp = now.Add(-8 * 24 * time.Hour)
			}
			if name == "future" {
				stamp = now.Add(time.Second)
			}
			if name == "boundary" {
				stamp = now.Add(-7 * 24 * time.Hour)
			}
			if name == "beyond-boundary" {
				stamp = now.Add(-7*24*time.Hour - time.Second)
			}
			if name == "huge-max-age" {
				limit = int(^uint(0) >> 1)
			}
			base := snapshot.PresenceEntry{BindingHash: plugin.Hash(w), LastSeen: stamp.Add(-time.Hour).Format(time.RFC3339)}
			if name == "baseline-future" {
				base.LastSeen = stamp.Add(time.Second).Format(time.RFC3339)
			}
			if name == "binding-changed" {
				base.BindingHash = strings.Repeat("a", 64)
			}
			if name == "other-account-baseline" {
				w.Instance = "other"
				base.BindingHash = plugin.Hash(w)
			}
			content := "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {domains: []}\n"
			if name == "present" {
				content = strings.Replace(content, "domains: []", "domains: [{name: EXAMPLE.COM.}]", 1)
			}
			if name == "service-absent" {
				content = strings.Replace(content, "domains: []", "cdn: []", 1)
			}
			if name == "service-broken" {
				content = strings.Replace(content, "domains: []", "domains: null", 1)
			}
			attempt := snapshot.Attempt{AttemptedAt: stamp.Format(time.RFC3339), Status: "succeeded", ConfigHash: plugin.Hash(w.Config), SnapshotHash: fmt.Sprintf("%x", sha256.Sum256([]byte(content)))}
			if name == "failed" {
				attempt.Status, attempt.Code, attempt.SnapshotHash = "failed", "http", ""
			}
			if name == "running" {
				attempt.Status, attempt.SnapshotHash = "running", ""
			}
			if name == "config-changed" {
				attempt.ConfigHash = strings.Repeat("a", 64)
			}
			if name == "hash-changed" {
				content += "# changed\n"
			}
			write(t, r.Dir, "state/aliyun/home.yaml", content)
			// 同名资产在另一个账号可见，不能覆盖 home 的缺失。
			write(t, r.Dir, "state/aliyun/other.yaml", "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: other}\nspec: {domains: [{name: example.com}]}\n")
			if name == "no-snapshot" {
				if err := os.Remove(filepath.Join(r.Dir, "state/aliyun/home.yaml")); err != nil {
					t.Fatal(err)
				}
			}
			stampText := stamp.Format(time.RFC3339)
			if name == "time-changed" {
				stampText = stamp.Add(time.Second).Format(time.RFC3339)
			}
			write(t, r.Dir, "state/_sync.yaml", "apiVersion: infrafile/v1alpha1\nkind: Sync\nspec:\n  aliyun:\n    home: '"+stampText+"'\n")
			if name != "legacy" {
				data, err := yaml.Marshal(snapshot.Status{APIVersion: ledger.APIVersion, Kind: "SyncStatus", Spec: map[string]map[string]snapshot.Attempt{"aliyun": {"home": attempt}}})
				if err != nil {
					t.Fatal(err)
				}
				write(t, r.Dir, "state/"+snapshot.StatusFile, string(data))
			}
			if name != "no-baseline" {
				data, err := yaml.Marshal(snapshot.Presence{APIVersion: ledger.APIVersion, Kind: "Presence", Spec: map[string]snapshot.PresenceEntry{w.Target: base}})
				if err != nil {
					t.Fatal(err)
				}
				if name == "baseline-broken" {
					data = []byte("password: synthetic-private-presence-value")
				}
				write(t, r.Dir, "state/"+snapshot.PresenceFile, string(data))
			}
			snaps, _, err := ReadSnapshots(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			in := &Input{Repo: r, Snapshots: snaps, Now: now, MaxAge: &limit}
			err = Prepare(context.Background(), in, []Rule{missing})
			if name == "service-broken" || name == "baseline-broken" {
				if err == nil || strings.Contains(err.Error(), "synthetic-private") {
					t.Fatalf("invalid evidence: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			findings, notices := Run(in, []Rule{missing})
			want := name == "absent" || name == "boundary" || name == "huge-max-age" || name == "zero-age"
			if (len(findings) == 1) != want {
				t.Fatalf("findings=%+v notices=%+v", findings, notices)
			}
			if want {
				if len(notices) != 0 || findings[0].Target != w.Target || findings[0].Evidence["last_seen"] != base.LastSeen || !strings.Contains(findings[0].Message, "不能据此断言已删除") {
					t.Fatalf("finding contract: %+v %+v", findings, notices)
				}
			} else if name == "present" {
				if len(notices) != 0 {
					t.Fatal(notices)
				}
			} else if len(notices) != 1 || notices[0].Code != "missing_unknown" {
				t.Fatalf("unknown required: %+v", notices)
			}
		})
	}
}
