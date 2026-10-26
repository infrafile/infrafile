package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

func runChild(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// 自己建输出管道：交给 exec 拷贝时，子进程非零退出会让 ExitError 盖住 ErrWaitDelay，
	// 看不出子孙进程占着管道、输出没收完。
	outR, outW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer outR.Close()
	errR, errW, err := os.Pipe()
	if err != nil {
		outW.Close()
		return err
	}
	defer errR.Close()
	stdout, stderr := cmd.Stdout, cmd.Stderr
	cmd.Stdout, cmd.Stderr = outW, errW
	// 非 *os.File 的 stdin 仍由 exec 拷贝，不能让它无限期阻塞 Wait。
	cmd.WaitDelay = time.Second
	kill, err := startRunProcess(cmd)
	outW.Close()
	errW.Close()
	if err != nil {
		return err
	}
	var copies sync.WaitGroup
	var copyErr error
	var once sync.Once
	for _, c := range []struct {
		dst io.Writer
		src *os.File
	}{{stdout, outR}, {stderr, errR}} {
		copies.Add(1)
		go func() {
			defer copies.Done()
			if _, err := io.Copy(c.dst, c.src); err != nil {
				once.Do(func() { copyErr = err })
			}
			// Like exec: once we stop reading, the child gets EPIPE instead of blocking.
			c.src.Close()
		}()
	}
	copied := make(chan struct{})
	go func() {
		copies.Wait()
		close(copied)
	}()
	// Keep cancellation active while draining pipes, even after the direct child exits.
	stop := context.AfterFunc(ctx, kill)
	defer stop()
	defer kill()
	waitErr := cmd.Wait()
	select {
	case <-copied:
	case <-time.After(time.Second):
		// Close our read ends so abandoned descendants holding the pipes cannot block us.
		outR.Close()
		errR.Close()
		<-copied
		return exec.ErrWaitDelay
	}
	if waitErr != nil {
		return waitErr
	}
	if copyErr != nil {
		return copyErr
	}
	return ctx.Err()
}
