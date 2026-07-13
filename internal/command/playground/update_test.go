package playground

import (
	"errors"
	"testing"
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
