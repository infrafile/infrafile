package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/internal/testutil"
)

func doctorCLI(t *testing.T, want int, args ...string) []plugin.DoctorCheck {
	t.Helper()
	out := importCLI(t, want, append([]string{"doctor", "--json"}, args...)...)
	var result struct {
		Version    string
		OK, Online bool
		Checks     []plugin.DoctorCheck
	}
	if err := json.Unmarshal(out, &result); err != nil || result.Version != "v1" || result.OK != (want == 0) {
		t.Fatalf("invalid doctor result: %s %v", out, err)
	}
	return result.Checks
}

func doctorConfigFile(t *testing.T, instances string) {
	t.Helper()
	emptyRepo(t)
	writeImportFile(t, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n"+instances)
}

func TestDoctorOfflineNoNetworkHelpersOrWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX helper trap")
	}
	doctorConfigFile(t, "  cloudflare:\n    instances:\n      present: {api_token: 'env://DOCTOR_PRESENT'}\n      absent: {api_token: 'env://DOCTOR_ABSENT'}\n")
	t.Setenv("DOCTOR_PRESENT", "synthetic-private-value")
	t.Setenv("DOCTOR_ABSENT", "")
	os.Unsetenv("DOCTOR_ABSENT")
	testutil.IsolateKeys(t)
	ref, _, _ := testutil.EncryptedFile(t, ".")
	f, _ := os.ReadFile("infrafile.yaml")
	writeImportFile(t, "infrafile.yaml", string(f)+fmt.Sprintf("      encrypted: {api_token: '%s'}\n      password-manager: {api_token: 'op://Test/Item/password'}\n", ref))
	marker, _ := filepath.Abs("helper-ran")
	bin := t.TempDir()
	writeImportFile(t, filepath.Join(bin, "op"), "#!/bin/sh\nprintf ran > '"+marker+"'\nexit 1\n")
	os.Chmod(filepath.Join(bin, "op"), 0o700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SOPS_AGE_KEY_CMD", "touch '"+marker+"'")
	old := http.DefaultTransport
	http.DefaultTransport = cloudReplay(func(*http.Request) (*http.Response, error) {
		t.Error("offline network")
		return nil, errors.New("blocked")
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	before := mcpFiles(t)
	checks := doctorCLI(t, 1)
	codes := map[string]string{}
	for _, c := range checks {
		codes[c.Instance] = c.Code
	}
	if codes["absent"] != "missing-reference" || codes["encrypted"] != "offline-ready" || codes["password-manager"] != "online-required" || codes["present"] != "offline-ready" {
		t.Fatal(checks)
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("doctor changed repository")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("offline helper executed")
	}
}

func TestDoctorOnlineClassificationSelectionAndNoPublication(t *testing.T) {
	t.Setenv("DOCTOR_TOKEN", "synthetic-private-value")
	for _, tc := range []struct {
		name   string
		status int
		body   string
		cause  error
		code   string
		exit   int
	}{
		{"success", 200, `{"success":true,"result":[],"result_info":{"page":1,"total_count":0}}`, nil, "read-succeeded", 0},
		{"auth", 401, "synthetic-private-value", nil, "unauthorized", 1},
		{"permission", 403, "synthetic-private-value", nil, "permission-or-feature-denied", 1},
		{"ambiguous-provider", 200, `{"success":false,"errors":[{"message":"synthetic-private-value"}]}`, nil, "unknown", 1},
		{"malformed", 200, "synthetic-private-value", nil, "unknown", 1},
		{"unreachable", 0, "", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic-private-value")}, "unreachable", 1},
		{"unknown-transport", 0, "", errors.New("synthetic-private-value"), "unknown", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doctorConfigFile(t, "  cloudflare:\n    instances:\n      chosen: {api_token: 'env://DOCTOR_TOKEN'}\n      untouched: {api_token: 'sops://missing.enc.yaml#absent'}\n")
			old := http.DefaultTransport
			calls := 0
			http.DefaultTransport = cloudReplay(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/client/v4/zones" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if tc.cause != nil {
					return nil, tc.cause
				}
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = old })
			before := mcpFiles(t)
			checks := doctorCLI(t, tc.exit, "--online", "--plugin", "cloudflare", "--instance", "chosen")
			if len(checks) != 2 || checks[1].Code != tc.code || calls != 1 {
				t.Fatal(checks, calls)
			}
			out, _ := json.Marshal(checks)
			if strings.Contains(string(out), "synthetic-private-value") {
				t.Fatal("reflected secret")
			}
			if !reflect.DeepEqual(before, mcpFiles(t)) {
				t.Fatal("online doctor published state")
			}
		})
	}
}

func TestDoctorSOPSOnlineAndCancellation(t *testing.T) {
	doctorConfigFile(t, "  cloudflare:\n    instances:\n      chosen: {}\n")
	testutil.IsolateKeys(t)
	ref, value, id := testutil.EncryptedFile(t, ".")
	t.Setenv("SOPS_AGE_KEY", id)
	writeImportFile(t, "infrafile.yaml", fmt.Sprintf("apiVersion: infrafile/v1alpha1\nkind: Config\nplugins:\n  cloudflare:\n    instances:\n      chosen: {api_token: '%s'}\n", ref))
	old := http.DefaultTransport
	// Header whitespace from the synthetic multiline value is checked by a real transport;
	// this replay verifies only in-memory SOPS resolution and safe diagnostics.
	http.DefaultTransport = cloudReplay(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+value {
			t.Error("SOPS credential not used")
		}
		return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(value)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	before := mcpFiles(t)
	checks := doctorCLI(t, 1, "--online", "--plugin", "cloudflare", "--instance", "chosen")
	if checks[1].Code != "permission-or-feature-denied" {
		t.Fatal(checks)
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("SOPS doctor changed files")
	}
	for _, cancelParent := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		http.DefaultTransport = cloudReplay(func(r *http.Request) (*http.Response, error) {
			if cancelParent {
				cancel()
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		root := newRoot(time.Now())
		root.SetArgs([]string{"doctor", "--online", "--plugin", "cloudflare", "--instance", "chosen", "--timeout", "20ms", "--json"})
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		start := time.Now()
		err := root.ExecuteContext(ctx)
		cancel()
		var code exitCode
		if !errors.As(err, &code) || code != 1 || !strings.Contains(stdout.String(), "cancelled-or-timeout") || time.Since(start) > time.Second {
			t.Fatalf("cancellation: %v %s %s", err, &stdout, &stderr)
		}
	}
}

func TestDoctorRequestBudget(t *testing.T) {
	doctorConfigFile(t, "  cloudflare:\n    instances:\n      chosen: {api_token: 'env://DOCTOR_TOKEN'}\n")
	t.Setenv("DOCTOR_TOKEN", "synthetic-private-value")
	old := http.DefaultTransport
	calls := 0
	http.DefaultTransport = cloudReplay(func(r *http.Request) (*http.Response, error) {
		calls++
		body := fmt.Sprintf(`{"success":true,"result":[{"id":"zone-%d","name":"example.com"}],"result_info":{"page":%s,"total_count":100000}}`, calls, r.URL.Query().Get("page"))
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	checks := doctorCLI(t, 1, "--online", "--plugin", "cloudflare", "--instance", "chosen")
	if calls != 20 || checks[1].Code != "request-budget" {
		t.Fatal(calls, checks)
	}
}

func TestDoctorRejectsUnsafeOrAmbiguousInputs(t *testing.T) {
	doctorConfigFile(t, "  ctyun:\n    instances:\n      invalid: {services: [domains]}\n")
	if checks := doctorCLI(t, 1); checks[1].Code != "invalid-config" {
		t.Fatal(checks)
	}
	for _, args := range [][]string{{"--online"}, {"--plugin", "ctyun"}, {"--plugin", "absent", "--instance", "absent"}, {"--timeout", "0s"}, {"--timeout", "3m"}, {"--online", "--plugin", "ctyun", "--instance", "absent"}} {
		var out, errout bytes.Buffer
		if code := run(append([]string{"doctor", "--json"}, args...), &out, &errout); code != 2 || out.Len() != 0 {
			t.Fatalf("%v: %d %s %s", args, code, &out, &errout)
		}
	}
	writeImportFile(t, "infrafile.yaml", "apiVersion: infrafile/v1alpha1\nkind: Config\napi_token: synthetic-private-value\n")
	checks := doctorCLI(t, 1)
	if len(checks) != 1 || checks[0].Code != "invalid-repository" {
		t.Fatal(checks)
	}
}

func TestDoctorAllBuiltinConfigsOffline(t *testing.T) {
	firstPartyRepo(t)
	f, err := os.ReadFile("infrafile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	writeImportFile(t, "infrafile.yaml", string(f)+"  dns:\n    instances:\n      chosen: {host: example.com}\n  tls:\n    instances:\n      chosen: {host: example.com}\n  ssh:\n    instances:\n      chosen: {host: example.com, user: demo, port: 22}\n  whois:\n    instances:\n      chosen: {domain: example.com, server: 'whois.example.com:43'}\n")
	old := http.DefaultTransport
	http.DefaultTransport = cloudReplay(func(*http.Request) (*http.Response, error) {
		t.Error("offline network")
		return nil, errors.New("blocked")
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	checks := doctorCLI(t, 0)
	if len(checks) != 10 {
		t.Fatal(checks)
	}
	for _, check := range checks[1:] {
		if len(check.Services) == 0 || len(check.Permissions) == 0 {
			t.Fatal(check)
		}
	}
}

func TestDoctorDoesNotExecuteExternalPlugin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable trap")
	}
	doctorConfigFile(t, "  external-fixture:\n    instances:\n      chosen: {}\n")
	marker, _ := filepath.Abs("external-ran")
	bin := t.TempDir()
	file := filepath.Join(bin, "external-fixture")
	writeImportFile(t, file, "#!/bin/sh\nprintf ran > '"+marker+"'\nexit 1\n")
	if err := os.Chmod(file, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	checks := doctorCLI(t, 1)
	if checks[1].Code != "external-unverified" {
		t.Fatal(checks)
	}
	checks = doctorCLI(t, 1, "--online", "--plugin", "external-fixture", "--instance", "chosen")
	if checks[1].Code != "unsupported" {
		t.Fatal(checks)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("external plugin executed")
	}
}
