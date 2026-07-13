package playground

import (
	"errors"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/dsl"
)

func TestApplyErrorWhileEditingHasNoToast(t *testing.T) {
	m := newPaneModel("type user\n", 80)
	m.editorOpen = true
	updated, _ := m.Update(modelAppliedMsg{err: errors.New("boom")})
	nm := updated.(Model)
	if nm.editorErr == "" {
		t.Fatal("expected editorErr to be set for footer display")
	}
	if nm.toasts.Active() {
		t.Fatal("expected no apply-error toast while the editor is open")
	}
}

func TestApplyErrorWhileClosedShowsToast(t *testing.T) {
	m := newPaneModel("type user\n", 80)
	m.editorOpen = false
	updated, _ := m.Update(modelAppliedMsg{err: errors.New("boom")})
	if !updated.(Model).toasts.Active() {
		t.Fatal("expected an apply-error toast when the editor is closed")
	}
}

func TestRefreshDiagnosticsFlagsUndefinedTypeWhenSyntaxValid(t *testing.T) {
	// Valid syntax: type user and type document declared, but role references undefined type reader
	valid := "model\n  schema 1.1\ntype user\ntype document\n  relations\n    define viewer: [reader]\n"
	m := newPaneModel(valid, 80)

	// Precondition: the DSL is syntactically valid
	syntaxDiags := dsl.Diagnostics(m.editor.Value())
	if len(syntaxDiags) > 0 {
		t.Fatalf("precondition: expected syntactically valid DSL, got %d syntax errors", len(syntaxDiags))
	}

	// Call refreshEditorDiagnostics which should run the semantic check
	m.refreshEditorDiagnostics()

	// Verify that undefined-type diagnostics were captured
	if len(m.editorDiags) == 0 {
		t.Fatal("expected undefined-type diagnostics when syntax is valid")
	}

	// Verify the diagnostic is about the undefined type
	if !strings.Contains(m.editorDiags[0].Msg, "undefined type") {
		t.Fatalf("expected 'undefined type' in diagnostic message, got: %s", m.editorDiags[0].Msg)
	}
}

func TestRefreshDiagnosticsSyntaxTakesPrecedenceOverSemantic(t *testing.T) {
	// Invalid syntax (missing type keyword value) AND undefined type
	invalid := "model\n  schema 1.1\ntype\ntype document\n  relations\n    define viewer: [undefined]\n"
	m := newPaneModel(invalid, 80)

	// Precondition: the DSL has syntax errors
	syntaxDiags := dsl.Diagnostics(m.editor.Value())
	if len(syntaxDiags) == 0 {
		t.Fatalf("precondition: expected syntax errors in test DSL")
	}

	// Call refreshEditorDiagnostics
	m.refreshEditorDiagnostics()

	// Verify that only syntax errors are shown, not semantic errors
	if len(m.editorDiags) == 0 {
		t.Fatal("expected syntax diagnostics to be present")
	}

	// All diagnostics should be syntax errors, not semantic errors
	for _, diag := range m.editorDiags {
		if strings.Contains(diag.Msg, "undefined type") {
			t.Fatal("expected no undefined-type diagnostics when there are syntax errors")
		}
	}
}
