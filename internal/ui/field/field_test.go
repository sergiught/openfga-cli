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
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "alt+enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}
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

func TestSetValuesSanitizesTerminalControls(t *testing.T) {
	f := NewForm(New("Name", ""))
	attack := "\x1b]52;c;YXR0YWNr\x07"
	f.SetValues([]string{"before" + attack + "after"})
	got := f.Values()[0]
	if strings.Contains(got, attack) || strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("field retained terminal controls: %q", got)
	}
}

// TestInsertSanitizesTerminalControls guards the same invariant as
// TestSetValuesSanitizesTerminalControls, but for Insert's own write path:
// Field.insert must run text through style.SanitizeTerminal rather than
// writing it straight into the textinput, or a Unicode bidi-override rune
// (invisible on screen, reordering what is displayed) and CSI residue that
// bubbles' own textinput leaves behind would both survive a paste.
func TestInsertSanitizesTerminalControls(t *testing.T) {
	f := NewForm(New("Name", ""))
	f.Insert(0, "doc\u202egnp")
	if got := f.Values()[0]; got != "docgnp" {
		t.Fatalf("field retained a bidi-override rune: %q", got)
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

// TUI-16: a Secret() field must mask its input in the rendered view (no
// cleartext token on screen) while still returning the real value to the form.
func TestSecretFieldMasksButKeepsValue(t *testing.T) {
	f := NewForm(New("API token", "token").Secret())
	f.SetWidth(40)
	f.Init()
	for _, ch := range "s3cr3t" {
		f.Update(key(string(ch)))
	}
	view := ansi.Strip(f.View())
	if strings.Contains(view, "s3cr3t") {
		t.Fatalf("secret field must not echo the token in cleartext, got:\n%s", view)
	}
	if !strings.Contains(view, "••••••") {
		t.Fatalf("secret field should render a dot mask, got:\n%s", view)
	}
	if got := f.Values()[0]; got != "s3cr3t" {
		t.Fatalf("secret field should still yield its real value, got %q", got)
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

func TestResumePreservesCompletedValues(t *testing.T) {
	f := NewForm(New("User", ""))
	f.SetWidth(40)
	f.Init()
	f.Update(key("a"))
	f.Update(key("enter"))
	f.Resume()
	if f.Completed() {
		t.Fatal("resumed form should accept corrections")
	}
	if got := f.Values()[0]; got != "a" {
		t.Fatalf("resumed value = %q, want preserved input", got)
	}
}

func TestFormViewportFollowsFocusAndValidationError(t *testing.T) {
	fields := []*Field{
		New("One", ""),
		New("Two", ""),
		New("Three", "").WithValidate(func(string) error { return errors.New("three is invalid") }),
		New("Four", ""),
	}
	f := NewForm(fields...)
	f.SetWidth(30)
	f.SetHeight(2) // the minimum dialog content budget at a 40x10 terminal
	f.Init()
	f.FocusIndex(2)
	view := ansi.Strip(f.View())
	if len(strings.Split(view, "\n")) > 2 || !strings.Contains(view, "Three") {
		t.Fatalf("clamped viewport must show focused field:\n%s", view)
	}
	f.Update(key("enter")) // advance
	f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	view = ansi.Strip(f.View())
	if len(strings.Split(view, "\n")) > 2 || !strings.Contains(view, "Three") || !strings.Contains(view, "three is invalid") {
		t.Fatalf("validation should focus and reveal the first error:\n%s", view)
	}
}

// A12: the tuple form hides its last field unless a condition is written, and
// moveFocus wraps past hidden fields, so enter measured from the last field
// alone went round and round and never submitted.
func TestEnterOnTheLastVisibleFieldSubmits(t *testing.T) {
	f := NewForm(New("a", ""), New("b", ""), New("c", ""))
	f.SetVisible(2, false)
	f.Init()
	f.Update(key("tab"))
	f.Update(key("enter"))
	if !f.Completed() {
		t.Fatalf("focus = %d, want the form submitted from the last visible field", f.FocusedIndex())
	}
}

// A hidden field does not apply, so its value cannot hold the form back: a
// failing validator there sent focus to a field nobody could see, and enter
// never got out.
func TestAHiddenInvalidFieldDoesNotBlockTheSubmit(t *testing.T) {
	f := NewForm(
		New("a", ""),
		New("b", ""),
		New("c", "").WithValidate(func(string) error { return errors.New("c is invalid") }),
	)
	f.SetVisible(2, false)
	f.Init()
	f.Update(key("tab"))
	f.Update(key("enter"))
	if !f.Completed() {
		t.Fatalf("focus = %d, want the form submitted past the hidden invalid field", f.FocusedIndex())
	}
}

// textinput works out which part of a long value is on screen when its caret
// moves, not when its width changes, so a value set before the form had a
// width, or one windowed for a wider form, drew past the edge after a resize.
func TestAResizeRewindowsALongValue(t *testing.T) {
	long := "organization:" + strings.Repeat("x", 300)
	for _, c := range []struct {
		name  string
		first int // the width the value was set at; 0 is none yet
	}{{"filled before it had a width", 0}, {"shrunk from 120", 120}} {
		t.Run(c.name, func(t *testing.T) {
			f := NewForm(New("Object", ""))
			f.Init()
			if c.first > 0 {
				f.SetWidth(c.first)
			}
			f.SetValues([]string{long})
			f.SetCursor(0, 20)
			f.SetWidth(40)
			for i, line := range strings.Split(f.View(), "\n") {
				if w := lipgloss.Width(line); w > 40 {
					t.Fatalf("line %d is %d cells wide at width 40", i, w)
				}
			}
			if got := f.Cursor(0); got != 20 {
				t.Fatalf("caret = %d, want it left at 20", got)
			}
			if got := f.Values()[0]; got != long {
				t.Fatalf("the value changed: %d bytes, want %d", len(got), len(long))
			}
		})
	}
}

// A tab shows as a space, so the words it separates stay apart, and Source
// gives back the value as set until the user edits it.
func TestAnAreaShowsATabAsASpaceAndGivesBackItsSource(t *testing.T) {
	const when = "input.enabled\tand\tinput.admin"
	f := NewForm(NewArea("When", ""), New("Name", ""))
	f.Init()
	f.SetValues([]string{when, "r\x1b[2J"})
	if got := f.Values()[0]; got != "input.enabled and input.admin" {
		t.Fatalf("value = %q, want the tabs shown as spaces", got)
	}
	if f.Source(0) != when || f.Source(1) != "r\x1b[2J" {
		t.Fatalf("source = %q, %q; want the values as set", f.Source(0), f.Source(1))
	}
	f.Insert(0, "x")
	if got := f.Source(0); got != "xinput.enabled and input.admin" {
		t.Fatalf("source = %q, want the edit", got)
	}
	// Undoing the edit gives the value as set back, not the text shown.
	f.SetCursor(0, 1)
	f.Update(key("backspace"))
	if got := f.Source(0); got != when {
		t.Fatalf("source = %q after the undo, want %q", got, when)
	}
	f.Reset()
	if got := f.Source(0); got != "" {
		t.Fatalf("source = %q after Reset", got)
	}
}

// An area keeps its line breaks, and its caret is a rune offset into the
// whole value, newlines included, like a text field's.
func TestAnAreaKeepsLinesAndInsertsAtTheCaret(t *testing.T) {
	f := NewForm(NewArea("When", ""))
	f.Init()
	f.SetValues([]string{"ab\ncd\x1b[2J"})
	if got := f.Values()[0]; got != "ab\ncd" {
		t.Fatalf("value = %q, want the lines kept and the control dropped", got)
	}
	f.SetCursor(0, 4)
	if got := f.Cursor(0); got != 4 {
		t.Fatalf("cursor = %d, want 4", got)
	}
	f.Insert(0, "X\u202e")
	if got := f.Values()[0]; got != "ab\ncXd" || f.Cursor(0) != 5 {
		t.Fatalf("value = %q, cursor = %d", got, f.Cursor(0))
	}
	f.Update(key("alt+enter"))
	f.Update(key("y"))
	if got := f.Values()[0]; got != "ab\ncX\nyd" {
		t.Fatalf("value = %q, want alt+enter to start a line", got)
	}
}

// Up and down move between an area's lines, and leave it only from its first
// or last line.
func TestUpAndDownLeaveAnAreaOnlyFromItsEdges(t *testing.T) {
	f := NewForm(New("Name", ""), NewArea("When", ""), New("After", ""))
	f.Init()
	f.SetValues([]string{"", "one\ntwo"})
	f.Update(key("down"))
	if f.FocusedIndex() != 1 {
		t.Fatalf("focus = %d, want the area", f.FocusedIndex())
	}
	f.Update(key("down"))
	if f.FocusedIndex() != 1 || f.Cursor(1) < len("one\n") {
		t.Fatalf("focus = %d, cursor = %d; want the area's second line", f.FocusedIndex(), f.Cursor(1))
	}
	f.Update(key("down"))
	if f.FocusedIndex() != 2 {
		t.Fatalf("focus = %d, want the field after the area", f.FocusedIndex())
	}
}
