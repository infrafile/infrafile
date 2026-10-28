package observation

import (
	"errors"
	"net/netip"
	"strings"
)

// AddressRecord 是地址观测，独立于生命周期的资产映射；Proxied=nil 表示未声明。
type AddressRecord struct {
	Host    string
	Type    string
	Address netip.Addr
	Proxied *bool
}

// AddressRecords 只解释 DNS / Cloudflare 的地址字段，不解析 CNAME、不访问网络。
func AddressRecords(plugin string, spec map[string]any) ([]AddressRecord, error) {
	result := []AddressRecord{}
	invalid := errors.New("DNS 地址观测字段缺失、类型错误或地址无效")
	if plugin == "dns" {
		host, ok := spec["host"].(string)
		if !ok || host == "" {
			return nil, invalid
		}
		addresses, ok := spec["addresses"].([]any)
		if !ok {
			return nil, invalid
		}
		for _, value := range addresses {
			raw, ok := value.(string)
			if !ok {
				return nil, invalid
			}
			addr, err := netip.ParseAddr(raw)
			if err != nil {
				return nil, invalid
			}
			addr = addr.Unmap()
			typ := "AAAA"
			if addr.Is4() {
				typ = "A"
			}
			result = append(result, AddressRecord{Host: hostname(host), Type: typ, Address: addr})
		}
		return result, nil
	}
	if plugin != "cloudflare" {
		return result, nil
	}
	zones, ok := spec["zones"].([]any)
	if !ok {
		return nil, invalid
	}
	for _, value := range zones {
		zone, ok := value.(map[string]any)
		if !ok {
			return nil, invalid
		}
		records, ok := zone["dns_records"].([]any)
		if !ok {
			return nil, invalid
		}
		for _, value := range records {
			record, ok := value.(map[string]any)
			if !ok {
				return nil, invalid
			}
			typ, ok := record["type"].(string)
			if !ok {
				return nil, invalid
			}
			typ = strings.ToUpper(typ)
			if typ != "A" && typ != "AAAA" {
				continue
			}
			host, ok := record["name"].(string)
			if !ok || host == "" {
				return nil, invalid
			}
			raw, ok := record["content"].(string)
			if !ok {
				return nil, invalid
			}
			addr, err := netip.ParseAddr(raw)
			if err != nil || typ == "A" && !addr.Is4() || typ == "AAAA" && !addr.Is6() {
				return nil, invalid
			}
			entry := AddressRecord{Host: hostname(host), Type: typ, Address: addr.Unmap()}
			if value, exists := record["proxied"]; exists {
				proxied, ok := value.(bool)
				if !ok {
					return nil, invalid
				}
				entry.Proxied = &proxied
			}
			result = append(result, entry)
		}
	}
	return result, nil
}
