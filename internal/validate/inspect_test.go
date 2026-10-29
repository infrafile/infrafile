package validate

import (
	"strings"
	"testing"
)

func TestInspectNAContract(t *testing.T) {
	entry := "{check: '" + strings.Repeat("a", 64) + "', reason: reviewed, owner: maintainer, review_by: '2026-12-31'}"
	for _, tc := range []struct {
		name, marker, list string
		valid              bool
	}{
		{"valid", "storageVersion: v1\n", entry, true},
		{"no-marker", "", entry, false},
		{"duplicate", "storageVersion: v1\n", entry + ", " + entry, false},
		{"no-owner", "storageVersion: v1\n", strings.ReplaceAll(entry, "owner: maintainer, ", ""), false},
		{"empty-reason", "storageVersion: v1\n", strings.ReplaceAll(entry, "reason: reviewed", "reason: ' '"), false},
		{"bad-date", "storageVersion: v1\n", strings.ReplaceAll(entry, "2026-12-31", "2026-02-30"), false},
		{"unknown-field", "storageVersion: v1\n", strings.ReplaceAll(entry, "reason: reviewed", "reason: reviewed, apply: true"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := "apiVersion: infrafile/v1alpha1\nkind: Config\n" + tc.marker + "audit:\n  not_applicable: [" + tc.list + "]\n"
			problems := run(t, newRepo(t, map[string]string{"infrafile.yaml": cfg}), Options{})
			if (len(problems) == 0) != tc.valid {
				t.Fatalf("valid=%t problems=%+v", tc.valid, problems)
			}
		})
	}
}
