package mapping

import (
	"context"
	"testing"
)

func TestCompileSourceReportsPositionedDiagnostics(t *testing.T) {
	// A YAML syntax error: mapper only places diagnostics on a line for these.
	src := []byte("version: \"1\"\nrules:\n  - name: r\n    tuples: [\n")
	m, diags, err := CompileSource(src)
	if err == nil || m != nil {
		t.Fatalf("want a compile error, got m=%v err=%v", m, err)
	}
	if len(diags) == 0 || diags[0].Position.StartLine == 0 {
		t.Fatalf("want positioned diagnostics, got %+v", diags)
	}
}

func TestCompileSourceTracesRules(t *testing.T) {
	src := []byte("version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - user: \"user:1\"\n        relation: \"member\"\n        object: \"team:1\"\n")
	m, _, err := CompileSource(src)
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Evaluate(context.Background(), map[string]any{"type": "x"})
	if err != nil || res.Trace == nil {
		t.Fatalf("want a trace, got res=%+v err=%v", res, err)
	}
}
