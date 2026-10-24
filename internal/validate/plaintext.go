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

var secretKeys = map[string]bool{
	"password": true, "passwd": true, "pwd": true, "secret": true, "token": true,
	"api_key": true, "apikey": true, "access_key": true, "secret_key": true,
	"private_key": true, "client_secret": true, "auth_token": true, "access_token": true,
}

func secretKey(key string) bool {
	k := strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(key, "x-")), "-", "_")
	if secretKeys[k] {
		return true
	}
	for _, suffix := range []string{"_password", "_passwd", "_secret", "_token"} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
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
