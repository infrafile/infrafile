package validate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// schemaProblems 把 schema 校验错误翻译成 Problem。
// 消息里不回显字段的值，以免把误写进台账的明文再打印一遍。
func schemaProblems(doc *ledger.Document, err error) []ledger.Problem {
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []ledger.Problem{{Code: ledger.CodeSchema, File: doc.File, Message: err.Error()}}
	}
	var problems []ledger.Problem
	collect(doc, ve, &problems)
	return problems
}

func collect(doc *ledger.Document, ve *jsonschema.ValidationError, out *[]ledger.Problem) {
	if len(ve.Causes) > 0 {
		for _, c := range ve.Causes {
			collect(doc, c, out)
		}
		return
	}

	path, line := locate(doc, ve.InstanceLocation)
	add := func(path string, line int, code, msg string) {
		*out = append(*out, ledger.Problem{Code: code, File: doc.File, Line: line, Path: path, Message: msg})
	}
	switch k := ve.ErrorKind.(type) {
	case *kind.Required:
		for _, name := range k.Missing {
			add(ledger.JoinKey(path, name), line, ledger.CodeSchema, "缺少必填字段")
		}
	case *kind.AdditionalProperties:
		// 标签键名用 patternProperties 而不是 propertyNames 约束：
		// jsonschema v6 给 propertyNames 错误的实例位置不可靠。
		msg := "未知字段"
		switch path {
		case "spec":
			msg += "（自定义字段请加 x- 前缀）"
		case "metadata.labels":
			msg = "标签键名不合法：只能用字母、数字和 . _ / -，首尾是字母或数字"
		}
		for _, name := range k.Properties {
			p := ledger.JoinKey(path, name)
			l := doc.Line(p)
			if l == 0 {
				l = line
			}
			add(p, l, ledger.CodeSchema, msg)
		}
	case *kind.Pattern:
		if isSecretValue(ve.InstanceLocation) {
			add(path, line, ledger.CodePlaintext, "应为凭据引用（sops://、op:// 或 env://），不要写明文")
		} else if path == "metadata.name" {
			add(path, line, ledger.CodeSchema, "只能用小写字母、数字、点和连字符，首尾是字母或数字")
		} else {
			add(path, line, ledger.CodeSchema, "不符合格式 "+k.Want)
		}
	default:
		add(path, line, ledger.CodeSchema, message(ve.ErrorKind))
	}
}

func message(ek jsonschema.ErrorKind) string {
	switch k := ek.(type) {
	case *kind.Type:
		want := make([]string, len(k.Want))
		for i, w := range k.Want {
			want[i] = typeName(w)
		}
		return fmt.Sprintf("类型应为%s，实际是%s", strings.Join(want, "或"), typeName(k.Got))
	case *kind.Const:
		return "应为 " + jsonText(k.Want)
	case *kind.Enum:
		want := make([]string, len(k.Want))
		for i, w := range k.Want {
			want[i] = jsonText(w)
		}
		return "应为以下之一：" + strings.Join(want, "、")
	case *kind.Format:
		switch k.Want {
		case "date":
			return "应为日期（YYYY-MM-DD）"
		case "uri":
			return "应为 URI"
		}
		return "格式应为 " + k.Want
	case *kind.MinLength:
		if k.Want == 1 {
			return "不能为空"
		}
		return fmt.Sprintf("长度至少为 %d", k.Want)
	case *kind.Minimum:
		return "不能小于 " + k.Want.RatString()
	case *kind.Maximum:
		return "不能大于 " + k.Want.RatString()
	case *kind.FalseSchema:
		return "不允许出现这个字段"
	}
	return "不符合 schema（" + strings.Join(ek.KeywordPath(), "/") + "）"
}

var typeNames = map[string]string{
	"object":  "对象",
	"array":   "数组",
	"string":  "字符串",
	"integer": "整数",
	"number":  "数字",
	"boolean": "布尔值",
	"null":    "空值",
}

func typeName(t string) string {
	if n, ok := typeNames[t]; ok {
		return n
	}
	return t
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// locate 把 schema 错误的实例位置换成字段路径，并找到最近的有行号的位置。
func locate(doc *ledger.Document, loc []string) (string, int) {
	path, line := "", doc.Line("")
	v := doc.Value
	for _, tok := range loc {
		if items, ok := v.([]any); ok {
			i, _ := strconv.Atoi(tok)
			path = ledger.JoinIndex(path, i)
			if i >= 0 && i < len(items) {
				v = items[i]
			} else {
				v = nil
			}
		} else {
			path = ledger.JoinKey(path, tok)
			m, _ := v.(map[string]any)
			v = m[tok]
		}
		if l := doc.Line(path); l != 0 {
			line = l
		}
	}
	return path, line
}

// isSecretValue 报告实例位置是否是 spec.secrets 下的一个值。
func isSecretValue(loc []string) bool {
	return len(loc) == 3 && loc[0] == "spec" && loc[1] == "secrets"
}
