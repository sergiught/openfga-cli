package dsl

import "testing"

func TestDiagnosticsValidModelReturnsNil(t *testing.T) {
	src := "model\n  schema 1.1\ntype user\ntype document\n  relations\n    define viewer: [user]\n"
	if got := Diagnostics(src); got != nil {
		t.Fatalf("expected nil diagnostics for valid model, got %+v", got)
	}
}

func TestDiagnosticsReportsSyntaxErrorPosition(t *testing.T) {
	// Missing ':' after the relation name on the 6th line (0-based line index 5).
	src := "model\n  schema 1.1\ntype user\ntype document\n  relations\n    define viewer [user]\n"
	got := Diagnostics(src)
	if len(got) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %+v", len(got), got)
	}
	want := Diagnostic{Line: 5, Col: 18, Msg: "missing ':' at '['"}
	if got[0] != want {
		t.Fatalf("diagnostic mismatch:\n got %+v\nwant %+v", got[0], want)
	}
}

func TestDiagnosticsMultipleErrorsAreInSourceOrder(t *testing.T) {
	// Two relations, each missing the ':' after the relation name, on
	// consecutive lines. Pins the assumption that diags[0] is the top-most
	// (lowest-line) error, as relied on by the playground footer.
	src := "model\n  schema 1.1\ntype user\ntype document\n  relations\n    define viewer [user]\n    define editor [user]\n"
	got := Diagnostics(src)
	if len(got) < 2 {
		t.Fatalf("expected at least 2 diagnostics, got %d: %+v", len(got), got)
	}
	if got[0].Line > got[1].Line {
		t.Fatalf("expected diagnostics in source order, got diags[0].Line=%d > diags[1].Line=%d", got[0].Line, got[1].Line)
	}
}
