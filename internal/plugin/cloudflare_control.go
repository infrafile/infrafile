package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/infrafile/infrafile/protocol"
)

var cfIdentifier = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (b *builtin) cloudflareControl(ctx context.Context, method string, p protocol.ControlParams) (any, error) {
	var cfg struct {
		APIToken  string `json:"api_token"`
		AccountID string `json:"account_id"`
	}
	if err := decodeConfig(p.Config, &cfg); err != nil {
		return nil, err
	}
	if !cfIdentifier.MatchString(p.Target.ZoneID) || (p.Action == "cdn-origin" && (!cfIdentifier.MatchString(p.Target.RulesetID) || !cfIdentifier.MatchString(p.Target.RuleID))) {
		return nil, failure("config", "Cloudflare 控制标识必须是 32 位十六进制 ID")
	}
	if p.Action != "cdn-pause" && p.Action != "cdn-resume" && p.Action != "cdn-origin" {
		return nil, failure("config", "不支持的 Cloudflare 动作")
	}
	value, err := b.credential(ctx, cfg.APIToken)
	if err != nil {
		return nil, err
	}
	headers := http.Header{"Authorization": {"Bearer " + value}, "Content-Type": {"application/json"}}
	base := "/zones/" + p.Target.ZoneID
	zone, err := b.cfObject(ctx, "GET", base, headers, nil)
	if err != nil {
		return nil, err
	}
	paused, ok := zone["paused"].(bool)
	name, _ := zone["name"].(string)
	if !ok || zone["id"] != p.Target.ZoneID || !Hostname(name) {
		return nil, failure("protocol", "Cloudflare zone 响应缺少或不匹配标识与状态")
	}
	if cfg.AccountID != "" {
		account, _ := zone["account"].(map[string]any)
		if account["id"] != cfg.AccountID {
			return nil, failure("config", "目标 zone 不属于配置的账号")
		}
	}
	before := map[string]any{"zone_id": p.Target.ZoneID, "name": name, "paused": paused}
	if p.Action == "cdn-origin" {
		rs, err := b.cfObject(ctx, "GET", base+"/rulesets/phases/http_request_origin/entrypoint", headers, nil)
		if err != nil {
			return nil, err
		}
		rules, ok := rs["rules"].([]any)
		if !ok || rs["id"] != p.Target.RulesetID || rs["kind"] != "zone" || rs["phase"] != "http_request_origin" {
			return nil, failure("protocol", "目标不是该 zone 已部署的 Origin Rules ruleset")
		}
		for _, item := range rules {
			rule, ok := item.(map[string]any)
			if !ok {
				return nil, failure("protocol", "Origin Rule 格式错误")
			}
			id, _ := rule["id"].(string)
			expression, _ := rule["expression"].(string)
			if !cfIdentifier.MatchString(id) || expression == "" {
				return nil, failure("protocol", "Origin Rule 缺少标识或匹配条件")
			}
			delete(rule, "version")
			delete(rule, "last_updated")
			if _, ok := rule["enabled"]; !ok {
				rule["enabled"] = true
			}
		}
		before["ruleset_id"] = p.Target.RulesetID
		before["rules"] = rules
	}
	after, err := Desired(before, p.Target, p.Action, p.Parameters)
	if err != nil {
		return nil, err
	}
	if method == "probe" {
		return before, nil
	}
	if p.ExpectedHash == "" || Hash(before) != p.ExpectedHash {
		return nil, failure("stale", "Cloudflare 状态已变化，未执行")
	}
	path := base
	body := map[string]any{"paused": after["paused"]}
	if p.Action == "cdn-origin" {
		path += "/rulesets/" + p.Target.RulesetID + "/rules/" + p.Target.RuleID
		for _, item := range after["rules"].([]any) {
			rule := item.(map[string]any)
			if rule["id"] == p.Target.RuleID {
				body = rule
				break
			}
		}
		delete(body, "id")
	}
	data, _ := json.Marshal(body)
	if _, err := b.cfObject(ctx, "PATCH", path, headers, data); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

func (b *builtin) cfObject(ctx context.Context, method, path string, headers http.Header, body []byte) (map[string]any, error) {
	var response struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if err := requestJSON(ctx, method, "https://api.cloudflare.com/client/v4"+path, headers, body, &response); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, failure("provider", "Cloudflare API 拒绝请求")
	}
	result, err := Object(response.Result)
	if err != nil {
		return nil, err
	}
	if _, err := b.encodeResult(result); err != nil {
		return nil, err
	}
	return result, nil
}
