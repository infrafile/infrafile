// Package protocol 定义内置和外部插件共用的 v1 契约。
package protocol

import (
	"bytes"
	"embed"
	"encoding/json"
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

var schemas = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	result := map[string]*jsonschema.Schema{}
	for _, name := range []string{"request", "response", "describe", "sync", "snapshot"} {
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
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return compiled[name].Validate(v)
}
