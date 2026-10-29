package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Keep the optional operator helper covered by the existing go-test CI entry point.
func TestAgentAcceptancePreparationContracts(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("operator helper requires Python 3; binary behavior is covered independently")
	}
	script, err := filepath.Abs("../../scripts/test_prepare_agent_acceptance.py")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, python, script).CombinedOutput(); err != nil {
		t.Fatalf("preparation helper contract: %v\n%s", err, out)
	}
}
