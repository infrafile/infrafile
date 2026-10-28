package observation

import (
	"reflect"
	"testing"
)

func TestOriginNormalization(t *testing.T) {
	o := Origin{Scope: "global", Sources: []OriginSource{
		{Host: "ORIGIN.EXAMPLE.COM.", Port: 443, Protocol: "https"},
		{Host: "origin.example.com", Port: 443, Protocol: "https"},
		{Host: "::ffff:192.0.2.1", Port: 0, Protocol: "future"},
		{Host: "2001:0db8::1", Port: 65536},
	}}
	o.Normalize()
	want := []OriginSource{{Host: "192.0.2.1"}, {Host: "2001:db8::1"}, {Host: "origin.example.com", Port: 443, Protocol: "https"}}
	if o.Scope != "global" || !reflect.DeepEqual(o.Sources, want) {
		t.Fatalf("%+v", o)
	}
	for _, bad := range []string{"https://user:secret@example.com/path", "origin.example.com:443", "[2001:db8::1]", "fe80::1%eth0", "*.example.com", ""} {
		o := Origin{Scope: "global", Sources: []OriginSource{{Host: bad}}}
		o.Normalize()
		if o.Scope != "unknown" || o.Sources[0].Host != "" {
			t.Fatalf("unsafe: %+v", o)
		}
	}
}
