// Package plugin 负责插件调用、契约校验及观测快照。
package plugin

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/infrafile/infrafile/protocol"
)

// Client 的参数与结果与 stdio 协议相同；每个实例独占一个 client。
type Client interface {
	Call(context.Context, string, any) (json.RawMessage, error)
	Close()
}

type Error struct{ Code, Message string }

func (e *Error) Error() string           { return e.Message }
func failure(code, message string) error { return &Error{code, message} }

// Sync 先握手，再校验 sync 能力和结果。内外插件均经过这里。
func Sync(ctx context.Context, client Client, params protocol.SyncParams) (data json.RawMessage, err error) {
	defer func() {
		if recover() != nil {
			data, err = nil, failure("plugin", "插件发生内部错误")
		}
	}()
	described, err := client.Call(ctx, "describe", map[string]any{})
	if err != nil {
		return nil, err
	}
	if protocol.Validate("describe", described) != nil {
		return nil, failure("protocol", "插件握手不符合 v1 协议")
	}
	var desc protocol.Description
	if err := json.Unmarshal(described, &desc); err != nil {
		return nil, failure("protocol", "插件握手无法解析")
	}
	if !slices.Contains(desc.Capabilities, "sync") {
		return nil, failure("capability", "插件没有声明 sync 能力")
	}
	result, err := client.Call(ctx, "sync", params)
	if err != nil {
		return nil, err
	}
	if protocol.Validate("sync", result) != nil {
		return nil, failure("protocol", "插件 sync 结果不符合协议")
	}
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(result, &body); err != nil {
		return nil, failure("protocol", "插件结果无法解析")
	}
	return body.Data, nil
}

// example 不接触云端，仅用虚构域名演示完整的内置插件调用路径。
type example struct{}

func (example) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, failure("timeout", "插件超时或被取消")
	}
	if method == "describe" {
		return json.Marshal(protocol.Description{ProtocolVersion: protocol.Version, Capabilities: []string{"sync"}})
	}
	return json.RawMessage(`{"data":{"domains":[{"name":"example.com"}]}}`), nil
}
func (example) Close() {}

// Open 优先匹配内置插件，否则按启用名在 PATH 上查找可执行文件。
func Open(ctx context.Context, name, dir string) (Client, error) {
	if name == "example" {
		return example{}, nil
	}
	switch name {
	case "cloudflare", "dns", "tls", "whois", "ssh", "aliyun", "tencent", "baidu", "ctyun":
		return &builtin{name: name, dir: dir}, nil
	}
	return openExternal(ctx, name)
}
