package change

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func processAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == windows.ERROR_INVALID_PARAMETER {
		return false
	}
	if err != nil {
		return true
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if windows.GetExitCodeProcess(handle, &code) != nil {
		return true
	}
	return code == 259
}

func supportsEdits() error {
	return errors.New("Windows 尚未支持台账编辑事务的权限与恢复保证，请使用 Linux/macOS；只读功能保持可用")
}

func lockRecovery(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}
