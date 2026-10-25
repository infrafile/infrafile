package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
	"github.com/infrafile/infrafile/protocol"
	"go.yaml.in/yaml/v3"
)

type Result struct {
	Plugin   string `json:"plugin"`
	Instance string `json:"instance"`
	File     string `json:"file"`
	OK       bool   `json:"ok"`
	Changed  bool   `json:"changed"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message,omitempty"`
}

// Run 只接收已校验的数据仓，按插件名和实例名顺序执行。
func Run(ctx context.Context, repo *ledger.Repo, selected string, timeout time.Duration, now time.Time) ([]Result, error) {
	config := repo.Config.Value.(map[string]any)
	plugins, _ := config["plugins"].(map[string]any)
	names := sortedKeys(plugins)
	if selected != "" {
		if !slices.Contains(names, selected) {
			return nil, fmt.Errorf("插件 %q 未在 infrafile.yaml 中启用", selected)
		}
		names = []string{selected}
	}
	results := []Result{}
	if len(names) == 0 {
		return results, nil
	}
	state, err := openState(repo.Dir)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	for _, name := range names {
		instances := plugins[name].(map[string]any)["instances"].(map[string]any)
		for _, instance := range sortedKeys(instances) {
			r := Result{Plugin: name, Instance: instance, File: "state/" + name + "/" + instance + ".yaml"}
			data, err := syncInstance(ctx, name, protocol.SyncParams{Instance: instance, Config: instances[instance].(map[string]any)}, timeout)
			if err == nil {
				r.Changed, err = state.Write(name, instance, data, now)
			}
			r.OK = err == nil
			if err != nil {
				r.Code, r.Message = "plugin", "插件执行失败"
				var e *Error
				if errors.As(err, &e) {
					r.Code, r.Message = e.Code, e.Message
				}
			}
			results = append(results, r)
			if ctx.Err() != nil {
				return results, nil
			}
		}
	}
	return results, nil
}

func syncInstance(parent context.Context, name string, params protocol.SyncParams, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	client, err := Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	data, err := Sync(ctx, client, params)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, failure("timeout", "插件超时或被取消")
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, failure("protocol", "插件数据无法编码为 YAML")
	}
	if !validate.SnapshotSafe(value) {
		return nil, failure("sensitive", "插件快照含敏感字段或疑似凭据，已拒绝写入")
	}
	yamlNumbers(value)
	snapshot := struct {
		APIVersion string            `yaml:"apiVersion"`
		Kind       string            `yaml:"kind"`
		Metadata   map[string]string `yaml:"metadata"`
		Spec       map[string]any    `yaml:"spec"`
	}{ledger.APIVersion, "Snapshot", map[string]string{"name": params.Instance}, value}
	encoded, err := yaml.Marshal(snapshot)
	if err != nil {
		return nil, failure("protocol", "插件数据无法编码为 YAML")
	}
	return encoded, nil
}

// 用原数字文本构建 YAML 标量，避免大整数或小数经过 float64 后丢失精度。
func yamlNumbers(v any) any {
	switch v := v.(type) {
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(v.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: v.String()}
	case map[string]any:
		for key, child := range v {
			v[key] = yamlNumbers(child)
		}
	case []any:
		for i, child := range v {
			v[i] = yamlNumbers(child)
		}
	}
	return v
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
