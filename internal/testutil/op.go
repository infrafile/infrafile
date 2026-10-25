package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// FakeOp 按真实命令边界模拟字段元数据和取值，脚本里不存凭据。
func FakeOp(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("模拟 op 使用 POSIX shell；Windows 仅做交叉编译")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
test "$1" = read && test "$2" = --no-newline || exit 9
case "$3" in
  'op://Test/Item/password?attribute=id') printf '%s' 'field-id' ;;
  'op://Test/Item/password') printf '%s' "$INFRAFILE_TEST_OP_VALUE" ;;
  'op://Test/Slow/password') exec sleep 30 ;;
  *) printf '%s' "$INFRAFILE_TEST_OP_VALUE" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "op"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
