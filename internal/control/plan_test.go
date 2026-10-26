package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
