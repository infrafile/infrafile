//go:build !windows

package main

import (
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

func startRunProcess(cmd *exec.Cmd) (func(), error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	restoreTerminal := func() {}
	if input, ok := cmd.Stdin.(*os.File); ok {
		fd := int(input.Fd())
		if group, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP); err == nil && group == syscall.Getpgrp() {
			// Hand the terminal to the child, then restore it without stopping on SIGTTOU.
			ignored := signal.Ignored(syscall.SIGTTOU)
			signal.Ignore(syscall.SIGTTOU)
			cmd.SysProcAttr.Foreground = true
			cmd.SysProcAttr.Ctty = fd
			restoreTerminal = func() {
				_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, group)
				if !ignored {
					signal.Reset(syscall.SIGTTOU)
				}
			}
		}
	}
	if err := cmd.Start(); err != nil {
		restoreTerminal()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			restoreTerminal()
		})
	}, nil
}
