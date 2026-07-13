package playground

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/dsl"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
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
	out := m.editorPane(80, 10)
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
	out := m.editorPane(80, 10)
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
	if strings.Contains(m.editorPane(80, 10), "\x1b[7m") {
		t.Fatal("expected no cursor block when editor is blurred")
	}
}

// TestResizeReflowsEditorScrollAfterShrink covers the tea.WindowSizeMsg path
// (model.go's resize()): shrinking the editor height must reflow editorTop
// immediately, not leave the cursor's line outside the visible window
// [editorTop, editorTop+Height()) until the next keypress.
func TestResizeReflowsEditorScrollAfterShrink(t *testing.T) {
	value := strings.Repeat("line\n", 30)
	m := newPaneModel(value, 100)
	// resize() touches every section list; give it real (empty) ones instead
	// of nils so driving it through Update below doesn't panic.
	m.profilesList = uilist.New()
	m.storesList = uilist.New()
	m.tuplesList = uilist.New()
	m.changesList = uilist.New()
	m.assertionsList = uilist.New()
	m.paletteList = uilist.New()
	m.modelsList = uilist.New()
	m.editorOpen = true
	m.ready = true

	// A generous size first: the editor is tall enough that the whole buffer
	// fits, so editorTop stays 0.
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = nm.(Model)

	// Move the cursor to the last line directly on the textarea, bypassing
	// the model's own key-driven reflow (handleKey calls reflowEditorScroll
	// after every editor keystroke) so editorTop is left stale relative to
	// the cursor once we shrink below.
	for i := 0; i < 40; i++ {
		m.editor, _ = m.editor.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	last := m.editor.Line()
	if last == 0 {
		t.Fatal("precondition: expected cursor to have moved off line 0")
	}

	// Shrink the terminal drastically: the editor height drops well below
	// the cursor's line.
	nm, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 9})
	m = nm.(Model)

	if m.editorTop > last || last >= m.editorTop+m.editorViewportRows() {
		t.Fatalf("cursor line %d not in view [%d, %d) after resize", last, m.editorTop, m.editorTop+m.editorViewportRows())
	}
}

func TestEditorBodyWrapsLongErrorWithinWidth(t *testing.T) {
	m := newPaneModel("type user\n", 60)
	m.editorErr = "invalid_authorization_model: the relation viewer in type document " +
		"references relation ownr which does not exist on any type in the model"
	w, h := m.contentSize()
	lines := strings.Split(m.editorBody(), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected the long error to wrap across multiple footer lines, got %d:\n%s",
			len(lines), strings.Join(lines, "\n"))
	}
	for i, ln := range lines {
		if lipgloss.Width(ln) > w {
			t.Fatalf("line %d exceeds content width %d (shell would truncate it): %q", i, w, ln)
		}
	}
	if len(lines) > h {
		t.Fatalf("rendered %d rows exceeds main-area height %d (would be clipped)", len(lines), h)
	}
}

func TestEditorBodyPaneShrinksForTallFooter(t *testing.T) {
	short := newPaneModel("type user\n", 60)
	shortRows := short.editorViewportRows()
	tall := newPaneModel("type user\n", 60)
	tall.editorErr = strings.Repeat("very long error message that will certainly wrap. ", 8)
	if tall.editorViewportRows() >= shortRows {
		t.Fatalf("a tall wrapped footer should reduce the editor viewport rows (short=%d tall=%d)",
			shortRows, tall.editorViewportRows())
	}
}
