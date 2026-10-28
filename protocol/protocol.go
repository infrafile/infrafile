// Package protocol 定义插件 v1 契约与版本化管理记录信封。
package protocol

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const Version = "v1"

//go:embed *.schema.json
var files embed.FS

type Description struct {
	ProtocolVersion string   `json:"protocol_version"`
	Capabilities    []string `json:"capabilities"`
}

type SyncParams struct {
	Instance string         `json:"instance"`
	Config   map[string]any `json:"config"`
}

// Target 只包含台账中明确登记的控制标识，不接受任意 API 地址。
type Target struct {
	ZoneID    string `json:"zone_id"`
	RulesetID string `json:"ruleset_id,omitempty"`
	RuleID    string `json:"rule_id,omitempty"`
}

type ControlParams struct {
	Instance     string         `json:"instance"`
	Config       map[string]any `json:"config"`
	Target       Target         `json:"target"`
	Action       string         `json:"action"`
	Parameters   map[string]any `json:"parameters"`
	ExpectedHash string         `json:"expected_hash,omitempty"`
}

var schemas = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	result := map[string]*jsonschema.Schema{}
	for _, name := range []string{"request", "response", "describe", "sync", "snapshot", "record"} {
		data, err := files.ReadFile(name + ".schema.json")
		if err != nil {
			return nil, err
		}
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		url := "file:///infrafile/protocol/" + name + ".schema.json"
		if err := c.AddResource(url, v); err != nil {
			return nil, err
		}
		s, err := c.Compile(url)
		if err != nil {
			return nil, err
		}
		result[name] = s
	}
	return result, nil
})

// Validate 校验消息；调用方不能把校验错误中的插件原文直接输出。
func Validate(name string, data json.RawMessage) error {
	compiled, err := schemas()
	if err != nil {
		return err
	}
	schema, ok := compiled[name]
	if !ok {
		return fmt.Errorf("未知协议 Schema：%s", name)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(v)
}
