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
