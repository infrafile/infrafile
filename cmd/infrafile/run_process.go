package main

import (
	"context"
	"os/exec"
	"time"
)

func runChild(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd.WaitDelay = time.Second
	kill, err := startRunProcess(cmd)
	if err != nil {
		return err
	}
	// Keep cancellation active while Wait drains pipes, even after the direct child exits.
	stop := context.AfterFunc(ctx, kill)
	defer stop()
	defer kill()
	if err := cmd.Wait(); err != nil {
		return err
	}
	return ctx.Err()
}
