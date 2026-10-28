package observation

import (
	"net/netip"
	"slices"
	"strings"
)

// Origin 只保存可公开的回源地址；空字段表示未观测到，不推断厂商默认值。
type Origin struct {
	Scope   string         `json:"scope"`
	Sources []OriginSource `json:"sources"`
}

type OriginSource struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

type ExpectedOrigin struct {
	Hosts    []string `json:"hosts"`
	Port     int      `json:"port,omitempty"`
	Protocol string   `json:"protocol,omitempty"`
}

func OriginHost(value string) (string, bool) {
	if ip, err := netip.ParseAddr(value); err == nil {
		return ip.Unmap().String(), ip.Zone() == ""
	}
	host, ok := domainName(value)
	return host, ok && !strings.Contains(host, "*")
}

func OriginProtocol(value string) string {
	if value == "http" || value == "https" || value == "follow" {
		return value
	}
	return ""
}

func (o *Origin) Normalize() {
	if o.Scope != "global" && o.Scope != "conditional" {
		o.Scope = "unknown"
	}
	if len(o.Sources) == 0 {
		o.Scope = "unknown"
	}
	for i := range o.Sources {
		s := &o.Sources[i]
		host, ok := OriginHost(s.Host)
		if !ok {
			o.Scope = "unknown"
			s.Host = "" // 不保留可能含 userinfo、路径或鉴权参数的原值。
		} else {
			s.Host = host
		}
		if s.Port < 1 || s.Port > 65535 {
			s.Port = 0
		}
		s.Protocol = OriginProtocol(s.Protocol)
	}
	slices.SortFunc(o.Sources, func(a, b OriginSource) int {
		if n := strings.Compare(a.Host, b.Host); n != 0 {
			return n
		}
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		return strings.Compare(a.Protocol, b.Protocol)
	})
	o.Sources = slices.Compact(o.Sources)
	if o.Sources == nil {
		o.Sources = []OriginSource{}
	}
}
