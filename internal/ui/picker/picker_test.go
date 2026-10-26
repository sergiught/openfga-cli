package picker

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func testPicker() *Picker {
	return New([]Item{
		{Title: "alpha", Desc: "first", Value: "a"},
		{Title: "beta", Desc: "second", Value: "b"},
		{Title: "gamma", Value: "c"},
	})
}

func TestMoveWraps(t *testing.T) {
	p := testPicker()
	if got := p.Cursor(); got != 0 {
		t.Fatalf("expected initial cursor 0, got %d", got)
	}

	p.Move(-1)
	if got := p.Cursor(); got != 2 {
		t.Fatalf("expected Move(-1) to wrap to last row, got %d", got)
	}

	p.Move(1)
	if got := p.Cursor(); got != 0 {
		t.Fatalf("expected Move(1) to wrap back to first row, got %d", got)
	}

	p.Move(1)
	if got := p.Cursor(); got != 1 {
		t.Fatalf("expected cursor 1, got %d", got)
	}
}

func TestMoveOnEmptyPickerIsSafe(t *testing.T) {
	p := New(nil)

	p.Move(1)
	p.Move(-1)

	if got := p.Cursor(); got != 0 {
		t.Fatalf("expected cursor to stay 0 on an empty picker, got %d", got)
	}
	if got := p.Selected(); got != (Item{}) {
		t.Fatalf("expected Selected() to be the zero Item, got %+v", got)
	}
}

func TestSetCursorClamps(t *testing.T) {
	p := testPicker()

	p.SetCursor(-5)
	if got := p.Cursor(); got != 0 {
		t.Fatalf("expected a negative index clamped to 0, got %d", got)
	}

	p.SetCursor(99)
	if got := p.Cursor(); got != p.Len()-1 {
		t.Fatalf("expected an out-of-range index clamped to the last row, got %d", got)
	}

	p.SetCursor(1)
	if got := p.Cursor(); got != 1 {
		t.Fatalf("expected cursor 1, got %d", got)
	}
}

func TestViewMarksSelectionAndShowsDescriptions(t *testing.T) {
	p := testPicker()
	p.SetCursor(1)

	view := ansi.Strip(p.View(40))
	for _, want := range []string{"alpha", "beta", "gamma", "first", "second"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected view to contain %q, got %q", want, view)
		}
	}

	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[2], "beta") {
		t.Fatalf("expected the selected row to be marked, got %q", view)
	}
}

func TestClampTruncatesWithEllipsis(t *testing.T) {
	if got := Clamp("hello", 0); got != "hello" {
		t.Fatalf("expected a non-positive width to leave the string alone, got %q", got)
	}
	if got := Clamp("hello", 10); got != "hello" {
		t.Fatalf("expected a string shorter than the width to be untouched, got %q", got)
	}
	if got := Clamp("hello world", 5); got != "hell…" {
		t.Fatalf("expected truncation with an ellipsis, got %q", got)
	}
}
