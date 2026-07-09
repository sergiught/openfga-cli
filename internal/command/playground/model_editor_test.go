package playground

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	transformer "github.com/openfga/language/pkg/go/transformer"

	"github.com/sergiught/go-openfga/openfga"
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
	m, _ = m.Update(key("2"))     // Model section
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
	m, _ = m.Update(key("2"))     // Model section
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
	m, _ = m.Update(key("2"))     // Model section
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
	m, _ = m.Update(key("2"))
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

func TestModelEditorApplySuccessClosesEditor(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("2"))
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("e"))
	// Simulate a successful apply
	m, _ = m.Update(modelAppliedMsg{err: nil, modelID: "new-model-id"})
	if m.(Model).editorOpen {
		t.Error("editor should close on success")
	}
}
