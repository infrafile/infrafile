package main

import (
	"encoding/json"
	"strings"

	"github.com/infrafile/infrafile/internal/audit"
	"github.com/infrafile/infrafile/internal/ledger"
)

type queryTool struct {
	name, description string
	schema            map[string]any
}

// Both transports use the same selectors and offline query implementation.
func queryTools() ([]queryTool, error) {
	files, _ := ledger.SchemaFiles()
	var common map[string]any
	if err := json.Unmarshal(files["common.schema.json"], &common); err != nil {
		return nil, err
	}
	defs := common["$defs"].(map[string]any)
	namePattern := defs["name"].(map[string]any)["pattern"].(string)
	labels := defs["metadata"].(map[string]any)["properties"].(map[string]any)["labels"].(map[string]any)
	labels["minProperties"] = 1
	kinds := []string{}
	descriptions := []string{}
	for _, kind := range ledger.Kinds {
		kinds = append(kinds, kind.Name)
		var schema map[string]any
		json.Unmarshal(files[ledger.SchemaFileName(kind.Name)], &schema)
		descriptions = append(descriptions, kind.Name+"："+schema["description"].(string))
	}
	key := map[string]any{"type": "string", "pattern": "^(" + strings.Join(kinds, "|") + ")/" + strings.TrimPrefix(namePattern, "^"), "description": "台账主键 Kind/name；必须已登记。"}
	kindSchema := map[string]any{"type": "string", "enum": kinds}
	selector := func(required bool) map[string]any {
		s := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"key": key, "labels": labels, "kind": kindSchema}}
		if required {
			s["oneOf"] = []any{map[string]any{"required": []string{"key"}, "not": map[string]any{"required": []string{"labels"}}}, map[string]any{"required": []string{"labels"}, "not": map[string]any{"required": []string{"key"}}}}
		}
		return s
	}
	empty := map[string]any{"type": "object", "additionalProperties": false}
	listSchema := selector(false)
	expirySchema := selector(false)
	expirySchema["properties"].(map[string]any)["within_days"] = map[string]any{"type": "integer", "minimum": 0}
	ruleIDs := []string{}
	for _, rule := range audit.Rules() {
		ruleIDs = append(ruleIDs, rule.ID)
	}
	auditSchema := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"rules":        map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": ruleIDs}},
		"max_age_days": map[string]any{"type": "integer", "minimum": 0},
	}}
	inspectSchema := selector(false)
	inspectSchema["properties"].(map[string]any)["max_age_days"] = map[string]any{"type": "integer", "minimum": 0}
	inspectSchema["not"] = map[string]any{"required": []string{"key", "labels"}}
	planSchema := selector(true)
	props := planSchema["properties"].(map[string]any)
	props["action"] = map[string]any{"enum": []string{"cdn-pause", "cdn-resume", "cdn-origin"}}
	props["origin_key"] = map[string]any{"type": "string", "pattern": "^(Server|Resource|Domain)/" + strings.TrimPrefix(namePattern, "^")}
	planSchema["required"] = []string{"action"}
	planSchema["if"] = map[string]any{"properties": map[string]any{"action": map[string]any{"const": "cdn-origin"}}}
	planSchema["then"] = map[string]any{"required": []string{"origin_key"}}
	planSchema["else"] = map[string]any{"not": map[string]any{"required": []string{"origin_key"}}}
	return []queryTool{
		{"list", "按主键、类型或标签筛选台账（含归档）；标签精确匹配且全部满足。", listSchema},
		{"show", "读取唯一记录和引用关系，省略凭据字段。" + strings.Join(descriptions, "；"), selector(true)},
		{"expiry", "检查在用资产到期日，默认30天，含已过期及边界当天。", expirySchema},
		{"audit", "离线审计已启用实例的快照，不访问网络或解析凭据。默认运行就绪规则；stale_state 和 missing 需要 max_age_days。", auditSchema},
		{"inspect", "离线聚合选定资产的直接关系、执行/覆盖/风险；无新鲜证据保持 unknown，不写历史、不联网或解析凭据。", inspectSchema},
		{"validate", "只读校验当前数据仓，返回问题代码与数量；详细诊断用 CLI validate。", empty},
		{"schema", "返回与 CLI 校验相同的内嵌 JSON Schema。", empty},
		{"capabilities", "离线返回第一方插件的字段、动作、权限前提和支持边界；不检查当前账号或现场健康，外部插件细节未知。", empty},
		{"plan", "只读生成单动作计划，不保存、不执行。标签必须唯一匹配在用 Resource；改回源必须提供已登记的 origin_key。", planSchema},
	}, nil
}
