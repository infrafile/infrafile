package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/observation"
	"github.com/infrafile/infrafile/internal/snapshot"
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
		presence, err := snapshot.ReadPresence(ctx, repo.Dir)
		if err != nil || presence == nil || len(presence.Spec) == 0 {
			return results, err
		}
	}
	state, err := openState(ctx, repo.Dir)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	watches, problems := observation.Watches(repo)
	if len(problems) != 0 {
		return nil, failure("config", "audit.watch 检查范围无效，请先校验台账")
	}
	// 在网络调用前清除失效绑定，即使后续采集失败也不能恢复旧资格。
	pruned, err := state.nextPresence(watches, "", "", nil, now)
	if err != nil {
		return nil, err
	}
	if err := state.WritePresence(pruned); err != nil {
		return nil, err
	}
	for _, name := range names {
		instances := plugins[name].(map[string]any)["instances"].(map[string]any)
		for _, instance := range sortedKeys(instances) {
			if ctx.Err() != nil {
				return results, nil
			}
			r := Result{Plugin: name, Instance: instance, File: "state/" + name + "/" + instance + ".yaml"}
			config := instances[instance].(map[string]any)
			attempt := snapshot.Attempt{AttemptedAt: now.UTC().Format(time.RFC3339), Status: "running", ConfigHash: Hash(config)}
			if err := state.WriteStatus(name, instance, attempt); err != nil {
				return nil, err
			}
			data, spec, err := syncInstance(ctx, name, repo.Dir, protocol.SyncParams{Instance: instance, Config: config}, timeout)
			var presence snapshot.Presence
			if err == nil {
				presence, err = state.nextPresence(watches, name, instance, spec, now)
			}
			if err == nil {
				r.Changed, err = state.Write(name, instance, data, now)
			}
			if err == nil {
				err = state.WritePresence(presence)
			}
			r.OK = err == nil
			if err != nil {
				r.Code, r.Message = "plugin", "插件执行失败"
				var e *Error
				if errors.As(err, &e) {
					r.Code, r.Message = e.Code, e.Message
				}
			}
			attempt.Status = "succeeded"
			attempt.SnapshotHash = fmt.Sprintf("%x", sha256.Sum256(data))
			if !r.OK {
				attempt.Status, attempt.Code, attempt.SnapshotHash = "failed", r.Code, ""
			}
			if err := state.WriteStatus(name, instance, attempt); err != nil {
				r.OK, r.Code, r.Message = false, "write", "执行回执保存失败，已停止后续实例；请检查 state/.sync-status.yaml"
				return append(results, r), nil
			}
			results = append(results, r)
			if ctx.Err() != nil {
				return results, nil
			}
		}
	}
	return results, nil
}

func syncInstance(parent context.Context, name, dir string, params protocol.SyncParams, timeout time.Duration) ([]byte, map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	client, err := Open(ctx, name, dir)
	if err != nil {
		return nil, nil, err
	}
	defer client.Close()
	data, err := Sync(ctx, client, params)
	if err != nil {
		return nil, nil, err
	}
	if ctx.Err() != nil {
		return nil, nil, failure("timeout", "插件超时或被取消")
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, nil, failure("protocol", "插件数据无法编码为 YAML")
	}
	if !validate.SnapshotSafe(value) {
		return nil, nil, failure("sensitive", "插件快照含敏感字段或疑似凭据，已拒绝写入")
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
		return nil, nil, failure("protocol", "插件数据无法编码为 YAML")
	}
	return encoded, value, nil
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
