//go:build !windows

package main

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func opsPrivateOwner(info os.FileInfo) bool {
	value, ok := info.Sys().(*syscall.Stat_t)
	return ok && value.Uid == uint32(os.Getuid())
}

func opsGenerationLock(path string) (func(), error) {
	return opsFileLease(path, unix.LOCK_EX)
}

func opsRetentionReadLock(path string) (func(), error) {
	return opsFileLease(path, unix.LOCK_SH)
}

func opsFileLease(path string, kind int) (func(), error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	var owner unix.Stat_t
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || unix.Fstat(fd, &owner) != nil || owner.Uid != uint32(os.Getuid()) {
		file.Close()
		return nil, fmt.Errorf("invalid shared lock")
	}
	if err = unix.Flock(fd, kind|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("busy")
	}
	return func() { file.Close() }, nil
}
