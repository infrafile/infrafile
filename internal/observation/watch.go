package observation

import (
	"fmt"
	"slices"

	"github.com/infrafile/infrafile/internal/ledger"
)

// Watch 的 JSON 是基线绑定：包含两种匹配身份和配置引用，不包含台账其他字段。
type Watch struct {
	Target   string
	Plugin   string
	Instance string
	Service  string
	Identity Identity
	Endpoint Identity
	Config   map[string]any
	Record   *ledger.Record `json:"-"`
}

// Watches 校验显式范围，不从厂商名或台账 account 推断插件实例。
func Watches(repo *ledger.Repo) ([]Watch, []ledger.Problem) {
	if repo.Config == nil {
		return nil, nil
	}
	config, _ := repo.Config.Value.(map[string]any)
	audit, _ := config["audit"].(map[string]any)
	items, _ := audit["watch"].([]any)
	plugins, _ := config["plugins"].(map[string]any)
	index := map[string]*ledger.Record{}
	for _, r := range repo.Records {
		index[r.Kind+"/"+r.Name] = r
	}
	watches := []Watch{}
	problems := []ledger.Problem{}
	seen := map[string]bool{}
	for i, item := range items {
		m, _ := item.(map[string]any)
		get := func(key string) string { s, _ := m[key].(string); return s }
		w := Watch{Target: get("target"), Plugin: get("plugin"), Instance: get("instance"), Service: get("service")}
		p, _ := plugins[w.Plugin].(map[string]any)
		instances, _ := p["instances"].(map[string]any)
		w.Config, _ = instances[w.Instance].(map[string]any)
		w.Record = index[w.Target]
		message := ""
		switch {
		case seen[w.Target]:
			message = "同一 target 只能声明一个检查范围"
		case w.Record == nil || w.Record.Archived():
			message = "target 必须是存在且未归档的记录主键"
		case w.Config == nil:
			message = "检查范围的插件实例未启用"
		default:
			services := []string{}
			switch w.Plugin {
			case "aliyun", "tencent", "baidu":
				services = []string{"domains", "cdn"}
			case "ctyun":
				services = []string{"cdn"}
			case "cloudflare":
				services = []string{"zones"}
			}
			if !slices.Contains(services, w.Service) {
				message = "插件不支持该 missing 检查服务"
			} else if value, exists := w.Config["services"]; exists {
				list, ok := value.([]any)
				valid := value == nil || ok && len(list) > 0
				enabled := value == nil
				seenServices := map[string]bool{}
				for _, value := range list {
					service, ok := value.(string)
					valid = valid && ok && slices.Contains(services, service) && !seenServices[service]
					seenServices[service] = true
					enabled = enabled || service == w.Service
				}
				if !valid || !enabled || w.Plugin == "cloudflare" {
					message = "检查服务未在实例配置中启用"
				}
			}
			spec, _ := w.Record.Value.(map[string]any)["spec"].(map[string]any)
			w.Identity = IdentityOf(w.Record.Kind, w.Record.Name, spec)
			supported := w.Record.Kind == "Domain" && (w.Service == "domains" || w.Service == "zones")
			if w.Record.Kind == "Resource" {
				w.Endpoint = EndpointIdentity(spec)
				typ := "cdn"
				if w.Service == "zones" {
					typ = "dns-zone"
				}
				supported = w.Service != "domains" && w.Endpoint.Provider == w.Plugin && w.Endpoint.Type == typ && (w.Identity.ResourceID != "" || w.Endpoint.Endpoint != "")
			}
			if !supported {
				message = "target 的资产类型、provider 或自然身份不适用于该检查服务"
			}
		}
		seen[w.Target] = true
		if message != "" {
			path := fmt.Sprintf("audit.watch[%d]", i)
			problems = append(problems, ledger.Problem{Code: ledger.CodeRef, File: repo.Config.File, Path: path, Line: repo.Config.Line(path), Message: message})
			continue
		}
		watches = append(watches, w)
	}
	return watches, problems
}

// WatchRecords 只映射声明的服务；缺少集合是未知，空集合才表示成功返回零条。
func WatchRecords(plugin, instance, service string, spec map[string]any) (map[Identity]bool, bool, error) {
	value, ok := spec[service]
	if !ok {
		return nil, false, nil
	}
	records, _, err := Map(plugin, instance, "", map[string]any{service: value})
	if err != nil {
		return nil, false, err
	}
	identities := map[Identity]bool{}
	for _, r := range records {
		identities[IdentityOf(r.Kind, r.Name, r.Spec)] = true
		if plugin == "cloudflare" {
			identities[IdentityOf("Domain", r.Spec["endpoint"].(string), nil)] = true
		}
	}
	return identities, true, nil
}

func (w Watch) Seen(identities map[Identity]bool) bool {
	return identities[w.Identity] || identities[w.Endpoint]
}
