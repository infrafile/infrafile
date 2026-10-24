package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"无参数", nil, 2, "", "用法"},
		{"version", []string{"version"}, 0, "infrafile dev", ""},
		{"--version", []string{"--version"}, 0, "infrafile dev", ""},
		{"help", []string{"help"}, 0, "用法", ""},
		{"未知命令", []string{"nope"}, 2, "", `未知命令 "nope"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("退出码 = %d，期望 %d", code, tt.wantCode)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q，期望包含 %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q，期望包含 %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
