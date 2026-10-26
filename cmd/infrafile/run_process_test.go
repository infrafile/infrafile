package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunTreeChild(t *testing.T) {
	if os.Getenv("INFRAFILE_TREE_TEST") != "1" {
		return
	}
	mode, pulse := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	if os.Getenv("INJECTED") != "synthetic-tree-value" {
		os.Exit(9)
	}
	if mode == "leaf" {
		os.WriteFile(pulse+".leaf", []byte(strconv.Itoa(os.Getpid())), 0600)
		for i := 0; i < 1000; i++ {
			if err := os.WriteFile(pulse, []byte(strconv.Itoa(i)), 0600); err != nil {
				os.Exit(10)
			}
			time.Sleep(20 * time.Millisecond)
		}
		os.Exit(0)
	}
	os.WriteFile(pulse+".branch", []byte(strconv.Itoa(os.Getpid())), 0600)
	child := exec.Command(os.Args[0], "-test.run=^TestRunTreeChild$", "--", "leaf", pulse)
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(11)
	}
	if mode != "running" {
		os.Exit(0)
	}
	_ = child.Wait()
	os.Exit(0)
}

func TestRunCleansDescendants(t *testing.T) {
	for _, mode := range []string{"running", "exited", "pipe-timeout"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("INFRAFILE_TREE_TEST", "1")
			t.Setenv("INFRAFILE_TREE_VALUE", "synthetic-tree-value")
			pulse := filepath.Join(t.TempDir(), "pulse")
			ctx, cancel := context.WithCancel(context.Background())
			root := newRoot(time.Now())
			root.SetArgs([]string{"run", "--secret", "INJECTED=env://INFRAFILE_TREE_VALUE", "--", os.Args[0], "-test.run=^TestRunTreeChild$", "--", mode, pulse})
			var out, stderr bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&stderr)
			done := make(chan error, 1)
			go func() { done <- root.ExecuteContext(ctx) }()
			finished := false
			t.Cleanup(func() {
				cancel()
				for _, suffix := range []string{".leaf", ".branch"} {
					data, _ := os.ReadFile(pulse + suffix)
					if pid, err := strconv.Atoi(string(data)); err == nil {
						if process, err := os.FindProcess(pid); err == nil {
							_ = process.Kill()
							_ = process.Release()
						}
					}
				}
				if !finished {
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("run did not finish after cleanup")
					}
				}
			})
			deadline := time.Now().Add(5 * time.Second)
			for {
				if data, _ := os.ReadFile(pulse); len(data) > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("descendant did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mode != "pipe-timeout" {
				cancel()
			}
			select {
			case err := <-done:
				finished = true
				if err == nil {
					t.Fatal("cancelled or pipe-blocked run succeeded")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("run hung on descendant output pipes")
			}
			time.Sleep(100 * time.Millisecond)
			before, _ := os.ReadFile(pulse)
			time.Sleep(100 * time.Millisecond)
			after, _ := os.ReadFile(pulse)
			if len(before) == 0 || !bytes.Equal(before, after) {
				t.Fatal("descendant still running after command completion")
			}
			if strings.Contains(fmt.Sprint(&out, &stderr), "synthetic-tree-value") {
				t.Fatal("secret leaked")
			}
		})
	}
}
