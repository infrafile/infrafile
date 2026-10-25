package secret_test

import (
	"bytes"
	"testing"

	"github.com/infrafile/infrafile/internal/secret"
)

func TestRedactionAcrossWrites(t *testing.T) {
	input := "s:abcdef;bcde;abc;ab"
	for cut := 0; cut <= len(input); cut++ {
		var out bytes.Buffer
		w := secret.NewRedactor(&out, []string{"abc", "abcdef", "bcde", ""})
		if _, err := w.Write([]byte(input[:cut])); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(input[cut:])); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if out.String() != "s:[REDACTED];[REDACTED];[REDACTED];ab" {
			t.Errorf("分块位置 %d 的遮盖结果不符", cut)
		}
	}
}
