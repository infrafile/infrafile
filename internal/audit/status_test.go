package audit

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/snapshot"
	"go.yaml.in/yaml/v3"
)

func TestSyncExecutionNotices(t *testing.T) {
	config := "plugins:\n  example:\n    instances:\n      home: {}\n"
	for _, tc := range []struct{ name, want string }{
		{"success", ""}, {"failure", "sync_failed"}, {"first-failure", "sync_failed"},
		{"running", "sync_incomplete"}, {"config-changed", "sync_status_unverified"},
		{"snapshot-changed", "sync_status_unverified"}, {"time-missing", "sync_status_unverified"},
		{"newer-legacy-sync", "sync_status_unverified"}, {"receipt-missing", "sync_status_unverified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := snapshotYAML("home")
			files := map[string]string{
				"state/example/home.yaml": content,
				"state/_sync.yaml":        "apiVersion: infrafile/v1alpha1\nkind: Sync\nspec: {example: {home: '2026-12-01T12:00:00Z'}}\n",
			}
			attempt := snapshot.Attempt{AttemptedAt: "2026-12-01T12:00:00Z", Status: "succeeded", ConfigHash: plugin.Hash(map[string]any{}), SnapshotHash: fmt.Sprintf("%x", sha256.Sum256([]byte(content)))}
			switch tc.name {
			case "failure", "first-failure":
				attempt.Status, attempt.Code, attempt.SnapshotHash = "failed", "timeout", ""
				if tc.name == "first-failure" {
					delete(files, "state/example/home.yaml")
					delete(files, "state/_sync.yaml")
				}
			case "running":
				attempt.Status, attempt.SnapshotHash = "running", ""
			case "config-changed":
				attempt.ConfigHash = strings.Repeat("a", 64)
			case "snapshot-changed":
				files["state/example/home.yaml"] += "# manually changed\n"
			case "time-missing":
				delete(files, "state/_sync.yaml")
			case "newer-legacy-sync":
				attempt.Status, attempt.Code, attempt.SnapshotHash = "failed", "timeout", ""
				files["state/_sync.yaml"] = strings.Replace(files["state/_sync.yaml"], "12:00:00", "13:00:00", 1)
			}
			instances := map[string]snapshot.Attempt{"home": attempt}
			if tc.name == "receipt-missing" {
				instances = map[string]snapshot.Attempt{"disabled": attempt}
			}
			data, err := yaml.Marshal(snapshot.Status{APIVersion: ledger.APIVersion, Kind: "SyncStatus", Spec: map[string]map[string]snapshot.Attempt{"example": instances}})
			if err != nil {
				t.Fatal(err)
			}
			files["state/"+snapshot.StatusFile] = string(data)
			_, notices, err := ReadSnapshots(context.Background(), repo(t, config, files))
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" && len(notices) != 0 || tc.want != "" && (len(notices) != 1 || notices[0].Code != tc.want || notices[0].Target != "example/home") {
				t.Fatalf("notices = %+v, want %s", notices, tc.want)
			}
		})
	}
}

func TestAuditRejectsBrokenStatus(t *testing.T) {
	r := repo(t, "", map[string]string{"state/" + snapshot.StatusFile: "password: synthetic-private-status-value\n"})
	_, _, err := ReadSnapshots(context.Background(), r)
	if err == nil || strings.Contains(err.Error(), "synthetic-private-status-value") {
		t.Fatalf("status parsing failure: %v", err)
	}
}
