package playground

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/dsl"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	"github.com/sergiught/openfga-cli/internal/ui/shell"
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

// ctrlG is the glyph-cycle chord. This package's key() helper only builds
// named keys and single runes, so a ctrl chord is constructed directly — the
// same way redraw_test.go builds ctrl+l.
var ctrlG = tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}

func TestGlyphCycleKey(t *testing.T) {
	// The TUI writes config on cycle; keep it out of the real config file.
	configtest.Isolate(t)
	t.Cleanup(func() { icons.Apply(icons.ModeNerdFont) })

	icons.Apply(icons.ModeNerdFont)
	var m tea.Model = newTestModel()

	// Cycle runs nerdfont -> unicode -> off -> nerdfont.
	want := []icons.Mode{icons.ModeUnicode, icons.ModeOff, icons.ModeNerdFont}
	for i, w := range want {
		m, _ = m.Update(ctrlG)
		if got := icons.Current(); got != w {
			t.Fatalf("press %d: icons.Current() = %v, want %v", i+1, got, w)
		}
	}
}

func TestGlyphCyclePersistsConcreteMode(t *testing.T) {
	configtest.Isolate(t)
	t.Cleanup(func() { icons.Apply(icons.ModeNerdFont) })

	icons.Apply(icons.ModeNerdFont)
	var m tea.Model = newTestModel()
	m, _ = m.Update(ctrlG)

	// An explicit choice must outrank the guess: cycling writes a concrete
	// rung, taking the user out of auto for good.
	if got := m.(Model).cli.Config.Icons; got != "unicode" {
		t.Fatalf("config.Icons = %q, want %q", got, "unicode")
	}
}

func TestGlyphCycleStartsFromResolvedAuto(t *testing.T) {
	configtest.Isolate(t)
	t.Cleanup(func() { icons.Apply(icons.ModeNerdFont) })

	// With auto resolved, the first press must visibly change something rather
	// than appear to no-op.
	icons.Apply(icons.ModeAuto)
	before := icons.Current()
	var m tea.Model = newTestModel()
	if _, _ = m.Update(ctrlG); icons.Current() == before {
		t.Fatal("first press after auto resolution did not change the rung")
	}
}

func TestGlyphCycleIgnoredWhileFiltering(t *testing.T) {
	configtest.Isolate(t)
	t.Cleanup(func() { icons.Apply(icons.ModeNerdFont) })

	icons.Apply(icons.ModeNerdFont)
	mm := newTestModel().(Model)
	mm.section = secStores
	mm.focus = shell.FocusPanel
	var m tea.Model = mm

	// "/" starts the list filter; every subsequent key belongs to the filter
	// input, including ctrl+g.
	m, _ = m.Update(key("/"))
	if _, _ = m.Update(ctrlG); icons.Current() != icons.ModeNerdFont {
		t.Fatal("g typed into a list filter must not cycle glyphs")
	}
}
