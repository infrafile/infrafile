package ledger

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWithoutSecretsCopiesNestedRecords(t *testing.T) {
	original := map[string]any{"metadata": map[string]any{"name": "home"}, "spec": map[string]any{"secrets": map[string]any{"token": "env://EXAMPLE_TOKEN"}, "x-items": []any{map[string]any{"secrets": map[string]any{"password": "op://Example/Item/password"}, "id": json.Number("9007199254740993")}}}}
	out := WithoutSecrets(original)
	b, err := json.Marshal(out)
	if err != nil || strings.Contains(string(b), "secrets") || strings.Contains(string(b), "env://") || !strings.Contains(string(b), "9007199254740993") {
		t.Fatal("redaction changed safe data", err)
	}
	if _, ok := original["spec"].(map[string]any)["secrets"]; !ok {
		t.Fatal("redaction mutated loaded ledger")
	}
}
