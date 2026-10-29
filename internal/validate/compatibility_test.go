package validate

import "testing"

func TestStorageVersion(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		valid       bool
	}{
		{"legacy", "", true},
		{"v1", "storageVersion: v1\n", true},
		{"future", "storageVersion: v2\n", false},
		{"number", "storageVersion: 1\n", false},
		{"null", "storageVersion: null\n", false},
		{"duplicate", "storageVersion: v1\nstorageVersion: v1\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t, map[string]string{"infrafile.yaml": "apiVersion: infrafile/v1alpha1\nkind: Config\n" + tc.field})
			problems := run(t, dir, Options{})
			if (len(problems) == 0) != tc.valid {
				t.Fatalf("valid=%v problems=%v", tc.valid, problems)
			}
		})
	}
}
