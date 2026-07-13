package playground

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/dsl"
	"github.com/sergiught/openfga-cli/internal/ui/shell"
)

// newPaneModel builds a Model whose editor matches production no-wrap config,
// with a shell sized so contentSize() is deterministic.
func newPaneModel(value string, totalW int) Model {
	sh := shell.New()
	sh.SetSize(totalW, 30)
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.MaxWidth = editorNoWrapWidth
	ta.SetWidth(editorNoWrapWidth)
	ta.SetHeight(10)
	ta.Focus()
	ta.SetValue(value)
	return Model{sh: sh, editor: ta}
}

func TestEditorBodyShowsFooterOnError(t *testing.T) {
	m := newPaneModel("model\n  schema 1.1\ntype user\n  relations\n    define x [user]\n", 80)
	m.editorDiags = dsl.Diagnostics(m.editor.Value())
	if len(m.editorDiags) == 0 {
		t.Fatal("precondition: expected a diagnostic")
	}
	if out := m.editorBody(); !strings.Contains(out, "error line") {
		t.Fatalf("expected footer 'error line', got:\n%s", out)
	}
}

func TestEditorBodyFooterShowsMoreCountForMultipleErrors(t *testing.T) {
	m := newPaneModel("model\n", 80)
	m.editorDiags = []dsl.Diagnostic{{Line: 5, Col: 1, Msg: "a"}, {Line: 6, Col: 1, Msg: "b"}}
	out := m.editorBody()
	if !strings.Contains(out, "(+1 more)") {
		t.Fatalf("expected '(+1 more)' in footer, got:\n%s", out)
	}
	if !strings.Contains(out, "error line 6") { // Line 5 shown 1-based
		t.Fatalf("expected 'error line 6' in footer, got:\n%s", out)
	}
}

func TestEditorPaneHighlightsAndDrawsCursor(t *testing.T) {
	// No trailing newline: after SetValue the cursor rests on line 0 at end-of-line.
	m := newPaneModel("type user", 80)
	// Move to col 2 on line 0: Home, then Right twice.
	m.editor, _ = m.editor.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m.editor, _ = m.editor.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.editor, _ = m.editor.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.editor.Line() != 0 {
		t.Fatalf("precondition: expected cursor on line 0, got %d", m.editor.Line())
	}
	out := m.editorPane(80)
	if !strings.Contains(out, "\x1b[7m") {
		t.Fatalf("expected reverse-video cursor block, got:\n%q", out)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected syntax coloring, got:\n%q", out)
	}
}

func TestEditorPaneErrorMarkerNoRowShift(t *testing.T) {
	m := newPaneModel("model\n  schema 1.1\ntype user\n  relations\n    define x [user]\n", 80)
	m.editorDiags = dsl.Diagnostics(m.editor.Value())
	if len(m.editorDiags) == 0 {
		t.Fatal("precondition: expected a diagnostic")
	}
	out := m.editorPane(80)
	// 5 non-empty source lines + 1 trailing empty line = 6 logical lines; the
	// render must not insert extra rows for the error (no caret row).
	if got := strings.Count(out, "\n") + 1; got != 6 {
		t.Fatalf("expected 6 rows (one per logical line, no inserted caret row), got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "▸") {
		t.Fatalf("expected red gutter marker on the error line, got:\n%s", out)
	}
}

func TestEditorPaneBlurHidesCursor(t *testing.T) {
	m := newPaneModel("type user\n", 80)
	m.editor.Blur()
	if strings.Contains(m.editorPane(80), "\x1b[7m") {
		t.Fatal("expected no cursor block when editor is blurred")
	}
}
