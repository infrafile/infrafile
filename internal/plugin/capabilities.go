package plugin

import "github.com/infrafile/infrafile/internal/ledger"

// Support describes implementation scope, not the current account's permissions or health.
type Support string

const (
	Supported   Support = "supported"
	Conditional Support = "conditional"
	Unsupported Support = "unsupported"
	Unknown     Support = "unknown"
)

type Stages struct {
	Register Support `json:"register"`
	Observe  Support `json:"observe"`
	Compare  Support `json:"compare"`
	Plan     Support `json:"plan"`
	Execute  Support `json:"execute"`
	Readback Support `json:"readback"`
}

type ServiceCapability struct {
	Service      string   `json:"service"`
	Action       string   `json:"action,omitempty"`
	Fields       []string `json:"fields"`
	Stages       Stages   `json:"stages"`
	Permissions  []string `json:"permissions"`
	Conditions   []string `json:"conditions"`
	Verification string   `json:"verification"`
}

type PluginCapability struct {
	Plugin   string              `json:"plugin"`
	Methods  []string            `json:"methods"`
	Services []ServiceCapability `json:"services"`
}

type CapabilityCatalog struct {
	Kinds           []string           `json:"kinds"`
	Plugins         []PluginCapability `json:"plugins"`
	ExternalDetails Support            `json:"external_details"`
}

// Capabilities returns a fresh catalog without opening plugins, a ledger or credentials.
func Capabilities() CapabilityCatalog {
	kinds := make([]string, 0, len(ledger.Kinds))
	for _, kind := range ledger.Kinds {
		kinds = append(kinds, kind.Name)
	}
	cloud := func(name string, domains bool) PluginCapability {
		services := []ServiceCapability{}
		if domains {
			services = append(services, observationCapability("domains", []string{"name", "expires_at"},
				"注册域名列表只读权限，精确权限见第一方插件指南", "缺少字段或权限时不能证明清单完整；missing 需要 watch、新鲜成功回执和正向基线"))
		}
		cdnFields := []string{"name", "status", "cname"}
		if name == "baidu" {
			cdnFields = []string{"name"}
		}
		services = append(services, observationCapability("cdn", cdnFields,
			"CDN 清单只读权限，精确权限见第一方插件指南", "状态值按厂商映射；不识别的状态不判正常"))
		origin := observationCapability("cdn-origin", []string{"hosts", "port", "protocol"},
			"额外的 CDN 回源详情只读权限", "显式开启 collect_origin；仅比较显式期望；条件/区域策略和不可观测字段为 unknown")
		origin.Stages.Register = Unsupported
		services = append(services, origin)
		if !domains {
			services[0].Permissions = []string{"天翼 API 账号与 CDN 只读访问；精确最小 IAM 权限尚未确认"}
			services[1].Permissions = []string{"额外的天翼 CDN 回源详情只读访问；精确最小 IAM 权限尚未确认"}
			services = append(services, ServiceCapability{Service: "domains", Fields: []string{}, Stages: noStages(),
				Permissions: []string{}, Conditions: []string{"没有已支持的注册域名列表接口"}, Verification: "unverified"})
		}
		return PluginCapability{Plugin: name, Methods: []string{"sync"}, Services: services}
	}
	cf := PluginCapability{Plugin: "cloudflare", Methods: []string{"sync", "probe", "control"}, Services: []ServiceCapability{
		observationCapability("zones", []string{"id", "name"}, "Zone Read", "zone 不是注册域名；missing 需要显式范围和存在基线"),
		observationCapability("dns", []string{"type", "name", "content", "proxied"}, "DNS Read", "不导入 DNS 记录为新资产；DNS 写入交由外部 DNSControl"),
		observationCapability("cdn-origin", []string{"hosts", "port"}, "Origin Read 与对应功能授权", "显式 collect_origin；只比较单条启用、无条件规则；协议不可观测；复杂规则、Host header、SNI 不在比较范围"),
	}}
	cf.Services[1].Stages.Register = Unsupported
	cf.Services[2].Stages.Register = Unsupported
	for _, action := range []string{"cdn-pause", "cdn-resume", "cdn-origin"} {
		fields, permission, condition := []string{"paused"}, "Zone Read / Zone Write", "作用于整个 zone，可能影响所有代理流量；不是单个主机名操作"
		if action == "cdn-origin" {
			fields, permission, condition = []string{"origin.host"}, "Zone Read / Origin Read / Origin Write 与对应功能授权", "仅已有启用 Origin Rule 的主机覆盖；权限与套餐有条件；不修改端口、协议、Host header、SNI、DNS 或匹配条件"
		}
		stages := noStages()
		stages.Observe, stages.Plan, stages.Execute, stages.Readback = Conditional, Conditional, Conditional, Conditional
		cf.Services = append(cf.Services, ServiceCapability{Service: "control", Action: action, Fields: fields, Stages: stages,
			Permissions: []string{permission}, Conditions: []string{condition, "单动作；执行前重读，执行后回读；配置接受不等于业务健康；没有服务端原子 CAS"}, Verification: "automated-only"})
	}
	dns := observationCapability("dns", []string{"addresses", "canonical_name"}, "无云权限；系统 DNS 解析器可达", "只查询 A/AAAA/CNAME；fake-IP 出口不能证明公网解析；不是完整 DNS 导出")
	dns.Stages.Register = Unsupported
	tls := observationCapability("tls", []string{"not_after", "verified", "verification"}, "目标 TLS 端口可达", "仅观测服务证书；不枚举证书资产、不做资产 missing、不签发或部署；sync 成功不等于信任通过")
	tls.Stages.Register, tls.Stages.Compare = Unsupported, Unsupported
	whois := observationCapability("found", []string{"found", "registrar", "expires_at"}, "WHOIS TCP 43 可达", "字段取决于公开响应；没有 RDAP 回退；不能证明云账号清单或资产 missing")
	ssh := observationCapability("host", []string{"host", "system", "release", "architecture"}, "普通 SSH 账号、可信主机密钥与非交互认证", "只运行固定 uname -srm；不证明应用健康，不提供任意命令或资产 missing")
	return CapabilityCatalog{Kinds: kinds, ExternalDetails: Unknown, Plugins: []PluginCapability{
		cloud("aliyun", true), cloud("baidu", true), cf, cloud("ctyun", false),
		{Plugin: "dns", Methods: []string{"sync"}, Services: []ServiceCapability{dns}},
		{Plugin: "ssh", Methods: []string{"sync"}, Services: []ServiceCapability{ssh}},
		cloud("tencent", true),
		{Plugin: "tls", Methods: []string{"sync"}, Services: []ServiceCapability{tls}},
		{Plugin: "whois", Methods: []string{"sync"}, Services: []ServiceCapability{whois}},
	}}
}

func observationCapability(service string, fields []string, permission, condition string) ServiceCapability {
	stages := noStages()
	stages.Register, stages.Observe, stages.Compare = Conditional, Conditional, Conditional
	return ServiceCapability{Service: service, Fields: fields, Stages: stages, Permissions: []string{permission},
		Conditions: []string{condition, "字段可能省略；缺失或不可观测字段不能当作正常证据"}, Verification: "automated-only"}
}

func noStages() Stages {
	return Stages{Unsupported, Unsupported, Unsupported, Unsupported, Unsupported, Unsupported}
}

func builtinCapability(name string) (PluginCapability, bool) {
	for _, capability := range Capabilities().Plugins {
		if capability.Plugin == name {
			return capability, true
		}
	}
	return PluginCapability{}, false
}

func supportsAction(name, action string) bool {
	capability, _ := builtinCapability(name)
	for _, service := range capability.Services {
		if service.Action == action && service.Stages.Execute != Unsupported {
			return true
		}
	}
	return false
}
