package control

import (
	"errors"
	"os"
)

func lockExecution(f *os.File) error {
	return errors.New("Windows 尚未验证执行回执持久化与私有权限；请使用 Linux/macOS")
}
