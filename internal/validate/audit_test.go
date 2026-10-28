package validate

import "testing"

func TestLifecycleIgnoreSchema(t *testing.T) {
	for _, tc := range []struct {
		name, rule, match string
		valid             bool
	}{
		{"domain", "orphan", "{kind: Domain, name: example.org}", true},
		{"server", "zombie", "{kind: Server, address: '2001:db8::1'}", true},
		{"resource_id", "orphan", "{kind: Resource, provider: aliyun, resource_id: same}", true},
		{"resource_endpoint", "zombie", "{kind: Resource, provider: baidu, type: cdn, endpoint: static.example.com}", true},
		{"instance_stale", "stale_state", "", true},
		{"instance_lifecycle", "zombie", "", false},
		{"partial", "orphan", "{kind: Resource, provider: aliyun}", false},
		{"mixed_identity", "orphan", "{kind: Resource, provider: aliyun, resource_id: same, type: cdn, endpoint: static.example.com}", false},
		{"extra", "orphan", "{kind: Domain, name: example.org, command: ignored}", false},
		{"empty_id", "orphan", "{kind: Resource, provider: aliyun, resource_id: ''}", false},
		{"stale_match", "stale_state", "{kind: Domain, name: example.org}", false},
		{"planned", "origin_exposure", "{kind: Domain, name: example.org}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := "apiVersion: infrafile/v1alpha1\nkind: Config\naudit:\n  ignore:\n    - rule: " + tc.rule + "\n      plugin: aliyun\n      instance: home\n      reason: 测试精确豁免\n"
			if tc.match != "" {
				config += "      match: " + tc.match + "\n"
			}
			problems := run(t, newRepo(t, map[string]string{"infrafile.yaml": config}), Options{})
			if (len(problems) == 0) != tc.valid {
				t.Fatalf("valid=%v problems=%+v", tc.valid, problems)
			}
		})
	}
}
