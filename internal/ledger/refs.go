package ledger

// Reference 是一条记录中由 schema 的 x-ref 声明的外键。
type Reference struct {
	Kind string
	Name string
	Path string
}

// References 返回记录中的外键；类型错误仍由 schema 校验报告。
func (r *Record) References(schemas *Schemas) []Reference {
	var refs []Reference
	for _, ref := range schemas.Refs(r.Kind) {
		matchPath(r.Value, ref.Pattern, "", func(path string, v any) {
			if name, ok := v.(string); ok {
				refs = append(refs, Reference{Kind: ref.Target, Name: name, Path: path})
			}
		})
	}
	return refs
}

// matchPath 找出 v 中符合 pattern 的所有值，"[]" 匹配数组的每个元素。
func matchPath(v any, pattern []string, path string, fn func(path string, v any)) {
	if len(pattern) == 0 {
		fn(path, v)
		return
	}
	if pattern[0] == "[]" {
		items, _ := v.([]any)
		for i, item := range items {
			matchPath(item, pattern[1:], JoinIndex(path, i), fn)
		}
		return
	}
	m, _ := v.(map[string]any)
	child, ok := m[pattern[0]]
	if !ok {
		return
	}
	matchPath(child, pattern[1:], JoinKey(path, pattern[0]), fn)
}
