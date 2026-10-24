package ledger

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schemas/*.schema.json
var schemaFS embed.FS

// 编译时用的虚拟地址，只用来让文件之间的相对 $ref 能解析，不会真的访问。
const schemaBase = "file:///infrafile/schemas/"

// ConfigKind 是 infrafile.yaml 的 kind。
const ConfigKind = "Config"

// SchemaFileName 返回某个 kind（或 Config）的 schema 文件名。
func SchemaFileName(kind string) string {
	return strings.ToLower(kind) + ".schema.json"
}

// SchemaFiles 返回全部内嵌 schema 的文件名和内容，按文件名排序。
func SchemaFiles() (map[string][]byte, []string) {
	entries, err := schemaFS.ReadDir("schemas")
	if err != nil {
		panic(err) // 内嵌文件系统读不了只可能是构建出错
	}
	files := make(map[string][]byte, len(entries))
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		data, err := schemaFS.ReadFile("schemas/" + e.Name())
		if err != nil {
			panic(err)
		}
		files[e.Name()] = data
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return files, names
}

// RefField 是 schema 里用 x-ref 声明的一个外键字段。
type RefField struct {
	// Pattern 是字段路径，"[]" 表示数组的任意元素，如 spec / deployments / [] / server。
	Pattern []string
	Target  string // 被引用的 kind
}

// Schemas 是编译好的全部 schema。
type Schemas struct {
	compiled map[string]*jsonschema.Schema
	refs     map[string][]RefField
}

var (
	schemasOnce sync.Once
	schemas     *Schemas
	schemasErr  error
)

// LoadSchemas 编译内嵌的 schema，只做一次。
func LoadSchemas() (*Schemas, error) {
	schemasOnce.Do(func() { schemas, schemasErr = compileSchemas() })
	return schemas, schemasErr
}

func compileSchemas() (*Schemas, error) {
	files, names := SchemaFiles()
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	raw := make(map[string]any, len(names))
	for _, name := range names {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(files[name]))
		if err != nil {
			return nil, fmt.Errorf("内嵌 schema %s：%w", name, err)
		}
		if err := c.AddResource(schemaBase+name, doc); err != nil {
			return nil, fmt.Errorf("内嵌 schema %s：%w", name, err)
		}
		raw[name] = doc
	}

	s := &Schemas{compiled: map[string]*jsonschema.Schema{}, refs: map[string][]RefField{}}
	for _, kind := range append([]string{ConfigKind}, kindNames()...) {
		name := SchemaFileName(kind)
		sch, err := c.Compile(schemaBase + name)
		if err != nil {
			return nil, fmt.Errorf("内嵌 schema %s：%w", name, err)
		}
		s.compiled[kind] = sch
		refs, err := collectRefs(raw[name], nil)
		if err != nil {
			return nil, fmt.Errorf("内嵌 schema %s：%w", name, err)
		}
		s.refs[kind] = refs
	}
	return s, nil
}

func kindNames() []string {
	names := make([]string, len(Kinds))
	for i, k := range Kinds {
		names[i] = k.Name
	}
	return names
}

// collectRefs 沿 properties 和 items 收集 x-ref，不跟随 $ref。
func collectRefs(node any, path []string) ([]RefField, error) {
	m, ok := node.(map[string]any)
	if !ok {
		return nil, nil
	}
	var refs []RefField
	if target, ok := m["x-ref"]; ok {
		t, _ := target.(string)
		if _, known := KindByName(t); !known {
			return nil, fmt.Errorf("%s 的 x-ref 指向未知的 kind %v", strings.Join(path, "."), target)
		}
		refs = append(refs, RefField{Pattern: append([]string(nil), path...), Target: t})
	}
	if props, ok := m["properties"].(map[string]any); ok {
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sub, err := collectRefs(props[k], append(path, k))
			if err != nil {
				return nil, err
			}
			refs = append(refs, sub...)
		}
	}
	if items, ok := m["items"]; ok {
		sub, err := collectRefs(items, append(path, "[]"))
		if err != nil {
			return nil, err
		}
		refs = append(refs, sub...)
	}
	return refs, nil
}

// Validate 用 kind（或 Config）的 schema 校验一个值。
// 不符合时返回 *jsonschema.ValidationError。
func (s *Schemas) Validate(kind string, v any) error {
	sch, ok := s.compiled[kind]
	if !ok {
		return fmt.Errorf("未知的 kind %q", kind)
	}
	return sch.Validate(v)
}

// Refs 返回 kind 的全部外键字段。
func (s *Schemas) Refs(kind string) []RefField {
	return s.refs[kind]
}

// Bundle 把全部 schema 合成一个 JSON 对象，键是文件名。
func Bundle() (json.RawMessage, error) {
	files, names := SchemaFiles()
	var buf bytes.Buffer
	buf.WriteString("{")
	for i, name := range names {
		if i > 0 {
			buf.WriteString(",")
		}
		key, _ := json.Marshal(name)
		buf.Write(key)
		buf.WriteString(":")
		buf.Write(files[name])
	}
	buf.WriteString("}")
	return json.RawMessage(buf.Bytes()), nil
}
