package secret

import (
	"bytes"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/getsops/sops/v3/decrypt"
	"github.com/getsops/sops/v3/logging"
	"go.yaml.in/yaml/v3"
)

func init() {
	// SOPS 的诊断可能回显文件注释；所有对外错误由本包生成。
	for _, logger := range logging.Loggers {
		logger.SetOutput(io.Discard)
	}
}

var encryptedString = regexp.MustCompile(`^ENC\[AES256_GCM,data:[A-Za-z0-9+/=]*,iv:[A-Za-z0-9+/=]+,tag:[A-Za-z0-9+/=]+,type:str\]$`)

func (r Resolver) sops(ref string, resolve bool) (string, error) {
	file, key, ok := strings.Cut(strings.TrimPrefix(ref, "sops://"), "#")
	file, err := url.PathUnescape(file)
	if !ok || err != nil || !filepath.IsLocal(file) || strings.ContainsAny(file, "?\\\x00") || key == "" || strings.ContainsAny(key, "#?\x00") {
		return "", &Error{"invalid-ref", "sops 引用必须是 sops://仓内相对文件#以点分隔的字段路径"}
	}
	keys := strings.Split(key, ".")
	for _, k := range keys {
		if k == "" || keys[0] == "sops" {
			return "", &Error{"invalid-ref", "SOPS 字段路径为空或指向加密元数据"}
		}
	}
	root, err := os.OpenRoot(r.Dir)
	if err != nil {
		return "", &Error{"unavailable", "无法打开数据仓目录"}
	}
	defer root.Close()
	data, err := root.ReadFile(file)
	if err != nil {
		return "", &Error{"unavailable", "无法读取仓内密文文件（可能不存在或路径越界）"}
	}
	doc, err := secretDocument(data)
	if err != nil {
		return "", err
	}
	meta, _ := doc["sops"].(map[string]any)
	ages, _ := meta["age"].([]any)
	mac, _ := meta["mac"].(string)
	if len(ages) == 0 || !encryptedString.MatchString(mac) {
		return "", &Error{"invalid-file", "需要带 age 接收者和加密 MAC 的 SOPS YAML 文件"}
	}
	for _, backend := range []string{"kms", "gcp_kms", "azure_kv", "hc_vault", "hckms", "pgp", "key_groups"} {
		if v := meta[backend]; v != nil {
			if list, ok := v.([]any); !ok || len(list) != 0 {
				return "", &Error{"unsupported", "M3 仅支持单组 age 密文，不调用云 KMS 或 PGP"}
			}
		}
	}
	value, err := secretField(doc, keys)
	if err != nil {
		return "", err
	}
	if !encryptedString.MatchString(value) {
		return "", &Error{"invalid-value", "目标字段必须是 SOPS 加密的字符串"}
	}
	if !resolve {
		return "", nil
	}
	for _, name := range []string{"SOPS_AGE_KEY_CMD", "SOPS_AGE_SSH_PRIVATE_KEY_CMD"} {
		if command, ok := os.LookupEnv(name); ok && strings.TrimSpace(command) == "" {
			return "", &Error{"invalid-config", "age 密钥命令已设置但为空，请取消设置或提供有效命令"}
		}
	}
	plain, err := decrypt.Data(data, "yaml")
	if err != nil {
		return "", &Error{"decrypt-failed", "SOPS 解密或完整性校验失败，请检查 age 密钥和密文"}
	}
	defer clear(plain)
	doc, err = secretDocument(plain)
	if err != nil {
		return "", err
	}
	return secretField(doc, keys)
}

func secretDocument(data []byte) (map[string]any, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return nil, &Error{"invalid-file", "凭据文件必须是有效的 YAML 对象"}
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, &Error{"invalid-file", "凭据文件只能有一个 YAML 文档"}
	}
	return doc, nil
}

func secretField(doc map[string]any, keys []string) (string, error) {
	var v any = doc
	for _, key := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return "", &Error{"not-found", "凭据字段路径不存在"}
		}
		v, ok = m[key]
		if !ok {
			return "", &Error{"not-found", "凭据字段路径不存在"}
		}
	}
	s, ok := v.(string)
	if !ok {
		return "", &Error{"invalid-value", "凭据字段必须是字符串"}
	}
	return s, nil
}
