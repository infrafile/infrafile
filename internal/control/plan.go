// Package control 实现单个受控动作的计划、回读与执行。
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/protocol"
)

type Binding struct {
	Plugin   string `json:"plugin"`
	Instance string `json:"instance"`
	protocol.Target
}

type Action struct {
	Target     string         `json:"target"`
	Binding    Binding        `json:"binding"`
	RecordHash string         `json:"record_hash"`
	ConfigHash string         `json:"config_hash"`
	Operation  string         `json:"operation"`
	Parameters map[string]any `json:"parameters"`
	Before     map[string]any `json:"before"`
	BeforeHash string         `json:"before_hash"`
	After      map[string]any `json:"after"`
}

type Plan struct {
	Version     string   `json:"version"`
	GeneratedAt string   `json:"generated_at"`
	Actions     []Action `json:"actions"`
}

var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Read(path string) (*Plan, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return nil, fmt.Errorf("计划读取失败或超过 4 MiB")
	}
	if _, err := plugin.Object(data); err != nil {
		return nil, err
	}
	// YAML 的 JSON 子集解析同时拦截重复键，避免审阅工具理解出不同的计划。
	if _, problems := ledger.ParseDocument("plan.json", data); len(problems) > 0 {
		return nil, fmt.Errorf("计划含重复键或无效结构")
	}
	var p Plan
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil {
		return nil, fmt.Errorf("计划结构不合法")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

func (p *Plan) Validate() error {
	if p.Version != "v1" || len(p.Actions) != 1 {
		return fmt.Errorf("只支持 v1 且恰好包含一个动作的计划")
	}
	if _, err := time.Parse(time.RFC3339, p.GeneratedAt); err != nil {
		return fmt.Errorf("计划生成时间不合法")
	}
	a := p.Actions[0]
	if !strings.HasPrefix(a.Target, "Resource/") || len(strings.TrimPrefix(a.Target, "Resource/")) == 0 || !digest.MatchString(a.RecordHash) || !digest.MatchString(a.ConfigHash) || !digest.MatchString(a.BeforeHash) || plugin.Hash(a.Before) != a.BeforeHash {
		return fmt.Errorf("计划目标或状态哈希不合法")
	}
	params := protocol.ControlParams{Instance: a.Binding.Instance, Config: map[string]any{}, Target: a.Binding.Target, Action: a.Operation, Parameters: a.Parameters}
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "probe", "params": params})
	if protocol.Validate("request", req) != nil || !regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`).MatchString(a.Binding.Plugin) || len(a.Binding.Plugin) > 64 {
		return fmt.Errorf("计划插件绑定不合法")
	}
	after, err := plugin.Desired(a.Before, a.Binding.Target, a.Operation, a.Parameters)
	if err != nil {
		return err
	}
	if plugin.Hash(after) != plugin.Hash(a.After) {
		return fmt.Errorf("计划结果与动作不一致")
	}
	data, _ := json.Marshal(p)
	_, err = plugin.Object(data)
	return err
}

func resolve(repo *ledger.Repo, key string) (Binding, map[string]any, string, error) {
	var record *ledger.Record
	for _, r := range repo.Records {
		if r.Kind+"/"+r.Name == key {
			record = r
			break
		}
	}
	if record == nil || record.Kind != "Resource" || record.Archived() {
		return Binding{}, nil, "", fmt.Errorf("控制目标必须是未归档的 Resource 主键")
	}
	spec := record.Value.(map[string]any)["spec"].(map[string]any)
	var b Binding
	data, _ := json.Marshal(spec["control"])
	if json.Unmarshal(data, &b) != nil || b.Plugin == "" || (spec["type"] != "cdn" && spec["type"] != "dns-zone") || spec["provider"] != b.Plugin {
		return b, nil, "", fmt.Errorf("Resource 需要匹配 provider 的 control 绑定及 cdn / dns-zone 类型")
	}
	plugins, _ := repo.Config.Value.(map[string]any)["plugins"].(map[string]any)
	definition, _ := plugins[b.Plugin].(map[string]any)
	instances, _ := definition["instances"].(map[string]any)
	config, ok := instances[b.Instance].(map[string]any)
	if !ok {
		return b, nil, "", fmt.Errorf("控制插件实例未启用")
	}
	return b, config, plugin.Hash(record.Value), nil
}

func Generate(ctx context.Context, repo *ledger.Repo, key, operation string, parameters map[string]any, now time.Time) (*Plan, error) {
	b, c, rh, err := resolve(repo, key)
	if err != nil {
		return nil, err
	}
	client, err := plugin.Open(ctx, b.Plugin, repo.Dir)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	params := protocol.ControlParams{Instance: b.Instance, Config: c, Target: b.Target, Action: operation, Parameters: parameters}
	before, err := observe(ctx, client, params)
	if err != nil {
		return nil, err
	}
	after, err := plugin.Desired(before, b.Target, operation, parameters)
	if err != nil {
		return nil, err
	}
	p := &Plan{Version: "v1", GeneratedAt: now.UTC().Format(time.RFC3339), Actions: []Action{{Target: key, Binding: b, RecordHash: rh, ConfigHash: plugin.Hash(c), Operation: operation, Parameters: parameters, Before: before, BeforeHash: plugin.Hash(before), After: after}}}
	return p, p.Validate()
}

func observe(ctx context.Context, client plugin.Client, p protocol.ControlParams) (map[string]any, error) {
	data, err := plugin.Invoke(ctx, client, "probe", p)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("控制观测超时或被取消")
	}
	return plugin.Object(data)
}

type Result struct {
	Version string `json:"version"`
	OK      bool   `json:"ok"`
	Target  string `json:"target"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func Apply(ctx context.Context, repo *ledger.Repo, p *Plan) (Result, error) {
	r := Result{Version: "v1"}
	if err := p.Validate(); err != nil {
		return r, err
	}
	a := p.Actions[0]
	r.Target = a.Target
	b, c, rh, err := resolve(repo, a.Target)
	if err != nil {
		return r, err
	}
	if b != a.Binding || rh != a.RecordHash || plugin.Hash(c) != a.ConfigHash {
		return r, fmt.Errorf("台账目标或插件配置已变化，请重新生成计划")
	}
	client, err := plugin.Open(ctx, b.Plugin, repo.Dir)
	if err != nil {
		return r, err
	}
	defer client.Close()
	params := protocol.ControlParams{Instance: b.Instance, Config: c, Target: b.Target, Action: a.Operation, Parameters: a.Parameters}
	before, err := observe(ctx, client, params)
	if err != nil {
		return r, err
	}
	if plugin.Hash(before) != a.BeforeHash {
		r.Status = "stale"
		r.Message = "观测状态已变化，未执行；请重新生成计划"
		return r, nil
	}
	if plugin.Hash(before) == plugin.Hash(a.After) {
		r.OK = true
		r.Status = "unchanged"
		return r, nil
	}
	if ctx.Err() != nil {
		return r, fmt.Errorf("执行已取消")
	}
	params.ExpectedHash = a.BeforeHash
	data, err := plugin.Invoke(ctx, client, "control", params)
	if err == nil {
		_, err = plugin.Object(data)
	}
	if err != nil {
		r.Status = "uncertain"
		r.Message = "控制请求未获确认，远端可能已改变；请先回读后重新计划"
		return r, nil
	}
	after, err := observe(ctx, client, params)
	if err != nil {
		r.Status = "uncertain"
		r.Message = "控制请求已发送，但回读失败；请先回读后重新计划"
		return r, nil
	}
	if plugin.Hash(after) != plugin.Hash(a.After) {
		r.Status = "mismatch"
		r.Message = "执行后回读与计划不符；请检查远端状态"
		return r, nil
	}
	r.OK = true
	r.Status = "applied"
	return r, nil
}
