package secret_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/secret"
	"github.com/infrafile/infrafile/internal/testutil"
)

func TestEnvironment(t *testing.T) {
	t.Setenv("INFRAFILE_TEST_SECRET", "synthetic-environment-value")
	r := secret.Resolver{Dir: t.TempDir()}
	if err := r.Check(context.Background(), "env://INFRAFILE_TEST_SECRET"); err != nil {
		t.Fatal(err)
	}
	value, err := r.Resolve(context.Background(), "env://INFRAFILE_TEST_SECRET")
	if err != nil || value != "synthetic-environment-value" {
		t.Fatal("未解析出环境变量")
	}
	for _, ref := range []string{"env://", "env://BAD/NAME", "env://BAD?value=1", "env://INFRAFILE_TEST_MISSING", "http://example.com/value"} {
		if err := r.Check(context.Background(), ref); err == nil {
			t.Errorf("接受了无效或缺失的引用 %q", ref)
		}
	}
}

func TestOnePassword(t *testing.T) {
	testutil.FakeOp(t)
	t.Setenv("INFRAFILE_TEST_OP_VALUE", "synthetic-op-value")
	r := secret.Resolver{Dir: t.TempDir()}
	ctx := context.Background()
	if err := r.Check(ctx, "op://Test/Item/password"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(ctx, "op://Test/Item/password")
	if err != nil || got != "synthetic-op-value" {
		t.Fatal("op 取值失败")
	}
	for _, ref := range []string{"op://Test/Item/missing", "op://Test/Item/password?attribute=value", "op://Test//password", "op://Test/Item/password\n"} {
		if err := r.Check(ctx, ref); err == nil || strings.Contains(err.Error(), "synthetic-op-value") {
			t.Fatal("未安全拒绝无效 op 引用")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := r.Resolve(ctx, "op://Test/Slow/password"); err == nil {
		t.Fatal("op 超时未取消")
	}
}

func TestSOPS(t *testing.T) {
	testutil.IsolateKeys(t)
	dir := t.TempDir()
	ref, want, identity := testutil.EncryptedFile(t, dir)
	r := secret.Resolver{Dir: dir}
	ctx := context.Background()
	if err := r.Check(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(ctx, ref); err == nil {
		t.Fatal("没有密钥却解密成功")
	}
	t.Setenv("SOPS_AGE_KEY", identity)
	for _, name := range []string{"SOPS_AGE_KEY_CMD", "SOPS_AGE_SSH_PRIVATE_KEY_CMD"} {
		t.Setenv(name, " ")
		if _, err := r.Resolve(ctx, ref); err == nil {
			t.Fatal("没有拒绝空密钥命令")
		}
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.Resolve(ctx, ref)
	if err != nil || got != want {
		t.Fatal("SOPS 解密没有得到期望值")
	}
	for _, bad := range []string{
		"sops://../secrets.enc.yaml#database.password", "sops:///secrets.enc.yaml#database.password",
		"sops://secrets.enc.yaml", "sops://secrets.enc.yaml#database.missing", "sops://secrets.enc.yaml#sops.mac",
	} {
		if err := r.Check(ctx, bad); err == nil {
			t.Errorf("接受了无效引用 %q", bad)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "secrets.enc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	corrupt := strings.Replace(string(data), "password: ENC[AES256_GCM,data:", "password: ENC[AES256_GCM,data:AAAA", 1)
	if err := os.WriteFile(filepath.Join(dir, "secrets.enc.yaml"), []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(ctx, ref); err == nil || strings.Contains(err.Error(), want) {
		t.Fatal("篡改密文未被拒绝或错误泄露凭据")
	}
}

func TestSOPSRejectsPlaintextAndEscapingSymlinks(t *testing.T) {
	dir := t.TempDir()
	r := secret.Resolver{Dir: dir}
	ref, _, _ := testutil.EncryptedFile(t, dir)
	data, err := os.ReadFile(filepath.Join(dir, "secrets.enc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	start := bytes.Index(data, []byte("ENC[AES256_GCM,data:"))
	end := start + bytes.IndexByte(data[start:], ']') + 1
	plain := append(append([]byte(nil), data[:start]...), append([]byte("unprotected-test-value"), data[end:]...)...)
	if err := os.WriteFile(filepath.Join(dir, "secrets.enc.yaml"), plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(context.Background(), ref); err == nil || strings.Contains(err.Error(), "unprotected-test-value") {
		t.Fatal("接受明文或泄露其值")
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside.yaml"), filepath.Join(dir, "escape.yaml")); err != nil {
		t.Skip("系统不支持创建符号链接")
	}
	if err := r.Check(context.Background(), "sops://escape.yaml#database.password"); err == nil {
		t.Fatal("读取了仓外符号链接目标")
	}
}
