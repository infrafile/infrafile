package observation

import (
	"net/netip"
	"strings"
)

// Requirement 声明生命周期规则理解的插件和服务，不包含 DNS/TLS 探测。
type Requirement struct {
	Plugin   string
	Services []string
}

func Requirements() []Requirement {
	return []Requirement{
		{"aliyun", []string{"domains", "cdn"}},
		{"tencent", []string{"domains", "cdn"}},
		{"baidu", []string{"domains", "cdn"}},
		{"ctyun", []string{"cdn"}},
		{"cloudflare", []string{"zones"}},
		{"whois", []string{"found"}},
		{"ssh", []string{"host"}},
	}
}

// Identity 是用于精确匹配的自然身份。Resource 有 ID 时仅使用 provider + resource_id。
type Identity struct {
	Kind       string `json:"kind"`
	Name       string `json:"name,omitempty"`
	Provider   string `json:"provider,omitempty"`
	ResourceID string `json:"resource_id,omitempty"`
	Type       string `json:"type,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
	Address    string `json:"address,omitempty"`
}

func hostname(value string) string { return strings.TrimSuffix(strings.ToLower(value), ".") }

func resourceEndpoint(provider, kind, value string) string {
	value = hostname(value)
	// 阿里云 CDN 的 .example.com 是泛域名，统一成 *.example.com，保留根域名区别。
	if provider == "aliyun" && kind == "cdn" && strings.HasPrefix(value, ".") {
		value = "*" + value
	}
	return value
}

func (id Identity) Normalized() Identity {
	id.Name = hostname(id.Name)
	id.Endpoint = resourceEndpoint(id.Provider, id.Type, id.Endpoint)
	if addr, err := netip.ParseAddr(id.Address); err == nil {
		id.Address = addr.Unmap().String()
	} else {
		id.Address = hostname(id.Address)
	}
	return id
}

func IdentityOf(kind, name string, spec map[string]any) Identity {
	get := func(key string) string { v, _ := spec[key].(string); return v }
	id := Identity{Kind: kind}
	switch kind {
	case "Domain":
		id.Name = name
	case "Server":
		id.Address = get("address")
	case "Resource":
		id.Provider, id.ResourceID = get("provider"), get("resource_id")
		if id.ResourceID == "" {
			return EndpointIdentity(spec)
		}
	}
	return id.Normalized()
}

// EndpointIdentity 供无 ID 的观测查询台账；台账即使有 ID，也能按 endpoint 被这种观测覆盖。
func EndpointIdentity(spec map[string]any) Identity {
	provider, _ := spec["provider"].(string)
	typ, _ := spec["type"].(string)
	endpoint, _ := spec["endpoint"].(string)
	return (Identity{Kind: "Resource", Provider: provider, Type: typ, Endpoint: endpoint}).Normalized()
}

func (id Identity) String() string {
	switch id.Kind {
	case "Domain":
		return "Domain/" + id.Name
	case "Server":
		return "Server/" + id.Address
	case "Resource":
		if id.ResourceID != "" {
			return "Resource/" + id.Provider + "/id/" + id.ResourceID
		}
		return "Resource/" + id.Provider + "/" + id.Type + "/" + id.Endpoint
	}
	return id.Kind
}
