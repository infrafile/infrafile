package plugin

import (
	"context"
	"errors"
	"testing"

	"github.com/infrafile/infrafile/internal/observation"
)

func TestOriginAddressAndBudget(t *testing.T) {
	for _, tc := range []struct {
		value, host string
		port        int
		valid       bool
	}{
		{"EXAMPLE.COM.:8443:10", "example.com", 8443, true},
		{"192.0.2.1::50", "192.0.2.1", 0, true},
		{"2001:0db8::1", "2001:db8::1", 0, true},
		{"example.com:0", "", 0, false},
		{"example.com:65536", "", 0, false},
		{"example.com:443:101", "", 0, false},
		{"[2001:db8::1]:443", "", 0, false},
		{"https://user:private@example.com", "", 0, false},
	} {
		host, port, ok := tencentOriginAddress(tc.value)
		if host != tc.host || port != tc.port || ok != tc.valid {
			t.Fatalf("%s: %s %d %v", tc.value, host, port, ok)
		}
	}
	calls := 0
	get := func(context.Context, cloudAuth, cdnEntry) (*observation.Origin, error) {
		calls++
		return &observation.Origin{Scope: "global", Sources: []observation.OriginSource{{Host: "origin.example.com"}}}, nil
	}
	if err := collectCDNOrigins(context.Background(), cloudAuth{}, make([]cdnEntry, 201), get); err == nil || calls != 0 {
		t.Fatalf("budget: %d %v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := collectCDNOrigins(ctx, cloudAuth{}, make([]cdnEntry, 1), get); err == nil || calls != 0 {
		t.Fatalf("cancel: %d %v", calls, err)
	}
	if err := collectCDNOrigins(context.Background(), cloudAuth{}, make([]cdnEntry, 200), get); err != nil || calls != 200 {
		t.Fatalf("boundary: %d %v", calls, err)
	}
	b := &builtin{}
	_, _, err := b.cloudConfig(context.Background(), map[string]any{"services": []string{"domains"}, "collect_origin": true}, []string{"domains", "cdn"})
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "config" {
		t.Fatalf("config must fail before credentials: %v", err)
	}
}
