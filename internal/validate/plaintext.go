package validate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/infrafile/infrafile/internal/ledger"
)

// secretRef 是凭据引用的格式，与 common.schema.json 的 secrets 一致。
var secretRef = regexp.MustCompile(`^(sops|op|env)://.+`)

// tokenPatterns 是常见凭据的特征。宁可漏报也不要误报到让人想关掉检查。
var tokenPatterns = []struct {
	label string
	re    *regexp.Regexp
}{
	{"私钥", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"AWS 访问密钥", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"GitHub 令牌", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}`)},
	{"GitHub 令牌", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}`)},
	{"GitLab 令牌", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`)},
	{"Slack 令牌", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
	{"API 密钥", regexp.MustCompile(`\bsk-(?:proj-|ant-)?[A-Za-z0-9_-]{32,}`)},
	{"Google API 密钥", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{"阿里云 AccessKey", regexp.MustCompile(`\bLTAI[0-9A-Za-z]{12,24}\b`)},
	{"腾讯云 SecretId", regexp.MustCompile(`\bAKID[0-9A-Za-z]{32}\b`)},
	{"JWT", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+`)},
	{"URL 里的密码", regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@`)},
}

func secretKey(key string) bool {
	compact := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
	compact = strings.TrimSuffix(compact, "s")
	for _, suffix := range []string{"password", "passwd", "pwd", "secret", "token", "apikey", "accesskey", "accesskeyid", "secretkey", "privatekey", "secretaccesskey", "authorization", "cookie", "credential"} {
		if strings.HasSuffix(compact, suffix) {
			return true
		}
	}
	return false
}

// SnapshotSafe 比台账扫描更严格：即使字段为空或值为引用也不保存凭据字段。
// deny-list 是误写防线，不能识别任意名称下的任意秘密。
func SnapshotSafe(v any) bool {
	switch v := v.(type) {
	case map[string]any:
		for key, child := range v {
			if secretKey(key) || !SnapshotSafe(key) || !SnapshotSafe(child) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !SnapshotSafe(child) {
				return false
			}
		}
	case string:
		for _, pattern := range tokenPatterns {
			if pattern.re.MatchString(v) {
				return false
			}
		}
	}
	return true
}

// scanPlaintext 在 spec.secrets 以外的地方找疑似明文凭据。
// spec.secrets 里的值由 schema 要求必须是凭据引用。
func scanPlaintext(doc *ledger.Document) []ledger.Problem {
	var problems []ledger.Problem
	report := func(path, msg string) {
		problems = append(problems, ledger.Problem{Code: ledger.CodePlaintext, File: doc.File,
			Line: doc.Line(path), Path: path, Message: msg})
	}
	var walk func(v any, key, path string)
	walk = func(v any, key, path string) {
		switch v := v.(type) {
		case map[string]any:
			if path == "spec.secrets" {
				return
			}
			for k, child := range v {
				walk(child, k, ledger.JoinKey(path, k))
			}
		case []any:
			for i, child := range v {
				walk(child, key, ledger.JoinIndex(path, i))
			}
		case string:
			for _, p := range tokenPatterns {
				if p.re.MatchString(v) {
					report(path, fmt.Sprintf("疑似明文凭据（%s），不要写进台账；凭据放进 spec.secrets，写成 sops://、op:// 或 env:// 引用", p.label))
					return
				}
			}
			if v != "" && secretKey(key) && !secretRef.MatchString(v) {
				report(path, "字段名像凭据，值却不是凭据引用；请放进 spec.secrets，写成 sops://、op:// 或 env:// 引用")
			}
		case int, int64, uint64, float64:
			if secretKey(key) {
				report(path, "字段名像凭据，值却不是凭据引用；请放进 spec.secrets，写成 sops://、op:// 或 env:// 引用")
			}
		}
	}
	walk(doc.Value, "", "")
	return problems
}
