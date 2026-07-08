package field

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	default:
		r := []rune(s)[0]
		return tea.KeyPressMsg{Code: r, Text: s}
	}
}

func TestFormCompletesWithValues(t *testing.T) {
	f := NewForm(New("User", "user:anne"), New("Relation", "viewer"))
	f.SetWidth(40)
	f.Init()
	for _, k := range []string{"a", "tab", "b", "enter"} {
		f.Update(key(k))
	}
	if !f.Completed() {
		t.Fatal("form should be completed after enter with valid values")
	}
	got := f.Values()
	if got[0] != "a" || got[1] != "b" {
		t.Fatalf("values = %v, want [a b]", got)
	}
}

func TestValidationBlocksCompletion(t *testing.T) {
	f := NewForm(New("User", "").WithValidate(func(s string) error {
		if !strings.Contains(s, ":") {
			return errors.New("must be type:id")
		}
		return nil
	}))
	f.SetWidth(40)
	f.Init()
	f.Update(key("a"))
	f.Update(key("enter"))
	if f.Completed() {
		t.Fatal("form must not complete with invalid value")
	}
	if !strings.Contains(f.View(), "must be type:id") {
		t.Fatal("validation error should render in view")
	}
}

func TestSetValuesFillsFields(t *testing.T) {
	f := NewForm(New("User", ""), New("Relation", ""), New("Object", ""))
	f.SetWidth(40)
	f.Init()
	f.SetValues([]string{"user:anne", "viewer"}) // fewer vals than fields
	got := f.Values()
	if got[0] != "user:anne" || got[1] != "viewer" || got[2] != "" {
		t.Fatalf("values = %v, want [user:anne viewer \"\"]", got)
	}
}

func TestFocusedFieldHasDoubleWidthBar(t *testing.T) {
	f := NewForm(New("User", "user:anne"), New("Relation", "viewer"))
	f.SetWidth(40)
	f.Init()
	lines := strings.Split(ansi.Strip(f.View()), "\n")
	if !strings.HasPrefix(lines[1], "▐▌") {
		t.Fatalf("focused field input line = %q, want prefix ▐▌", lines[1])
	}
	for _, k := range []string{"a", "tab", "b", "enter"} {
		f.Update(key(k))
	}
	if !f.Completed() {
		t.Fatal("form should still complete with the double-width bar")
	}
}

func TestResetClearsState(t *testing.T) {
	f := NewForm(New("User", ""))
	f.SetWidth(40)
	f.Init()
	f.Update(key("a"))
	f.Update(key("enter"))
	f.Reset()
	if f.Completed() || f.Values()[0] != "" {
		t.Fatal("reset should clear completion and values")
	}
}
