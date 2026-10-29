//go:build !windows

package change

import (
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func lockRecovery(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func supportsEdits() error          { return nil }
