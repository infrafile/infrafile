package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretCheck(t *testing.T) {
	emptyRepo(t)
	t.Setenv("INFRAFILE_CHECK_PRESENT", "synthetic-check-value")
	if err := os.MkdirAll("inventory/accounts", 0o755); err != nil {
		t.Fatal(err)
	}
	data := "apiVersion: infrafile/v1alpha1\nkind: Account\nmetadata:\n  name: test\nspec:\n  provider: example\n  secrets:\n    good: env://INFRAFILE_CHECK_PRESENT\n    missing: env://INFRAFILE_CHECK_ABSENT\n"
	if err := os.WriteFile(filepath.Join("inventory/accounts", "test.yaml"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"secret", "check", "--json"}, &stdout, &stderr)
	var got struct {
		Checked  int `json:"checked"`
		Problems []struct {
			Code string `json:"code"`
			Path string `json:"path"`
		} `json:"problems"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("code=%d stderr=%s", code, &stderr)
	}
	if code != 1 || got.Checked != 2 || len(got.Problems) != 1 || got.Problems[0].Path != "spec.secrets.missing" || got.Problems[0].Code != "not-found" {
		t.Fatalf("检查结果错误：code=%d stdout=%s stderr=%s", code, &stdout, &stderr)
	}
	if bytes.Contains(stdout.Bytes(), []byte("synthetic-check-value")) {
		t.Fatal("检查输出泄露了值")
	}
}
