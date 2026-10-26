package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openDialog(dir, store string) func(s *Session) component {
	return func(s *Session) component { return newModelDialog(s, dir, store) }
}

func TestModelDialogPreselectsTheFileBesideTheMapping(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "model.json"))
	h := newHarness(t, nil, nil, openDialog(dir, "prod"))
	h.press("enter")
	if want := (modelChoice{kind: modelFile, path: filepath.Join(dir, "model.json")}); h.result != want {
		t.Fatalf("result = %#v, want %#v", h.result, want)
	}

	// model.fga wins over model.json.
	touch(t, filepath.Join(dir, "model.fga"))
	h = newHarness(t, nil, nil, openDialog(dir, ""))
	h.press("enter")
	if want := (modelChoice{kind: modelFile, path: filepath.Join(dir, "model.fga")}); h.result != want {
		t.Fatalf("result = %#v, want %#v", h.result, want)
	}
}

func TestModelDialogOffersTheStoreOnlyWhenOneIsSet(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, nil, nil, openDialog(dir, "profile prod, store 01ARZ"))
	if !strings.Contains(plain(h.view(80, 20)), "The store model (profile prod, store 01ARZ)") {
		t.Fatalf("no store row:\n%s", plain(h.view(80, 20)))
	}
	h.press("enter") // no file beside it, so the store is preselected
	if h.result != (modelChoice{kind: modelStore}) {
		t.Fatalf("result = %#v", h.result)
	}

	h = newHarness(t, nil, nil, openDialog(dir, ""))
	if strings.Contains(plain(h.view(80, 20)), "store model") {
		t.Fatal("a store row is offered without a store")
	}
	h.press("enter") // nothing else to preselect
	if h.result != (modelChoice{kind: modelNone}) {
		t.Fatalf("result = %#v", h.result)
	}
}

func TestModelDialogBrowsesForAnotherFile(t *testing.T) {
	dir := browseTree(t)
	h := newHarness(t, nil, nil, openDialog(dir, "prod"))
	h.press("down", "down") // beside, store, Another model file…
	h.press("enter")
	b := topAs[*fileBrowse](t, h)
	selectRow(t, b, "other.json")
	h.press("enter")
	if want := (modelChoice{kind: modelFile, path: filepath.Join(dir, "other.json")}); !h.closed || h.result != want {
		t.Fatalf("closed = %v, result = %#v", h.closed, h.result)
	}

	// Cancelling the browser leaves the question open.
	h = newHarness(t, nil, nil, openDialog(dir, "prod"))
	h.press("down", "down", "enter", "esc")
	if h.closed {
		t.Fatal("cancelling the browser closed the dialog")
	}
	topAs[*modelDialog](t, h)
}

func TestModelDialogEscKeepsTheCurrentModel(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "model.fga"))
	h := newHarness(t, nil, nil, openDialog(dir, "prod"))
	h.press("down", "esc")
	if !h.closed || h.result != nil || h.changes != 0 {
		t.Fatalf("closed = %v, result = %#v, changes = %d", h.closed, h.result, h.changes)
	}
}

func TestAFolderNamedModelIsNotPreselected(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "model.fga"), 0o750); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, nil, nil, openDialog(dir, ""))
	if d := h.top().(*modelDialog); d.beside != "" {
		t.Fatalf("beside = %q, want none", d.beside)
	}
}
