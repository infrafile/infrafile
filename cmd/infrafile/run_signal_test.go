package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunSignalWrapper(t *testing.T) {
	if os.Getenv("INFRAFILE_SIGNAL_TEST") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(run(os.Args[i+1:], os.Stdout, os.Stderr))
		}
	}
	os.Exit(9)
}

func TestRunSignalChild(t *testing.T) {
	if os.Getenv("INFRAFILE_SIGNAL_TEST") != "1" {
		return
	}
	fmt.Fprintln(os.Stdout, os.Getpid())
	for {
		time.Sleep(time.Hour)
	}
}

func TestRunTerminatesChildOnSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM 是 POSIX 进程信号")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(exe, "-test.run=^TestRunSignalWrapper$", "--", "run", "--secret", "INJECTED=env://INFRAFILE_SIGNAL_VALUE", "--", exe, "-test.run=^TestRunSignalChild$")
	parent.Env = append(os.Environ(), "INFRAFILE_SIGNAL_TEST=1", "INFRAFILE_SIGNAL_VALUE=synthetic-signal-value")
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer parent.Process.Kill()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(out).ReadString('\n'); ready <- strings.TrimSpace(line) }()
	var pid int
	select {
	case line := <-ready:
		pid, err = strconv.Atoi(line)
		if err != nil {
			t.Fatal("子进程未报告 PID")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("子进程未启动")
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Kill()
	if err := parent.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = parent.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("父进程未结束")
	}
	if err := child.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("父进程结束后，持有凭据的子进程仍存活")
	}
}
