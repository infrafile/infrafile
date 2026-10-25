package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestImportSignalWrapper(t *testing.T) {
	if os.Getenv("INFRAFILE_IMPORT_SIGNAL_TEST") != "1" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := newRoot(time.Now())
	args := []string{"import", "ssh-config", "--file", "ssh.conf", "--write"}
	command, _, err := root.Find(args)
	if err != nil {
		t.Fatal(err)
	}
	// Handshake after installing the handler, so the signal cannot kill startup.
	command.PreRun = func(*cobra.Command, []string) {
		fmt.Fprintln(os.Stdout, "ready")
		<-ctx.Done()
	}
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func TestImportSSHStopsOnSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM 是 POSIX 进程信号")
	}
	emptyRepo(t)
	writeImportFile(t, "ssh.conf", "Host sample\n HostName 192.0.2.1\n")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	parent := exec.CommandContext(ctx, exe, "-test.run=^TestImportSignalWrapper$")
	parent.Env = append(os.Environ(), "INFRAFILE_IMPORT_SIGNAL_TEST=1")
	var stderr bytes.Buffer
	parent.Stderr = &stderr
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if parent.ProcessState == nil {
			_ = parent.Process.Kill()
			_ = parent.Wait()
		}
	}()
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("import not ready: %q %v", line, err)
	}
	if err := parent.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err = parent.Wait()
	if ctx.Err() != nil || err == nil || parent.ProcessState.ExitCode() != 2 || !strings.Contains(stderr.String(), "context canceled") {
		t.Fatalf("cancelled import: err=%v timeout=%v stderr=%s", err, ctx.Err(), &stderr)
	}
	if _, err := os.Stat("inventory"); !os.IsNotExist(err) {
		t.Fatal("cancelled import wrote inventory")
	}
}

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
