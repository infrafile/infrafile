package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseDocumentExpansionLimit(t *testing.T) {
	var input strings.Builder
	input.WriteString("n0: &n0 [x, x, x, x]\n")
	for i := 1; i < 12; i++ {
		fmt.Fprintf(&input, "n%d: &n%d [%s]\n", i, i, strings.TrimSuffix(strings.Repeat(fmt.Sprintf("*n%d, ", i-1), 4), ", "))
	}
	doc, problems, err := ParseDocument(context.Background(), "config.yaml", []byte(input.String()))
	if err != nil || doc != nil || len(problems) != 1 || !strings.Contains(problems[0].Message, "100000") {
		t.Fatalf("expansion not rejected: doc=%v problems=%v err=%v", doc != nil, problems, err)
	}
}

func TestParseDocumentCyclesAndDepth(t *testing.T) {
	for _, input := range []string{"root: &root [*root, *root]\n", strings.Repeat("[", 101) + "x" + strings.Repeat("]", 101)} {
		doc, problems, err := ParseDocument(context.Background(), "config.yaml", []byte(input))
		if err != nil || doc != nil || len(problems) != 1 || problems[0].Code != CodeParse {
			t.Fatalf("unsafe expansion: doc=%v problems=%v err=%v", doc != nil, problems, err)
		}
	}
}

func TestParseDocumentExpandedPathsAreBounded(t *testing.T) {
	var input strings.Builder
	input.WriteString("n0: &n0 value\n")
	for i := 1; i < 9; i++ {
		fmt.Fprintf(&input, "n%d: &n%d {%s: [%s]}\n", i, i, strings.Repeat("k", 512), strings.TrimSuffix(strings.Repeat(fmt.Sprintf("*n%d, ", i-1), 4), ", "))
	}
	doc, problems, err := ParseDocument(context.Background(), "config.yaml", []byte(input.String()))
	if err != nil || doc != nil || len(problems) != 1 || !strings.Contains(problems[0].Message, "16 MiB") {
		t.Fatalf("expanded paths not bounded: doc=%v problems=%v err=%v", doc != nil, problems, err)
	}
}

func TestParseDocumentSharedAliasPreservesValuesAndLines(t *testing.T) {
	doc, problems, err := ParseDocument(context.Background(), "config.yaml", []byte("original: &value {date: 2027-01-01, port: 22}\ncopy: *value\n"))
	if err != nil || len(problems) != 0 || doc == nil {
		t.Fatalf("alias rejected: %v %v", problems, err)
	}
	value := doc.Value.(map[string]any)
	for _, key := range []string{"original", "copy"} {
		m := value[key].(map[string]any)
		if m["date"] != "2027-01-01" || m["port"] != 22 || doc.Line(key+".date") != 1 {
			t.Fatalf("alias values or lines changed: %v", m)
		}
	}
}

// Cancel synchronously during work instead of relying on scheduler timing.
type cancelDuringParse struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelDuringParse) Err() error {
	c.checks++
	if c.checks == 20 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestParseDocumentCancellation(t *testing.T) {
	for _, input := range []string{
		"root: &base [a, b, c, d]\ncopy: [*base, *base, *base, *base, *base]\n",
		strings.Repeat("# comment\n", 20_000) + "value: ok\n",
	} {
		ctx, cancel := context.WithCancel(context.Background())
		c := &cancelDuringParse{Context: ctx, cancel: cancel}
		doc, problems, err := ParseDocument(c, "config.yaml", []byte(input))
		cancel()
		if !errors.Is(err, context.Canceled) || doc != nil || len(problems) != 0 {
			t.Fatalf("cancelled parse: doc=%v problems=%v err=%v", doc != nil, problems, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Load(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("load ignored cancellation: %v", err)
	}
}
