package plugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/infrafile/infrafile/internal/validate"
	"github.com/infrafile/infrafile/protocol"
)

// Object 保留 JSON 数字精度，并在任何计划输出前复用快照敏感字段检查。
func Object(data []byte) (map[string]any, error) {
	var value map[string]any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if !json.Valid(data) || d.Decode(&value) != nil || value == nil || !validate.SnapshotSafe(value) {
		return nil, failure("protocol", "控制数据格式错误或含敏感字段")
	}
	return value, nil
}

func Hash(value any) string {
	data, _ := json.Marshal(value)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func Hostname(host string) bool {
	if len(host) > 253 || !strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) > 63 || !hostnameLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// Desired 从已审查状态推导唯一允许的变更，拒绝计划夹带额外修改。
func Desired(before map[string]any, target protocol.Target, action string, parameters map[string]any) (map[string]any, error) {
	data, _ := json.Marshal(before)
	after, err := Object(data)
	if err != nil {
		return nil, err
	}
	if _, ok := before["paused"].(bool); !ok {
		return nil, fmt.Errorf("观测状态缺少 paused")
	}
	switch action {
	case "cdn-pause", "cdn-resume":
		if len(parameters) != 0 {
			return nil, fmt.Errorf("暂停或恢复不接受额外参数")
		}
		after["paused"] = action == "cdn-pause"
	case "cdn-origin":
		host, _ := parameters["host"].(string)
		if len(parameters) != 1 || !Hostname(host) || target.RulesetID == "" || target.RuleID == "" {
			return nil, fmt.Errorf("改回源需要合法主机名及已有 ruleset_id / rule_id")
		}
		rules, _ := after["rules"].([]any)
		found := false
		for _, item := range rules {
			rule, _ := item.(map[string]any)
			if rule["id"] != target.RuleID {
				continue
			}
			if found || rule["action"] != "route" || rule["enabled"] != true {
				return nil, fmt.Errorf("目标必须是唯一且启用的 Origin Rule")
			}
			found = true
			params, _ := rule["action_parameters"].(map[string]any)
			if params == nil {
				return nil, fmt.Errorf("Origin Rule 缺少 action_parameters")
			}
			origin, _ := params["origin"].(map[string]any)
			if origin == nil {
				origin = map[string]any{}
				params["origin"] = origin
			}
			origin["host"] = host
		}
		if !found {
			return nil, fmt.Errorf("未找到目标 Origin Rule")
		}
	default:
		return nil, fmt.Errorf("未知控制动作")
	}
	return after, nil
}
