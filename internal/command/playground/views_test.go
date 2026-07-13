package playground

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"

	"github.com/sergiught/openfga-cli/internal/dsl"
	"github.com/sergiught/openfga-cli/internal/ui/shell"
)

// newEditorTestModel builds a Model with a usable editor and a shell sized so
// that contentSize() (== shell.MainSize()) is deterministic. A zero-value
// textarea.Model panics on View/SetWidth (nil viewport), and a nil shell panics
// in contentSize, so both must be constructed. totalW < ~120 keeps MainSize
// below editorSplitMinWidth (no split).
func newEditorTestModel(value string, totalW int) Model {
	sh := shell.New()
	sh.SetSize(totalW, 30)
	ta := textarea.New()
	ta.SetValue(value)
	ta.SetWidth(totalW)
	ta.SetHeight(10)
	return Model{sh: sh, editor: ta}
}

func TestEditorBodyShowsFooterOnError(t *testing.T) {
	m := newEditorTestModel("model\n  schema 1.1\ntype user\n  relations\n    define x [user]\n", 80)
	m.editorDiags = dsl.Diagnostics(m.editor.Value())
	if len(m.editorDiags) == 0 {
		t.Fatal("precondition: expected a diagnostic for the bad model")
	}
	out := m.editorBody()
	if !strings.Contains(out, "error line") {
		t.Fatalf("expected footer to contain 'error line', got:\n%s", out)
	}
}

func TestEditorBodyNarrowHasNoPreviewSeparator(t *testing.T) {
	m := newEditorTestModel("model\n  schema 1.1\ntype user\n", 80) // MainSize < 100 -> no split
	out := m.editorBody()
	// The split joins two bordered panes horizontally; narrow mode renders a
	// single pane, so the first row must not contain two vertical border runs.
	if strings.Count(firstLine(out), "│") > 1 {
		t.Fatalf("narrow editor should not render a split preview, got:\n%s", out)
	}
}

// splitEditorModel builds a wide model whose editor is sized the way resize()
// sizes it in split mode (half width), so the preview pane gets realistic room.
// The test helper sets the editor to the full width, so without this the preview
// is starved to a few columns and clips its own content.
func splitEditorModel(t *testing.T, value string) Model {
	t.Helper()
	m := newEditorTestModel(value, 160)
	w, _ := m.contentSize()
	if w < editorSplitMinWidth {
		t.Fatalf("precondition: contentSize width %d < %d, split will not trigger", w, editorSplitMinWidth)
	}
	m.editor.SetWidth(w/2 - 1) // mirror resize()'s split-mode sizing
	return m
}

func TestEditorBodyWideRendersSplitPreview(t *testing.T) {
	m := splitEditorModel(t, "model\n  schema 1.1\ntype user\n")
	out := m.editorBody()
	// The split adds a rounded-border preview panel; its vertical borders (│)
	// are absent in narrow mode, where the editor gutter uses ┃ instead. Count
	// across the whole output (the panel's top row is corners, not verticals).
	if got := strings.Count(out, "│"); got < 2 {
		t.Fatalf("wide editor should render a bordered split preview (>=2 │ runs), got %d in:\n%s", got, out)
	}
}

func TestEditorBodyWideShowsCaretForDiagnostic(t *testing.T) {
	m := splitEditorModel(t, "model\n  schema 1.1\ntype user\n  relations\n    define x [user]\n")
	m.editorDiags = dsl.Diagnostics(m.editor.Value())
	if len(m.editorDiags) == 0 {
		t.Fatal("precondition: expected a diagnostic for the bad model")
	}
	out := m.editorBody()
	// The preview underlines the offending column with a caret row.
	if !strings.Contains(out, "^") {
		t.Fatalf("expected preview to render a '^' caret for the diagnostic, got:\n%s", out)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
