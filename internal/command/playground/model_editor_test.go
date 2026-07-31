package playground

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	transformer "github.com/openfga/language/pkg/go/transformer"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

func TestDSLTransformsToWriteRequest(t *testing.T) {
	dsl := "model\n  schema 1.1\ntype user\ntype document\n  relations\n    define viewer: [user]"
	js, err := transformer.TransformDSLToJSON(dsl)
	if err != nil {
		t.Fatal(err)
	}
	var req openfga.WriteAuthorizationModelRequest
	if err := json.Unmarshal([]byte(js), &req); err != nil {
		t.Fatal(err)
	}
	if req.SchemaVersion != "1.1" {
		t.Errorf("schema = %q, want \"1.1\"", req.SchemaVersion)
	}
	if len(req.TypeDefinitions) != 2 {
		t.Errorf("types = %d, want 2", len(req.TypeDefinitions))
	}
}

func TestModelEditorOpensAndCloses(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("3"))     // Model section
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("e"))     // open editor
	if !m.(Model).editorOpen {
		t.Fatal("e should open the editor")
	}
	if strings.TrimSpace(m.(Model).viewString()) == "" {
		t.Fatal("editor view empty")
	}
	m, _ = m.Update(key("esc"))
	if m.(Model).editorOpen {
		t.Error("esc should close the editor")
	}
}

func TestModelEditorPreFillsWithModelDSL(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("3"))     // Model section
	m, _ = m.Update(key("enter")) // descend into the panel
	// Pre-load a DSL string into modelDSL
	mod := m.(Model)
	mod.modelDSL = "model\n  schema 1.1\ntype user\n"
	m = mod
	m, _ = m.Update(key("e")) // open editor
	val := m.(Model).editor.Value()
	if !strings.Contains(val, "schema 1.1") {
		t.Errorf("editor should be pre-filled with model DSL, got: %q", val)
	}
}

func TestModelEditorFallsBackToTemplate(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("3"))     // Model section
	m, _ = m.Update(key("enter")) // descend into the panel
	// Ensure no DSL pre-fill
	mod := m.(Model)
	mod.modelDSL = ""
	m = mod
	m, _ = m.Update(key("e")) // open editor
	val := m.(Model).editor.Value()
	if !strings.Contains(val, "schema 1.1") {
		t.Errorf("editor template should contain schema 1.1, got: %q", val)
	}
	if !strings.Contains(val, "document") {
		t.Errorf("editor template should contain document type, got: %q", val)
	}
}

func TestModelEditorApplyErrorKeepsEditorOpen(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("3"))
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("e"))
	if !m.(Model).editorOpen {
		t.Fatal("editor should be open")
	}
	// Simulate a DSL error response
	m, _ = m.Update(modelAppliedMsg{err: fmt.Errorf("syntax error at line 1"), modelID: ""})
	if !m.(Model).editorOpen {
		t.Error("editor should stay open on error")
	}
	if m.(Model).editorErr == "" {
		t.Error("editorErr should be set on error")
	}
}

// Clicking a sidebar tab while the DSL editor is open must be a no-op.
// handleKey routes every keystroke into the editor for as long as editorOpen
// is set, so swapping the section behind its back draws one section while
// typing goes to another — and clearing the flag instead would silently drop
// the unsaved edits the discard confirm exists to protect.
func TestNavClickIgnoredWhileModelEditorOpen(t *testing.T) {
	configtest.Isolate(t)
	tm := newTestModel()
	tm, _ = tm.Update(key("3"))     // Model section
	tm, _ = tm.Update(key("enter")) // descend into the panel
	tm, _ = tm.Update(key("e"))     // open the editor
	m := tm.(Model)
	if !m.editorOpen {
		t.Fatal("e should open the model editor")
	}

	// NavHit's row origin is recorded while rendering, so hit-test only after a
	// frame exists. navTop is unexported; probe for the Profiles row.
	_ = m.viewString()
	x, y := -1, -1
	for row := 0; row < 32 && y < 0; row++ {
		if m.sh.NavHit(1, row) == int(secProfiles) {
			x, y = 1, row
		}
	}
	if y < 0 {
		t.Fatal("could not locate the Profiles nav row; the sidebar must have collapsed")
	}

	tm2, _ := tea.Model(m).Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	clicked := tm2.(Model)
	if clicked.section != secModel {
		t.Fatalf("a nav click with the editor open must not change section; got %v", clicked.section)
	}
	if !clicked.editorOpen {
		t.Fatal("a nav click is not a discard; the editor must stay open")
	}
}

func TestModelEditorApplySuccessClosesEditor(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("3"))
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("e"))
	// Simulate a successful apply
	m, _ = m.Update(modelAppliedMsg{err: nil, modelID: "new-model-id"})
	if m.(Model).editorOpen {
		t.Error("editor should close on success")
	}
}
