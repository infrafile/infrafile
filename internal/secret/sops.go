package secret

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/getsops/sops/v3/aes"
	sopsage "github.com/getsops/sops/v3/age"
	"github.com/getsops/sops/v3/config"
	"github.com/getsops/sops/v3/logging"
	sopsyaml "github.com/getsops/sops/v3/stores/yaml"
	"go.yaml.in/yaml/v3"
)

func init() {
	// SOPS 的诊断可能回显文件注释；所有对外错误由本包生成。
	for _, logger := range logging.Loggers {
		logger.SetOutput(io.Discard)
	}
}

var encryptedString = regexp.MustCompile(`^ENC\[AES256_GCM,data:[A-Za-z0-9+/=]*,iv:[A-Za-z0-9+/=]+,tag:[A-Za-z0-9+/=]+,type:str\]$`)

func (r Resolver) sops(ctx context.Context, ref string, resolve bool) (string, error) {
	// 与台账 schema 一致：原文不含控制字符或空白，文件名里的空格写成 %20。
	if strings.ContainsFunc(ref, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return "", &Error{"invalid-ref", "sops 引用不能含空白或控制字符；文件名里的空格写成 %20"}
	}
	file, key, ok := strings.Cut(strings.TrimPrefix(ref, "sops://"), "#")
	file, err := url.PathUnescape(file)
	if !ok || err != nil || !filepath.IsLocal(file) || strings.ContainsAny(file, "?\\") || strings.ContainsFunc(file, unicode.IsControl) || key == "" || strings.ContainsAny(key, "#?") {
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
	store := sopsyaml.NewStore(&config.YAMLStoreConfig{})
	tree, err := store.LoadEncryptedFile(data)
	if err != nil || !encryptedString.MatchString(tree.Metadata.MessageAuthenticationCode) {
		return "", &Error{"invalid-file", "需要带 age 接收者和加密 MAC 的 SOPS YAML 文件"}
	}
	if len(tree.Metadata.KeyGroups) != 1 || len(tree.Metadata.KeyGroups[0]) == 0 {
		return "", &Error{"unsupported", "M3 仅支持单组 age 密文，不调用云 KMS 或 PGP"}
	}
	// 使用与解密相同的上游解析结果，避免大小写等不同写法绕过后端限制。
	for _, masterKey := range tree.Metadata.KeyGroups[0] {
		key, ok := masterKey.(*sopsage.MasterKey)
		if !ok {
			return "", &Error{"unsupported", "M3 仅支持单组 age 密文，不调用云 KMS 或 PGP"}
		}
		if key.Recipient == "" || key.EncryptedKey == "" {
			return "", &Error{"invalid-file", "age 接收者或加密数据密钥为空"}
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
	// 上游 decrypt.Data 不传递 context；自行加载 age 密钥，再沿用 SOPS 解密和 MAC 校验。
	var dataKey []byte
	for _, masterKey := range tree.Metadata.KeyGroups[0] {
		key := masterKey.(*sopsage.MasterKey)
		identities, err := ageIdentities(ctx, key.Recipient)
		if err != nil {
			return "", err
		}
		if len(identities) == 0 {
			continue
		}
		identities.ApplyToMasterKey(key)
		dataKey, err = key.Decrypt()
		if err == nil {
			break
		}
	}
	if ctx.Err() != nil {
		return "", &Error{"cancelled", "凭据操作已取消"}
	}
	if len(dataKey) == 0 {
		return "", &Error{"decrypt-failed", "SOPS 解密或完整性校验失败，请检查 age 密钥和密文"}
	}
	defer clear(dataKey)
	cipher := aes.NewCipher()
	mac, err := tree.Decrypt(dataKey, cipher)
	if err != nil {
		return "", &Error{"decrypt-failed", "SOPS 解密或完整性校验失败，请检查 age 密钥和密文"}
	}
	originalMAC, err := cipher.Decrypt(tree.Metadata.MessageAuthenticationCode, dataKey, tree.Metadata.LastModified.Format(time.RFC3339))
	if err != nil || originalMAC != mac {
		return "", &Error{"decrypt-failed", "SOPS 解密或完整性校验失败，请检查 age 密钥和密文"}
	}
	plain, err := store.EmitPlainFile(tree.Branches)
	if err != nil {
		return "", &Error{"decrypt-failed", "SOPS 明文结构无法解析"}
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
