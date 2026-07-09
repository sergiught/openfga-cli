package field

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
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

func TestSetBackgroundTintsChrome(t *testing.T) {
	const bgSGR = "48;2;18;52;86" // #123456
	f := NewForm(New("User", "user:anne"))
	f.SetWidth(40)
	f.SetBackground(lipgloss.Color("#123456"))
	f.Init()
	if !strings.Contains(f.View(), bgSGR) {
		t.Fatal("SetBackground should tint the form chrome to fill a colored surface")
	}
	// Without a background the form stays transparent (the flat query pane).
	g := NewForm(New("User", "user:anne"))
	g.SetWidth(40)
	g.Init()
	if strings.Contains(g.View(), bgSGR) {
		t.Fatal("a form without SetBackground must not paint a background")
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
