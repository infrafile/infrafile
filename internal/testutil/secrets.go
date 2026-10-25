// Package testutil 提供只用临时密钥和合成凭据的测试夹具。
package testutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/getsops/sops/v3"
	"github.com/getsops/sops/v3/aes"
	sopsage "github.com/getsops/sops/v3/age"
	"github.com/getsops/sops/v3/config"
	sopsyaml "github.com/getsops/sops/v3/stores/yaml"
)

// EncryptedFile 只写密文；私钥和明文留在测试内存中。
func EncryptedFile(t *testing.T, dir string) (ref, value, identity string) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	key, err := sopsage.MasterKeyFromRecipient(id.Recipient().String())
	if err != nil {
		t.Fatal(err)
	}
	value = "synthetic-sops-value-with-a-newline\nsecond-line"
	tree := sops.Tree{
		Branches: sops.TreeBranches{{{Key: "database", Value: sops.TreeBranch{{Key: "password", Value: value}}}}},
		Metadata: sops.Metadata{KeyGroups: []sops.KeyGroup{{key}}, Version: "3.13.3", LastModified: time.Now().UTC()},
	}
	dataKey, errs := tree.GenerateDataKey()
	if len(errs) != 0 {
		t.Fatal("生成临时数据密钥失败")
	}
	cipher := aes.NewCipher()
	mac, err := tree.Encrypt(dataKey, cipher)
	if err != nil {
		t.Fatal("加密夹具失败")
	}
	tree.Metadata.MessageAuthenticationCode, err = cipher.Encrypt(mac, dataKey, tree.Metadata.LastModified.Format(time.RFC3339))
	if err != nil {
		t.Fatal("加密 MAC 失败")
	}
	store := sopsyaml.NewStore(&config.YAMLStoreConfig{Indent: 2})
	data, err := store.EmitEncryptedFile(tree)
	if err != nil {
		t.Fatal("输出密文失败")
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.enc.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return "sops://secrets.enc.yaml#database.password", value, id.String()
}

// IsolateKeys 隔离测试进程的密钥发现目录，避免 SOPS 自动读取开发者的 age/SSH 私钥。
func IsolateKeys(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(name, dir)
	}
	for _, name := range []string{"SOPS_AGE_KEY", "SOPS_AGE_KEY_CMD", "SOPS_AGE_SSH_PRIVATE_KEY_CMD"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SOPS_AGE_KEY_FILE", filepath.Join(dir, "absent"))
	t.Setenv("SOPS_AGE_SSH_PRIVATE_KEY_FILE", filepath.Join(dir, "absent"))
}
