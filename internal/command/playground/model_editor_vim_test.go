package playground

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/ui/vimedit"
)

// openEditorOn opens the model editor on dsl.
func openEditorOn(t *testing.T, dsl string) tea.Model {
	t.Helper()
	m := newTestModel()
	m, _ = m.Update(key("3"))
	m, _ = m.Update(key("enter"))
	mm := m.(Model)
	mm.modelDSL = dsl
	m, _ = mm.Update(key("e"))
	if !m.(Model).editorOpen {
		t.Fatal("editor did not open")
	}
	return m
}

// typeKeys feeds each rune of s as a key press.
func typeKeys(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m, _ = m.Update(key(string(r)))
	}
	return m
}

// TestEditorLargeModelKeystrokesStayFast: the editor used to spend ~230ms per
// keystroke on a 1000-line model. Typing, moving and editing there, with a
// redraw after every key as the program does, must stay interactive.
func TestEditorLargeModelKeystrokesStayFast(t *testing.T) {
	src, err := os.ReadFile("../../fga/testdata/stress_model.fga")
	if err != nil {
		t.Fatal(err)
	}
	m := openEditorOn(t, string(src))
	m, _ = m.Update(key("esc"))
	m = typeKeys(m, "500G")
	keys := strings.Split("j j j k dd p u x u w b o", " ")
	const rounds = 5
	start := time.Now()
	for range rounds {
		for _, k := range keys {
			m = typeKeys(m, k)
			if k == "o" {
				m = typeKeys(m, "define x: [user]")
				m, _ = m.Update(key("esc"))
				m, _ = m.Update(key("u"))
			}
			_ = m.(Model).viewString()
		}
	}
	n := rounds * (len(strings.Join(keys, "")) + len("define x: [user]") + 2)
	per := time.Since(start) / time.Duration(n)
	// ~4ms here, ~40ms under -race; the old textarea took ~230ms without it.
	if per > 100*time.Millisecond {
		t.Fatalf("%v per keystroke on a %d-line model, want under 100ms", per, strings.Count(string(src), "\n"))
	}
}

// TestEditorColonWAppliesAndColonQConfirms: the ex commands map onto the
// same apply and discard-confirm paths as ctrl+s and esc.
func TestEditorColonWAppliesAndColonQConfirms(t *testing.T) {
	m := openEditorOn(t, "model\n  schema 1.1\ntype user\n")
	m, _ = m.Update(key("esc"))
	m = typeKeys(m, "ddu:w")
	m, cmd := m.Update(key("enter"))
	if !m.(Model).modelApplying || cmd == nil {
		t.Fatal(":w should start applying the model")
	}

	m = openEditorOn(t, "model\n  schema 1.1\ntype user\n")
	m, _ = m.Update(key("esc"))
	m = typeKeys(m, "dd:q")
	m, _ = m.Update(key("enter"))
	if mm := m.(Model); mm.confirm == nil || !mm.editorOpen {
		t.Fatal(":q on an edited buffer should ask before discarding")
	}

	m = openEditorOn(t, "model\n  schema 1.1\ntype user\n")
	m, _ = m.Update(key("esc"))
	m = typeKeys(m, "dd:q!")
	m, _ = m.Update(key("enter"))
	if mm := m.(Model); mm.confirm != nil || mm.editorOpen {
		t.Fatal(":q! should close without asking")
	}
}

// TestEditorPasteInsertsText: a bracketed paste lands in the buffer as typed
// text, newlines included.
func TestEditorPasteInsertsText(t *testing.T) {
	m := openEditorOn(t, "model\n")
	m, _ = m.Update(tea.PasteMsg{Content: "type a\ntype b\n"})
	if mm := m.(Model); !strings.HasPrefix(mm.editor.Value(), "type a\ntype b\nmodel") {
		t.Fatalf("paste not inserted at the cursor: %q", mm.editor.Value())
	}
}

// TestEditorDiagnosticsAreDebounced: diagnostics refresh once the buffer
// rests, and a tick for an older revision is ignored.
func TestEditorDiagnosticsAreDebounced(t *testing.T) {
	m := openEditorOn(t, "model\n  schema 1.1\ntype user\n")
	if len(m.(Model).editorDiags) != 0 {
		t.Fatalf("precondition: valid model has diagnostics %v", m.(Model).editorDiags)
	}
	m, cmd := m.Update(key("x")) // breaks the `model` keyword
	if cmd == nil {
		t.Fatal("an edit should schedule a diagnostics refresh")
	}
	if len(m.(Model).editorDiags) != 0 {
		t.Fatal("diagnostics should wait for the buffer to rest")
	}
	mm := m.(Model)
	stale := editorDiagMsg{rev: mm.editor.Revision() - 1}
	m, _ = m.Update(stale)
	if len(m.(Model).editorDiags) != 0 {
		t.Fatal("a tick for an older revision must not refresh")
	}
	m, parse := m.Update(cmd())
	if parse == nil {
		t.Fatal("a tick for the current revision should start a parse")
	}
	if len(m.(Model).editorDiags) != 0 {
		t.Fatal("the parse runs off the event loop, not in the tick")
	}
	result := parse()
	m, _ = m.Update(result)
	if len(m.(Model).editorDiags) == 0 {
		t.Fatal("expected a diagnostic once the buffer rests")
	}

	// A parse that finishes after a further edit is stale.
	m, _ = m.Update(key("x"))
	m, _ = m.Update(editorDiagsMsg{rev: result.(editorDiagsMsg).rev})
	if len(m.(Model).editorDiags) == 0 {
		t.Fatal("a result for an older revision must not replace the diagnostics")
	}
}

// TestEditorStatusLineShowsMode: the status line names the mode and the
// footer keys follow it.
func TestEditorStatusLineShowsMode(t *testing.T) {
	m := openEditorOn(t, "model\n")
	out := ansi.Strip(m.(Model).viewString())
	if !strings.Contains(out, "INSERT") || !strings.Contains(out, "esc normal mode") {
		t.Fatalf("insert mode not shown:\n%s", out)
	}
	m, _ = m.Update(key("esc"))
	m = typeKeys(m, ":%s/x")
	out = ansi.Strip(m.(Model).viewString())
	if !strings.Contains(out, ":%s/x") || !strings.Contains(out, "↵ run") {
		t.Fatalf("command line not shown:\n%s", out)
	}
	if mm := m.(Model); mm.editor.Mode() != vimedit.Command {
		t.Fatal("expected command mode")
	}
}

// TestEditorPaneScrollsSideways: on a line wider than the pane the cursor
// stays visible.
func TestEditorPaneScrollsSideways(t *testing.T) {
	long := strings.Repeat("a", 200) + "END"
	m := newPaneModel(long, 80)
	for range 202 {
		m.editor.HandleKey(vimedit.Key{Name: "right"})
	}
	out := ansi.Strip(m.editorPane(80, 5))
	if !strings.Contains(out, "EN") {
		t.Fatalf("pane did not scroll to the cursor:\n%s", out)
	}
}

// settleDiagnostics runs the debounced diagnostics refresh cmd schedules.
func settleDiagnostics(t *testing.T, m tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a diagnostics refresh to be scheduled")
	}
	m, parse := m.Update(cmd())
	if parse == nil {
		t.Fatal("expected a diagnostics parse")
	}
	m, _ = m.Update(parse())
	return m
}

// TestEditorErrorLineKeepsCursorVisible: an error appearing in the footer
// takes a buffer row, which must not push the cursor off the bottom.
func TestEditorErrorLineKeepsCursorVisible(t *testing.T) {
	var b strings.Builder
	b.WriteString("model\n  schema 1.1\n")
	for i := range 200 {
		b.WriteString("type t" + strconv.Itoa(i) + "\n")
	}
	m := openEditorOn(t, b.String())
	m, _ = m.Update(key("esc"))
	m = typeKeys(m, "L")
	m, cmd := m.Update(key("x")) // "ype tN": a syntax error on the cursor line
	m = settleDiagnostics(t, m, cmd)
	mm := m.(Model)
	if len(mm.editorDiags) == 0 {
		t.Fatal("precondition: expected a diagnostic")
	}
	if row, top, rows := mm.editor.Cursor().Row, mm.editorTop, mm.editorViewportRows(); row < top || row >= top+rows {
		t.Fatalf("cursor row %d outside the visible rows [%d, %d)", row, top, top+rows)
	}
}

// TestEditorPasteIgnoredUnderDialog: a paste while a dialog is over the
// editor must not change the buffer behind it.
func TestEditorPasteIgnoredUnderDialog(t *testing.T) {
	m := openEditorOn(t, "model\n")
	m, _ = m.Update(key("esc"))
	m = typeKeys(m, "dd")
	m, _ = m.Update(key("esc")) // asks before discarding the edit
	if m.(Model).confirm == nil {
		t.Fatal("precondition: expected the discard confirm")
	}
	before := func() string { mm := m.(Model); return mm.editor.Value() }()
	m, _ = m.Update(tea.PasteMsg{Content: "type x\n"})
	if mm := m.(Model); mm.editor.Value() != before {
		t.Fatalf("paste under the confirm changed the buffer: %q", mm.editor.Value())
	}
}

// TestEditorLongCommandLineShowsItsEnd: a command line wider than the status
// line keeps the end being typed in view.
func TestEditorLongCommandLineShowsItsEnd(t *testing.T) {
	m := openEditorOn(t, "model\n")
	m, _ = m.Update(key("esc"))
	m = typeKeys(m, ":%s/"+strings.Repeat("a", 300)+"/END")
	mm := m.(Model)
	line := ansi.Strip(mm.editorStatusLine(80))
	if !strings.Contains(line, "/END") || ansi.StringWidth(line) > 80 {
		t.Fatalf("status line lost the end of the command or overflowed: %q", line)
	}
}
