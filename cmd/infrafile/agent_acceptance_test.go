package main

import (
	"context"
	"encoding/json"
	"os"
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

func TestAgentAcceptanceInventoryUsesActualCLIContract(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("operator helper requires Python 3")
	}
	script, err := filepath.Abs("../../scripts/prepare_agent_acceptance.py")
	if err != nil {
		t.Fatal(err)
	}
	repo := changeRepo(t)
	before := changeGit(t, repo, "rev-parse", "HEAD")
	base := t.TempDir()
	request := filepath.Join(base, "request.json")
	outdir := filepath.Join(base, "evidence")
	data := `{"version":"v1","records":[{"apiVersion":"infrafile/v1alpha1","kind":"Account","metadata":{"name":"home"},"spec":{"provider":"example","owner":"reviewed","secrets":{"token":"env://EXAMPLE_TOKEN"}}}]}`
	if err := os.WriteFile(request, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("INFRAFILE_MCP_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, python, script, "inventory", "--binary", binary, "--dir", repo, "--request", request, "--output", outdir).CombinedOutput()
	if err != nil {
		t.Fatalf("actual CLI preparation failed: %v\n%s", err, out)
	}
	var summary struct {
		Prepared bool `json:"prepared"`
		Executed bool `json:"executed"`
	}
	if json.Unmarshal(out, &summary) != nil || !summary.Prepared || summary.Executed {
		t.Fatal("wrong preparation result")
	}
	if changeGit(t, repo, "rev-parse", "HEAD") != before || changeGit(t, repo, "status", "--porcelain") != "" {
		t.Fatal("preparation changed Git or inventory")
	}
}
