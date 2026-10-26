package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/protocol"
)

func TestEncodeRejectsPlanBeyondParserLimit(t *testing.T) {
	items := make([]bool, 100_001)
	p := &Plan{Actions: []Action{{Before: map[string]any{"items": items}}}}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) >= maxPlanBytes {
		t.Fatal("fixture must fit the byte limit")
	}
	data, err := p.Encode(context.Background())
	if err == nil || !strings.Contains(err.Error(), "解析限制") || len(data) != 0 {
		t.Fatalf("unreadable plan emitted: bytes=%d err=%v", len(data), err)
	}
}

// 可选的 ruleset_id / rule_id 显式写成空串，与省略等价，不算非规范字段。
func TestReadAcceptsExplicitEmptyTargetIDs(t *testing.T) {
	before := map[string]any{"paused": false}
	after, err := plugin.Desired(before, protocol.Target{ZoneID: "example-zone"}, "cdn-pause", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{Version: "v1", GeneratedAt: "2026-10-25T00:00:00Z", Actions: []Action{{Target: "Resource/edge",
		Binding:    Binding{Plugin: "cloudflare", Instance: "home", Target: protocol.Target{ZoneID: "example-zone"}},
		RecordHash: strings.Repeat("a", 64), ConfigHash: strings.Repeat("b", 64), Operation: "cdn-pause",
		Parameters: map[string]any{}, Before: before, BeforeHash: plugin.Hash(before), After: after}}}
	data, err := p.Encode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"zone_id": "example-zone"`), []byte(`"zone_id": "example-zone", "ruleset_id": "", "rule_id": ""`), 1)
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), path); err != nil {
		t.Fatalf("explicit empty ids rejected: %v", err)
	}
	// 非空的大小写变体仍要拒绝。
	data = bytes.Replace(data, []byte(`"rule_id": ""`), []byte(`"Rule_ID": "x"`), 1)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), path); err == nil {
		t.Fatal("case variant accepted")
	}
}

func TestStaleRecognizesWrappedPluginError(t *testing.T) {
	if !isStale(fmt.Errorf("control: %w", &plugin.Error{Code: "stale", Message: "changed"})) {
		t.Fatal("wrapped stale error not recognized")
	}
	if isStale(&plugin.Error{Code: "timeout"}) || isStale(nil) {
		t.Fatal("non-stale error recognized as stale")
	}
}
