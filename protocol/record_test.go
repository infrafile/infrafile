package protocol

import (
	"encoding/json"
	"testing"
)

func TestRecordEnvelope(t *testing.T) {
	for _, kind := range []string{"FindingHistory", "EditTransaction", "ExecutionReceipt"} {
		data, _ := json.Marshal(map[string]any{"version": "v1", "kind": kind, "data": map[string]any{}})
		if err := Validate("record", data); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	for _, data := range []string{
		`{"version":"v2","kind":"FindingHistory","data":{}}`,
		`{"version":"v1","kind":"Snapshot","data":{}}`,
		`{"version":"v1","kind":"FindingHistory"}`,
		`{"version":"v1","kind":"FindingHistory","data":null}`,
		`{"version":"v1","kind":"FindingHistory","data":[]}`,
		`{"version":"v1","kind":"FindingHistory","data":{},"extra":true}`,
	} {
		if err := Validate("record", json.RawMessage(data)); err == nil {
			t.Fatalf("accepted invalid envelope: %s", data)
		}
	}
	if err := Validate("unknown", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown schema accepted")
	}
}
