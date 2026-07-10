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

func TestSetHighlightMarksFocusedRowOnly(t *testing.T) {
	const bgSGR = "48;2;18;52;86" // #123456
	f := NewForm(New("Alpha", "aa"), New("Beta", "bb"))
	f.SetWidth(40)
	f.SetHighlight(lipgloss.Color("#123456"))
	f.Init() // focuses "Alpha"
	var focusedHL, unfocusedHL bool
	for _, ln := range strings.Split(f.View(), "\n") {
		plain := ansi.Strip(ln)
		if strings.Contains(ln, bgSGR) {
			if strings.Contains(plain, "Alpha") {
				focusedHL = true
			}
			if strings.Contains(plain, "Beta") {
				unfocusedHL = true
			}
		}
	}
	if !focusedHL {
		t.Fatal("the focused field's row must carry the highlight")
	}
	if unfocusedHL {
		t.Fatal("unfocused fields must not carry any highlight")
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

func TestFocusedInputStartsAtCursor(t *testing.T) {
	f := NewForm(New("User", "user:anne"), New("Relation", "viewer"))
	f.SetWidth(40)
	f.SetHighlight(lipgloss.Color("#123456"))
	f.Init()
	lines := strings.Split(ansi.Strip(f.View()), "\n")
	if strings.Contains(lines[1], "▐▌") {
		t.Fatalf("focused input line should have no accent bar, got %q", lines[1])
	}
	for _, k := range []string{"a", "tab", "b", "enter"} {
		f.Update(key(k))
	}
	if !f.Completed() {
		t.Fatal("form should still complete")
	}
}

func TestToggleFieldFlips(t *testing.T) {
	f := NewForm(New("User", "user:anne"), NewToggle("Expect", "Allowed", "Denied", true))
	f.SetWidth(40)
	f.Init()
	f.Update(key("tab")) // focus the toggle
	if got := f.Values()[1]; got != "true" {
		t.Fatalf("toggle should start true, got %q", got)
	}
	f.Update(key(" ")) // flip
	if got := f.Values()[1]; got != "false" {
		t.Fatalf("space should flip the toggle to false, got %q", got)
	}
	f.Update(key(" ")) // flip back
	if got := f.Values()[1]; got != "true" {
		t.Fatalf("space should flip the toggle back to true, got %q", got)
	}
	if v := ansi.Strip(f.View()); !strings.Contains(v, "Allowed") || !strings.Contains(v, "Denied") {
		t.Fatalf("toggle view should show both choice labels, got %q", v)
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
