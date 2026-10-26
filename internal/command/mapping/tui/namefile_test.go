package tui

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNameFileRefusesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mapping.yaml"), miniMapping)
	h := newHarness(t, nil, nil, func(s *Session) component { return newNameFile(s, dir) })

	h.press("enter")
	if h.closed {
		t.Fatal("an existing file was accepted")
	}
	if v := plain(h.view(60, 10)); !strings.Contains(v, "already exists") {
		t.Fatalf("no inline refusal:\n%s", v)
	}

	// Correcting the name works: the form is still live after a refusal.
	h.press("ctrl+a", "ctrl+k")
	h.typeText("fresh.yaml")
	h.press("enter")
	if !h.closed || h.result != filepath.Join(dir, "fresh.yaml") {
		t.Fatalf("closed = %v, result = %#v", h.closed, h.result)
	}
}

func TestNameFileDefaultsAndEsc(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, nil, nil, func(s *Session) component { return newNameFile(s, dir) })
	h.press("enter")
	if !h.closed || h.result != filepath.Join(dir, "mapping.yaml") {
		t.Fatalf("closed = %v, result = %#v", h.closed, h.result)
	}
	h = newHarness(t, nil, nil, func(s *Session) component { return newNameFile(s, dir) })
	h.press("esc")
	if !h.closed || h.result != nil {
		t.Fatalf("closed = %v, result = %#v", h.closed, h.result)
	}
}
