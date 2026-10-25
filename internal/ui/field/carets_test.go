package field

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A form rebuilt from scratch starts every caret at 0. Carets and SetCarets let
// a caller carry them across, so typing continues where it was.
func TestCaretsSurviveARebuild(t *testing.T) {
	build := func() *Form {
		return NewForm(New("a", ""), New("b", ""), NewToggle("c", "on", "off", false))
	}
	old := build()
	old.Init()
	for _, r := range "user:ae" {
		old.Update(key(string(r)))
	}
	old.Update(tea.KeyPressMsg{Code: tea.KeyLeft}) // caret between "a" and "e"
	carets := old.Carets()
	if want := []int{6, 0, 0}; !reflect.DeepEqual(carets, want) {
		t.Fatalf("Carets() = %v, want %v", carets, want)
	}
	old.Update(key("tab"))
	for _, r := range "viewer" {
		old.Update(key(string(r)))
	}
	carets = old.Carets()
	if want := []int{6, 6, 0}; !reflect.DeepEqual(carets, want) {
		t.Fatalf("Carets() = %v, want %v", carets, want)
	}

	fresh := build()
	fresh.Init()
	fresh.SetValues(old.Values())
	fresh.SetCarets(carets)
	fresh.FocusIndex(0)
	fresh.Update(key("n"))
	if got := fresh.Values()[0]; got != "user:ane" {
		t.Fatalf("typing after rebuild gave %q, want %q", got, "user:ane")
	}
}

func TestSetCaretsClampsToTheValue(t *testing.T) {
	f := NewForm(New("a", ""))
	f.Init()
	f.SetValues([]string{"abc"})
	f.SetCarets([]int{99})
	if got := f.Carets()[0]; got != 3 {
		t.Fatalf("caret = %d, want it clamped to 3", got)
	}
	f.SetCarets(nil)
	if got := f.Carets()[0]; got != 3 {
		t.Fatalf("caret = %d, want a missing entry to leave it at 3", got)
	}
}
