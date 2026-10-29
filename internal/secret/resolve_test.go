package secret_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/getsops/sops/v3/config"
	sopsyaml "github.com/getsops/sops/v3/stores/yaml"
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
	// 台账 schema 拒绝的空白和控制字符，解析端也按格式错误拒绝，不去读文件。
	for _, bad := range []string{
		"sops://secrets.enc.yaml#database.password hunter2", "sops://secrets.enc.yaml#database\u0085password",
		"sops://secrets%7f.enc.yaml#database.password", "sops://secrets.enc.yaml#database.password\x7f",
	} {
		var failure *secret.Error
		if err := r.Check(ctx, bad); !errors.As(err, &failure) || failure.Code != "invalid-ref" {
			t.Errorf("%q 应报 invalid-ref，得到 %v", bad, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "secrets.enc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	store := sopsyaml.NewStore(&config.YAMLStoreConfig{})
	tree, err := store.LoadEncryptedFile(data)
	if err != nil {
		t.Fatal(err)
	}
	mac := tree.Metadata.MessageAuthenticationCode
	badMAC := strings.Replace(string(data), mac, strings.Replace(mac, "data:", "data:AAAA", 1), 1)
	if err := os.WriteFile(filepath.Join(dir, "secrets.enc.yaml"), []byte(badMAC), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(ctx, ref); err == nil || strings.Contains(err.Error(), want) {
		t.Fatal("字段密文有效但 MAC 被篡改时未安全拒绝")
	}
	corrupt := strings.Replace(string(data), "password: ENC[AES256_GCM,data:", "password: ENC[AES256_GCM,data:AAAA", 1)
	if err := os.WriteFile(filepath.Join(dir, "secrets.enc.yaml"), []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(ctx, ref); err == nil || strings.Contains(err.Error(), want) {
		t.Fatal("篡改密文未被拒绝或错误泄露凭据")
	}
}

func TestSOPSKeyCommandsRespectContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("密钥命令夹具使用 POSIX shell")
	}
	testutil.IsolateKeys(t)
	dir := t.TempDir()
	ref, want, identity := testutil.EncryptedFile(t, dir)
	r := secret.Resolver{Dir: dir}
	command := filepath.Join(dir, "key-command")
	script := "#!/bin/sh\ncase \"$INFRAFILE_KEY_MODE\" in\n hang) exec sleep 1 ;;\n fail) echo synthetic-private-error >&2; exit 1 ;;\n *) test -n \"$SOPS_AGE_RECIPIENT\" || exit 9; printf '%s\\n' \"$INFRAFILE_TEST_AGE_ID\" ;;\nesac\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INFRAFILE_TEST_AGE_ID", identity)
	for _, name := range []string{"SOPS_AGE_KEY_CMD", "SOPS_AGE_SSH_PRIVATE_KEY_CMD"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, command)
			t.Setenv("INFRAFILE_KEY_MODE", "hang")
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			value, err := r.Resolve(ctx, ref)
			var failure *secret.Error
			if !errors.As(err, &failure) || failure.Code != "cancelled" || value != "" || time.Since(start) > 500*time.Millisecond {
				t.Fatalf("key command exceeded cancellation: elapsed=%s error=%v", time.Since(start), err)
			}
		})
	}
	t.Setenv("SOPS_AGE_KEY_CMD", command)
	value, err := r.Resolve(context.Background(), ref)
	if err != nil || value != want {
		t.Fatal("密钥命令未能解密")
	}
	t.Setenv("INFRAFILE_KEY_MODE", "fail")
	t.Setenv("SOPS_AGE_KEY", identity)
	value, err = r.Resolve(context.Background(), ref)
	if err != nil || value != want {
		t.Fatal("密钥命令失败不应阻止其他已配置密钥")
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

func TestSOPSOfflineRejectsLinksAndOversizedFiles(t *testing.T) {
	for _, name := range []string{"symlink", "oversized"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			ref, _, _ := testutil.EncryptedFile(t, dir)
			if name == "symlink" {
				if err := os.Symlink("secrets.enc.yaml", filepath.Join(dir, "link.enc.yaml")); err != nil {
					t.Skip("symlinks unavailable")
				}
				ref = "sops://link.enc.yaml#database.password"
			} else {
				file := filepath.Join(dir, "secrets.enc.yaml")
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				data = append(data, []byte("\n#"+strings.Repeat("x", 4<<20)+"\n")...)
				if err := os.WriteFile(file, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := (secret.Resolver{Dir: dir}).CheckOffline(context.Background(), ref); err == nil {
				t.Fatal("accepted unbounded or linked credential file")
			}
		})
	}
}

func TestSOPSRejectsCaseInsensitiveKMSMetadata(t *testing.T) {
	dir := t.TempDir()
	ref, _, _ := testutil.EncryptedFile(t, dir)
	file := filepath.Join(dir, "secrets.enc.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("\nsops:\n"), []byte("\nsops:\n  KMS:\n    - arn: arn:aws:kms:us-east-1:000000000000:key/00000000-0000-0000-0000-000000000000\n      created_at: \"2026-01-01T00:00:00Z\"\n      enc: AAAAAA==\n"), 1)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sopsyaml.NewStore(&config.YAMLStoreConfig{}).LoadEncryptedFile(data); err != nil {
		t.Fatalf("合成 KMS 夹具无效：%v", err)
	}
	r := secret.Resolver{Dir: dir}
	err = r.Check(context.Background(), ref)
	var failure *secret.Error
	if !errors.As(err, &failure) || failure.Code != "unsupported" {
		t.Fatalf("未按密钥类型拒绝混合大小写的 KMS 元数据：%v", err)
	}
	if _, err := r.Resolve(context.Background(), ref); !errors.As(err, &failure) || failure.Code != "unsupported" {
		t.Fatal("解析路径未在调用 KMS 前拒绝元数据")
	}
}
