package ledger

import (
	"regexp"
	"sort"
	"strconv"
)

// 问题代码。新增代码要同步更新 docs/json/validate.md。
const (
	CodeParse       = "parse"        // YAML 无法解析，或文件结构不是一条记录
	CodeLayout      = "layout"       // 文件或目录放错了位置
	CodeKind        = "kind"         // kind 与所在目录不符
	CodeSchema      = "schema"       // 不符合 JSON Schema
	CodeName        = "name"         // metadata.name 与文件名不一致
	CodeRef         = "ref"          // 外键指向不存在的记录
	CodeRefArchived = "ref-archived" // 在用的记录引用了已归档的记录
	CodePlaintext   = "plaintext"    // 疑似明文凭据
	CodeDeleted     = "deleted"      // 与基线相比有记录被删除
)

// Problem 是校验发现的一个问题。
type Problem struct {
	Code    string `json:"code"`
	File    string `json:"file"`           // 相对数据仓根目录、以 / 分隔的路径
	Line    int    `json:"line,omitempty"` // 从 1 开始；0 表示无法定位到行
	Path    string `json:"path,omitempty"` // 字段路径，如 spec.deployments[0].server
	Message string `json:"message"`
}

// SortProblems 按文件、行号、代码、字段路径排序，保证输出稳定。
func SortProblems(ps []Problem) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Path < b.Path
	})
}

var plainKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// JoinKey 拼出对象成员的字段路径。普通键用 a.b，含特殊字符的键用 a["b.c"]。
func JoinKey(parent, key string) string {
	if !plainKey.MatchString(key) {
		return parent + "[" + strconv.Quote(key) + "]"
	}
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// JoinIndex 拼出数组元素的字段路径，如 a[0]。
func JoinIndex(parent string, i int) string {
	return parent + "[" + strconv.Itoa(i) + "]"
}
