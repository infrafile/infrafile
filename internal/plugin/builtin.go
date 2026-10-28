package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/infrafile/infrafile/internal/secret"
	"github.com/infrafile/infrafile/protocol"
)

type builtin struct {
	name, dir string
	values    []string
}

func (b *builtin) Close() { b.values = nil }

func (b *builtin) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, failure("timeout", "插件超时或被取消")
	}
	if method == "describe" {
		capability, _ := builtinCapability(b.name)
		return json.Marshal(protocol.Description{ProtocolVersion: protocol.Version, Capabilities: capability.Methods})
	}
	if b.name == "cloudflare" && (method == "probe" || method == "control") {
		p, ok := params.(protocol.ControlParams)
		if !ok {
			return nil, failure("config", "控制参数格式错误")
		}
		b.values = nil
		defer b.Close()
		data, err := b.cloudflareControl(ctx, method, p)
		if err != nil {
			return nil, err
		}
		return b.encodeResult(data)
	}
	if method != "sync" {
		return nil, failure("capability", "插件不支持该方法")
	}
	p, ok := params.(protocol.SyncParams)
	if !ok {
		return nil, failure("config", "插件参数格式错误")
	}
	b.values = nil
	defer b.Close()
	var data any
	var err error
	switch b.name {
	case "cloudflare":
		data, err = b.cloudflare(ctx, p.Config)
	case "dns":
		data, err = probeDNS(ctx, p.Config)
	case "tls":
		data, err = probeTLS(ctx, p.Config)
	case "whois":
		data, err = probeWHOIS(ctx, p.Config)
	case "ssh":
		data, err = b.probeSSH(ctx, p.Config)
	case "aliyun":
		data, err = b.aliyun(ctx, p.Config)
	case "tencent":
		data, err = b.tencent(ctx, p.Config)
	case "baidu":
		data, err = b.baidu(ctx, p.Config)
	case "ctyun":
		data, err = b.ctyun(ctx, p.Config)
	}
	if ctx.Err() != nil {
		return nil, failure("timeout", "插件超时或被取消")
	}
	if err != nil {
		return nil, err
	}
	return b.encodeResult(data)
}

func (b *builtin) encodeResult(data any) (json.RawMessage, error) {
	result, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		return nil, failure("protocol", "插件结果无法编码")
	}
	for _, value := range b.values {
		encoded, _ := json.Marshal(value)
		if bytes.Contains(result, encoded[1:len(encoded)-1]) {
			return nil, failure("sensitive", "插件结果包含认证值，已拒绝输出")
		}
	}
	return result, nil
}

func decodeConfig(config map[string]any, result any) error {
	data, err := json.Marshal(config)
	if err != nil {
		return failure("config", "插件配置必须是 JSON 对象")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(result) != nil {
		return failure("config", "插件配置有未知字段或字段类型错误")
	}
	return nil
}

func (b *builtin) credential(ctx context.Context, ref string) (string, error) {
	value, err := (secret.Resolver{Dir: b.dir}).Resolve(ctx, ref)
	if err != nil || value == "" {
		return "", failure("credential", "插件凭据引用无法解析或值为空")
	}
	b.values = append(b.values, value)
	return value, nil
}

// 固定服务地址、不跟随重定向；响应只在内存中解析，错误不带请求或响应原文。
func requestJSON(ctx context.Context, method, endpoint string, headers http.Header, body []byte, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return failure("http", "无法创建云 API 请求")
	}
	req.Header = headers
	client := http.Client{Transport: http.DefaultTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return failure("http", "云 API 请求失败")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return failure("http", fmt.Sprintf("云 API 返回 HTTP %d", response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return failure("http", "云 API 响应读取失败或超过 4 MiB")
	}
	if json.Unmarshal(data, result) != nil {
		return failure("protocol", "云 API 响应格式错误")
	}
	return nil
}
