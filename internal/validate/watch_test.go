package validate

import (
	"fmt"
	"strings"
	"testing"
)

func TestWatchScope(t *testing.T) {
	for _, tc := range []struct {
		name, plugin, service, config, target, spec string
		valid                                       bool
	}{
		{"domain-default", "aliyun", "domains", "{}", "Domain/example.com", "{}", true},
		{"domain-selected", "tencent", "domains", "{services: [domains]}", "Domain/example.com", "{}", true},
		{"cdn", "baidu", "cdn", "{services: [cdn]}", "Resource/edge", "{provider: baidu, type: cdn, endpoint: example.com}", true},
		{"ctyun", "ctyun", "cdn", "{}", "Resource/edge", "{provider: ctyun, type: cdn, resource_id: item}", true},
		{"cf-domain", "cloudflare", "zones", "{}", "Domain/example.com", "{}", true},
		{"cf-resource", "cloudflare", "zones", "{}", "Resource/edge", "{provider: cloudflare, type: dns-zone, resource_id: zone}", true},
		{"disabled-service", "aliyun", "domains", "{services: [cdn]}", "Domain/example.com", "{}", false},
		{"empty-services", "aliyun", "domains", "{services: []}", "Domain/example.com", "{}", false},
		{"bad-services", "aliyun", "domains", "{services: wrong}", "Domain/example.com", "{}", false},
		{"null-default", "aliyun", "domains", "{services: null}", "Domain/example.com", "{}", true},
		{"unknown-service", "aliyun", "domains", "{services: [domains, unknown]}", "Domain/example.com", "{}", false},
		{"duplicate-service", "aliyun", "domains", "{services: [domains, domains]}", "Domain/example.com", "{}", false},
		{"unsupported", "ctyun", "domains", "{}", "Domain/example.com", "{}", false},
		{"probe", "whois", "found", "{}", "Domain/example.com", "{}", false},
		{"domain-cdn", "aliyun", "cdn", "{}", "Domain/example.com", "{}", false},
		{"provider", "aliyun", "cdn", "{}", "Resource/edge", "{provider: baidu, type: cdn, endpoint: example.com}", false},
		{"no-identity", "aliyun", "cdn", "{}", "Resource/edge", "{provider: aliyun, type: cdn}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, name, _ := strings.Cut(tc.target, "/")
			dir := "domains"
			if kind == "Resource" {
				dir = "resources"
			}
			config := fmt.Sprintf("apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n  %s:\n    instances:\n      home: %s\naudit:\n  watch:\n    - {target: '%s', plugin: %s, instance: home, service: %s}\n", tc.plugin, tc.config, tc.target, tc.plugin, tc.service)
			files := map[string]string{"infrafile.yaml": config, "inventory/" + dir + "/" + name + ".yaml": fmt.Sprintf("apiVersion: infrafile/v1alpha1\nkind: %s\nmetadata: {name: %s}\nspec: %s\n", kind, name, tc.spec)}
			got := run(t, newRepo(t, files), Options{})
			if (len(got) == 0) != tc.valid {
				t.Fatalf("valid=%v problems=%+v", tc.valid, got)
			}
		})
	}
}

func TestWatchTargetValidation(t *testing.T) {
	base := "apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n  aliyun:\n    instances:\n      home: {}\naudit:\n  watch:\n    - {target: Domain/example.com, plugin: aliyun, instance: home, service: domains}\n"
	record := "apiVersion: infrafile/v1alpha1\nkind: Domain\nmetadata: {name: example.com}\nspec: {}\n"
	for _, name := range []string{"missing", "archived", "disabled", "duplicate", "extra", "partial", "raw-address"} {
		t.Run(name, func(t *testing.T) {
			config, asset := base, record
			switch name {
			case "missing":
				config = strings.Replace(config, "Domain/example.com", "Domain/absent.example.com", 1)
			case "archived":
				asset = strings.Replace(asset, "name: example.com}", "name: example.com, archived: {date: 2026-10-28, reason: retired}}", 1)
			case "disabled":
				config = strings.Replace(config, "instance: home", "instance: other", 1)
			case "duplicate":
				config += "    - {target: Domain/example.com, plugin: aliyun, instance: home, service: domains}\n"
			case "extra":
				config = strings.Replace(config, "service: domains}", "service: domains, guessed: true}", 1)
			case "partial":
				config = strings.Replace(config, ", service: domains", "", 1)
			case "raw-address":
				config = strings.Replace(config, "Domain/example.com", "example.com", 1)
			}
			if got := run(t, newRepo(t, map[string]string{"infrafile.yaml": config, "inventory/domains/example.com.yaml": asset}), Options{}); len(got) == 0 {
				t.Fatal("accepted invalid watch")
			}
		})
	}
}
