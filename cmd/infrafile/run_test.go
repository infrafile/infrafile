package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/testutil"
)

func TestRunSecretChild(t *testing.T) {
	if os.Getenv("INFRAFILE_TEST_CHILD") != "1" {
		return
	}
	value := os.Getenv("INJECTED")
	digest := sha256.Sum256([]byte(value))
	if hex.EncodeToString(digest[:]) != os.Getenv("INFRAFILE_EXPECT_SHA") {
		os.Exit(9)
	}
	if strings.Contains(strings.Join(os.Args, " "), value) {
		os.Exit(10)
	}
	if os.Getenv("INFRAFILE_GOLDEN_CHILD") == "1" {
		fmt.Fprintln(os.Stdout, "child:"+value)
		code, _ := strconv.Atoi(os.Getenv("INFRAFILE_CHILD_EXIT"))
		os.Exit(code)
	}
	if os.Getenv("INFRAFILE_TEST_LEAK") == "1" {
		fmt.Fprint(os.Stdout, "out:", value[:3])
		fmt.Fprintln(os.Stdout, value[3:])
		fmt.Fprintln(os.Stderr, "err:"+value)
	} else {
		fmt.Fprintln(os.Stdout, "child-ok")
		fmt.Fprintln(os.Stderr, "child-err")
	}
	code, _ := strconv.Atoi(os.Getenv("INFRAFILE_CHILD_EXIT"))
	os.Exit(code)
}

func TestRunRedactsOutputAndPreservesExitCode(t *testing.T) {
	testutil.FakeOp(t)
	value := "synthetic-output-secret\nsecond-line"
	t.Setenv("INFRAFILE_TEST_OP_VALUE", value)
	t.Setenv("INFRAFILE_TEST_CHILD", "1")
	t.Setenv("INFRAFILE_TEST_LEAK", "1")
	t.Setenv("INFRAFILE_CHILD_EXIT", "7")
	digest := sha256.Sum256([]byte(value))
	t.Setenv("INFRAFILE_EXPECT_SHA", hex.EncodeToString(digest[:]))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"env://INFRAFILE_TEST_OP_VALUE", "op://Test/Item/password"} {
		for _, jsonMode := range []bool{false, true} {
			args := []string{"run", "--secret", "INJECTED=" + ref}
			if jsonMode {
				args = append(args, "--json")
			}
			args = append(args, "--", exe, "-test.run=^TestRunSecretChild$")
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != 7 {
				t.Fatalf("退出码 = %d", code)
			}
			if strings.Contains(stdout.String()+stderr.String(), value) {
				t.Fatal("子进程明文泄露到输出")
			}
			if jsonMode {
				var result struct {
					ExitCode int `json:"exit_code"`
				}
				if json.Unmarshal(stdout.Bytes(), &result) != nil || result.ExitCode != 7 {
					t.Fatal("JSON 结果被子进程输出污染")
				}
				if !strings.Contains(stderr.String(), "out:[REDACTED]\n") {
					t.Fatal("子进程 stdout 未送往 stderr")
				}
			} else if stdout.String() != "out:[REDACTED]\n" {
				t.Fatal("stdout 遮盖结果错误")
			}
			if !strings.Contains(stderr.String(), "err:[REDACTED]\n") {
				t.Fatal("stderr 遮盖结果错误")
			}
		}
	}
}

func TestRunInjectsSecretWithoutChangingParent(t *testing.T) {
	testutil.IsolateKeys(t)
	dir := t.TempDir()
	ref, value, key := testutil.EncryptedFile(t, dir)
	t.Setenv("SOPS_AGE_KEY", key)
	t.Setenv("INFRAFILE_TEST_CHILD", "1")
	t.Setenv("INJECTED", "parent-value")
	t.Setenv("TMPDIR", dir)
	digest := sha256.Sum256([]byte(value))
	t.Setenv("INFRAFILE_EXPECT_SHA", hex.EncodeToString(digest[:]))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(dir + "/secrets.enc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"run", "--dir", dir, "--secret", "INJECTED=" + ref, "--", exe, "-test.run=^TestRunSecretChild$"}
	if code := run(args, &stdout, &stderr); code != 0 || stdout.String() != "child-ok\n" || stderr.String() != "child-err\n" {
		t.Fatalf("运行失败：code=%d stdout=%s stderr=%s", code, &stdout, &stderr)
	}
	if os.Getenv("INJECTED") != "parent-value" {
		t.Fatal("修改了父进程环境")
	}
	after, err := os.ReadFile(dir + "/secrets.enc.yaml")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("修改了密文文件")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal("产生了额外文件")
	}
}

func TestRunRejectsInvalidInputBeforeStartingChild(t *testing.T) {
	t.Setenv("INFRAFILE_TEST_PRESENT", "synthetic-unused")
	t.Setenv("INFRAFILE_TEST_CHILD", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{
		{},
		{"--secret", "BAD-NAME=env://INFRAFILE_TEST_PRESENT"},
		{"--secret", "A=env://INFRAFILE_TEST_PRESENT", "--secret", "A=env://INFRAFILE_TEST_PRESENT"},
		{"--secret", "A=env://INFRAFILE_TEST_PRESENT", "--secret", "B=env://INFRAFILE_TEST_ABSENT"},
		{"--secret", "A=literal-must-not-appear"},
	} {
		args := append([]string{"run", "--json"}, flags...)
		args = append(args, "--", exe, "-test.run=^TestRunSecretChild$")
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || strings.Contains(stderr.String(), "literal-must-not-appear") {
			t.Fatalf("失败路径未安全终止：code=%d", code)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"run", "--secret", "A=env://INFRAFILE_TEST_PRESENT", exe}, &stdout, &stderr); code != 2 {
		t.Fatal("未要求 -- 分隔符")
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"run", "--secret", "A=env://INFRAFILE_TEST_PRESENT", "--", "/infrafile-command-does-not-exist"}, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
		t.Fatal("启动失败的输出或退出码不符")
	}
}
