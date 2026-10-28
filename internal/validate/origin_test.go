package validate

import "testing"

func TestResourceOrigin(t *testing.T) {
	for _, tc := range []struct {
		spec  string
		valid bool
	}{
		{`{provider: aliyun, type: cdn, origin: {hosts: [ORIGIN.EXAMPLE.COM., '2001:db8::1'], port: 8443, protocol: follow}}`, true},
		{`{provider: cloudflare, type: dns-zone, origin: {hosts: [origin.example.com]}}`, true},
		{`{provider: aliyun, type: bucket, origin: {hosts: [origin.example.com]}}`, false},
		{`{provider: tencent, type: dns-zone, origin: {hosts: [origin.example.com]}}`, false},
		{`{provider: aliyun, type: cdn, origin: {hosts: []}}`, false},
		{`{provider: aliyun, type: cdn, origin: {hosts: ['https://example.com/path']}}`, false},
		{`{provider: aliyun, type: cdn, origin: {hosts: ['*.example.com']}}`, false},
		{`{provider: aliyun, type: cdn, origin: {hosts: ['example.com:443']}}`, false},
		{`{provider: aliyun, type: cdn, origin: {hosts: ['fe80::1%eth0']}}`, false},
		{`{provider: aliyun, type: cdn, origin: {hosts: [example.com], port: 65536}}`, false},
		{`{provider: aliyun, type: cdn, origin: {hosts: [example.com], protocol: ftp}}`, false},
		{`{provider: aliyun, type: cdn, origin: {hosts: [example.com], token: something}}`, false},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			dir := newRepo(t, map[string]string{"inventory/resources/edge.yaml": "apiVersion: infrafile/v1alpha1\nkind: Resource\nmetadata: {name: edge}\nspec: " + tc.spec + "\n"})
			problems := run(t, dir, Options{})
			if (len(problems) == 0) != tc.valid {
				t.Fatalf("problems: %+v", problems)
			}
		})
	}
}
