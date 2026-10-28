package plugin

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/infrafile/infrafile/internal/observation"
)

const maxOriginDomains = 200

func collectCDNOrigins(ctx context.Context, auth cloudAuth, entries []cdnEntry, get func(context.Context, cloudAuth, cdnEntry) (*observation.Origin, error)) error {
	if len(entries) > maxOriginDomains {
		return failure("config", "回源采集超过 200 个域名上限")
	}
	for i := range entries {
		if err := ctx.Err(); err != nil {
			return failure("timeout", "回源采集超时或取消")
		}
		origin, err := get(ctx, auth, entries[i])
		if err != nil {
			return err
		}
		origin.Normalize()
		entries[i].Origin = origin
	}
	return nil
}

func aliyunOrigin(ctx context.Context, auth cloudAuth, entry cdnEntry) (*observation.Origin, error) {
	var detail struct {
		Code                 string
		GetDomainDetailModel struct {
			DomainName   string
			SourceModels struct {
				SourceModel []struct {
					Content, Type string
					Port          int
				}
			}
		}
	}
	query := url.Values{"DomainName": {entry.Name}}
	if err := aliyunRequest(ctx, "cdn.aliyuncs.com", "DescribeCdnDomainDetail", "2018-05-10", query, auth, &detail); err != nil {
		return nil, err
	}
	if detail.Code != "" {
		return nil, failure("provider", "阿里云回源详情 API 拒绝请求")
	}
	if !strings.EqualFold(strings.TrimSuffix(detail.GetDomainDetailModel.DomainName, "."), strings.TrimSuffix(entry.Name, ".")) {
		return nil, failure("protocol", "阿里云回源详情域名不匹配")
	}
	o := &observation.Origin{Scope: "global"}
	for _, s := range detail.GetDomainDetailModel.SourceModels.SourceModel {
		o.Sources = append(o.Sources, observation.OriginSource{Host: s.Content, Port: s.Port})
		if s.Type != "ipaddr" && s.Type != "domain" && s.Type != "oss" {
			o.Scope = "unknown"
		}
	}
	var config struct {
		Code          string
		DomainConfigs struct {
			DomainConfig []struct {
				Status, ParentId, FunctionName string
				FunctionArgs                   struct {
					FunctionArg []struct{ ArgName, ArgValue string }
				}
			}
		}
	}
	if err := aliyunRequest(ctx, "cdn.aliyuncs.com", "DescribeCdnDomainConfigs", "2018-05-10", query, auth, &config); err != nil {
		return nil, err
	}
	if config.Code != "" {
		return nil, failure("provider", "阿里云回源配置 API 拒绝请求")
	}
	if config.DomainConfigs.DomainConfig == nil {
		o.Scope = "unknown"
	}
	policies := 0
	for _, c := range config.DomainConfigs.DomainConfig {
		if c.ParentId != "" && c.ParentId != "-1" {
			o.Scope = "conditional"
		}
		if c.FunctionName != "forward_scheme" && c.FunctionName != "ali_origin_port_scheme" {
			// shortcut: 不解释高级回源与边缘脚本；新增映射前先核对其执行范围。
			if strings.Contains(c.FunctionName, "origin") && c.FunctionName != "origin_sni" || strings.Contains(c.FunctionName, "edge") {
				o.Scope = "unknown"
			}
			continue
		}
		args := map[string]string{}
		for _, a := range c.FunctionArgs.FunctionArg {
			args[a.ArgName] = a.ArgValue
		}
		if c.FunctionName == "forward_scheme" && args["enable"] == "off" {
			continue
		}
		policies++
		if c.Status != "success" || policies > 1 {
			o.Scope = "unknown"
		}
		protocol, port := args["scheme"], args["port"]
		if c.FunctionName == "forward_scheme" {
			protocol, port = args["scheme_origin"], args["scheme_origin_port"]
			if args["enable"] != "on" {
				o.Scope = "unknown"
			}
		}
		for i := range o.Sources {
			o.Sources[i].Protocol = observation.OriginProtocol(protocol)
			// 策略可覆盖源站端口；组合端口不能折叠成一个数字。
			o.Sources[i].Port, _ = strconv.Atoi(port)
		}
	}
	return o, nil
}

type tencentOriginConfig struct {
	Origins, BackupOrigins     []string
	OriginPullProtocol         string
	PathBasedOrigin, PathRules []struct{}
}

type tencentOriginDetail struct {
	Origin         *tencentOriginConfig
	SpecificConfig *struct {
		Mainland, Overseas *struct{ Origin *tencentOriginConfig }
	}
	RuleEngine *struct{ Switch string }
}

func (d tencentOriginDetail) observation() *observation.Origin {
	o := &observation.Origin{Scope: "global"}
	add := func(c *tencentOriginConfig) {
		if c == nil {
			return
		}
		if len(c.PathBasedOrigin)+len(c.PathRules) > 0 {
			o.Scope = "conditional"
		}
		for _, list := range [][]string{c.Origins, c.BackupOrigins} {
			for _, value := range list {
				host, port, ok := tencentOriginAddress(value)
				if !ok {
					o.Scope = "unknown"
				}
				o.Sources = append(o.Sources, observation.OriginSource{Host: host, Port: port, Protocol: c.OriginPullProtocol})
			}
		}
	}
	add(d.Origin)
	if d.Origin == nil || d.Origin.Origins == nil {
		o.Scope = "unknown"
	}
	if d.SpecificConfig != nil {
		for _, region := range []*struct{ Origin *tencentOriginConfig }{d.SpecificConfig.Mainland, d.SpecificConfig.Overseas} {
			if region != nil && region.Origin != nil {
				add(region.Origin)
				o.Scope = "conditional"
			}
		}
	}
	if d.RuleEngine != nil && d.RuleEngine.Switch != "off" {
		o.Scope = "conditional"
	}
	o.Normalize()
	return o
}

func tencentOriginAddress(value string) (string, int, bool) {
	if ip, err := netip.ParseAddr(value); err == nil && ip.Zone() == "" {
		return ip.Unmap().String(), 0, true
	}
	// shortcut: 带端口的 IPv6 需厂商明确括号语法后再支持，不能按冒号拆错地址。
	parts := strings.Split(value, ":")
	if len(parts) > 3 {
		return "", 0, false
	}
	host, ok := observation.OriginHost(parts[0])
	port := 0
	if len(parts) > 1 && parts[1] != "" {
		var err error
		port, err = strconv.Atoi(parts[1])
		ok = ok && err == nil && port > 0 && port <= 65535
	}
	if len(parts) == 3 {
		weight, err := strconv.Atoi(parts[2])
		ok = ok && err == nil && weight >= 1 && weight <= 100
	}
	if !ok {
		return "", 0, false
	}
	return host, port, true
}

func baiduOrigin(ctx context.Context, auth cloudAuth, entry cdnEntry) (*observation.Origin, error) {
	// 固定 API 路径不接受清单中的路径分隔符或 URL。
	if !Hostname(strings.TrimPrefix(entry.Name, "*.")) {
		return nil, failure("protocol", "百度回源域名无效")
	}
	var response struct {
		Code         string `json:"code"`
		OriginConfig []struct {
			Addr, UpstreamProtocol, Isp string
			HTTPPort                    int `json:"httpPort"`
			HTTPSPort                   int `json:"httpsPort"`
		} `json:"originConfig"`
	}
	path := "/v2/domain/" + entry.Name + "/config"
	if err := baiduRequest(ctx, "cdn.baidubce.com", path, url.Values{"originConfig": {""}}, auth, &response); err != nil {
		return nil, err
	}
	if response.Code != "" {
		return nil, failure("provider", "百度回源配置 API 拒绝请求")
	}
	o := &observation.Origin{Scope: "global"}
	for _, s := range response.OriginConfig {
		protocol, port := s.UpstreamProtocol, 0
		switch protocol {
		case "http":
			port = s.HTTPPort
		case "https":
			port = s.HTTPSPort
		case "*":
			protocol = "follow"
			if s.HTTPPort == s.HTTPSPort {
				port = s.HTTPPort
			}
		}
		if s.Isp != "" {
			o.Scope = "conditional"
		}
		o.Sources = append(o.Sources, observation.OriginSource{Host: s.Addr, Port: port, Protocol: protocol})
	}
	var config struct {
		Code            string     `json:"code"`
		Domain          string     `json:"domain"`
		PageRules       []struct{} `json:"pageRules_originConfig"`
		LegacyPageRules []struct{} `json:"pageRules_origins"`
	}
	if err := baiduRequest(ctx, "cdn.baidubce.com", path, url.Values{}, auth, &config); err != nil {
		return nil, err
	}
	if config.Code != "" {
		return nil, failure("provider", "百度域名配置 API 拒绝请求")
	}
	if config.Domain != entry.Name {
		return nil, failure("protocol", "百度回源详情域名不匹配")
	}
	if len(config.PageRules)+len(config.LegacyPageRules) > 0 {
		o.Scope = "conditional"
	}
	return o, nil
}

func ctyunOrigin(ctx context.Context, auth cloudAuth, entry cdnEntry) (*observation.Origin, error) {
	var response struct {
		Code     int    `json:"code"`
		Domain   string `json:"domain"`
		Product  string `json:"product_code"`
		Area     int    `json:"area_scope"`
		Protocol string `json:"origin_protocol"`
		Origin   []struct {
			Origin string
			Port   int
		} `json:"origin"`
		XOS   *int `json:"xos_origin_is"`
		HTTPS struct {
			Protocol string `json:"origin_protocol"`
		} `json:"https_basic"`
		Basic struct {
			HTTPPort  int `json:"http_origin_port"`
			HTTPSPort int `json:"https_origin_port"`
		} `json:"basic_conf"`
	}
	query := url.Values{"domain": {entry.Name}, "product_code": {entry.Type}}
	if err := ctyunRequest(ctx, "/domain/query-domain-detail", query, auth, &response); err != nil {
		return nil, err
	}
	if response.Code != 100000 {
		return nil, failure("provider", "天翼回源详情 API 拒绝请求")
	}
	if response.Domain != entry.Name || response.Product != entry.Type {
		return nil, failure("protocol", "天翼回源详情域名或产品不匹配")
	}
	o := &observation.Origin{Scope: "global"}
	if entry.Area == "" || map[int]string{1: "domestic", 2: "overseas", 3: "global"}[response.Area] != entry.Area {
		o.Scope = "unknown"
	}
	if response.XOS == nil || *response.XOS != 0 {
		o.Scope = "unknown"
	}
	protocol := response.Protocol
	if protocol != response.HTTPS.Protocol {
		protocol = ""
	}
	if protocol == "follow_request" {
		protocol = "follow"
	}
	for _, s := range response.Origin {
		port := s.Port
		if response.Basic.HTTPPort != 0 && response.Basic.HTTPPort != port || response.Basic.HTTPSPort != 0 && response.Basic.HTTPSPort != port {
			port = 0
		}
		o.Sources = append(o.Sources, observation.OriginSource{Host: s.Origin, Port: port, Protocol: protocol})
	}
	return o, nil
}

func cloudflareOrigin(ctx context.Context, zoneID string, headers http.Header) (*observation.Origin, error) {
	var response struct {
		Success bool `json:"success"`
		Result  *struct {
			Kind, Phase string
			Rules       []struct {
				Action, Expression string
				Enabled            *bool
				Parameters         struct {
					Origin struct {
						Host string
						Port int
					}
				} `json:"action_parameters"`
			}
		} `json:"result"`
	}
	endpoint := "https://api.cloudflare.com/client/v4/zones/" + url.PathEscape(zoneID) + "/rulesets/phases/http_request_origin/entrypoint"
	if err := requestJSON(ctx, "GET", endpoint, headers, nil, &response); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, failure("provider", "Cloudflare Origin Rules API 拒绝请求")
	}
	r := response.Result
	if r == nil || r.Kind != "zone" || r.Phase != "http_request_origin" || r.Rules == nil {
		return nil, failure("protocol", "Cloudflare Origin Rules 响应不完整")
	}
	o := &observation.Origin{Scope: "global"}
	active := 0
	for _, rule := range r.Rules {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}
		active++
		if rule.Enabled == nil || rule.Action != "route" || strings.TrimSpace(rule.Expression) != "true" || active > 1 {
			o.Scope = "conditional"
		}
		o.Sources = append(o.Sources, observation.OriginSource{Host: rule.Parameters.Origin.Host, Port: rule.Parameters.Origin.Port})
	}
	o.Normalize()
	return o, nil
}
