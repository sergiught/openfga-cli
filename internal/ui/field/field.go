// Package field provides a minimal themed form: labeled text inputs with an
// accent bar on focus, inline validation, tab-cycled focus, and enter-to-submit.
// It replaces huh, which pinned bubbletea v1 and capped the visual ceiling.
package field

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

type fieldKind int

const (
	kindText fieldKind = iota
	kindToggle
	kindSelect
)

// Field is one labeled input: a text entry, a two-choice toggle, or a
// cycle-through select.
type Field struct {
	kind     fieldKind
	label    string
	in       textinput.Model // kindText
	on       bool            // kindToggle value
	onLabel  string          // kindToggle labels for the two choices
	offLabel string
	options  []string // kindSelect choices
	sel      int      // kindSelect index
	validate func(string) error
	err      string
}

// New returns a text field with the given label and placeholder.
func New(label, placeholder string) *Field {
	in := textinput.New()
	in.Placeholder = placeholder
	in.Prompt = ""
	// Theme the input so it matches the rest of the UI: the library defaults
	// leak a 256-color placeholder gray and a white reverse-video cursor.
	st := in.Styles()
	text := lipgloss.NewStyle().Foreground(style.Fg)
	placeholderStyle := lipgloss.NewStyle().Foreground(style.Faintc)
	st.Focused.Text, st.Focused.Placeholder = text, placeholderStyle
	st.Blurred.Text, st.Blurred.Placeholder = text, placeholderStyle
	st.Cursor.Color = style.Primary
	in.SetStyles(st)
	return &Field{kind: kindText, label: label, in: in}
}

// NewToggle returns a two-choice toggle field (◉ on / ○ off). Space or ←→ flip
// the choice; its value is "true"/"false".
func NewToggle(label, onLabel, offLabel string, on bool) *Field {
	return &Field{kind: kindToggle, label: label, onLabel: onLabel, offLabel: offLabel, on: on}
}

// NewSelect returns a field that cycles through options with ←→ or space; its
// value is the selected option string.
func NewSelect(label string, options []string, initial int) *Field {
	if initial < 0 || initial >= len(options) {
		initial = 0
	}
	return &Field{kind: kindSelect, label: label, options: options, sel: initial}
}

// WithValidate attaches a validation function, run on submit.
func (f *Field) WithValidate(fn func(string) error) *Field {
	f.validate = fn
	return f
}

// Secret marks a text field as sensitive: its value is echoed as a dot mask
// while typing so API tokens and client secrets are never shown in cleartext.
// No-op on non-text fields.
func (f *Field) Secret() *Field {
	if f.kind == kindText {
		f.in.EchoMode = textinput.EchoPassword
		f.in.EchoCharacter = '•'
	}
	return f
}

// --- per-field behavior, dispatched on kind ---

func (f *Field) focus() tea.Cmd {
	f.err = "" // clear any inline error while the user edits; re-checked on blur
	if f.kind != kindText {
		return nil
	}
	return f.in.Focus()
}

func (f *Field) blur() {
	if f.kind == kindText {
		f.in.Blur()
	}
	// Validate on tab-off so a bad value is flagged inline before submit.
	if f.validate != nil {
		if err := f.validate(f.value()); err != nil {
			f.err = err.Error()
		} else {
			f.err = ""
		}
	}
}

func (f *Field) update(msg tea.Msg) tea.Cmd {
	switch f.kind {
	case kindToggle:
		if k, ok := msg.(tea.KeyPressMsg); ok {
			switch k.String() {
			case " ", "space", "left", "right":
				f.on = !f.on
			}
		}
		return nil
	case kindSelect:
		if k, ok := msg.(tea.KeyPressMsg); ok && len(f.options) > 0 {
			switch k.String() {
			case "right", " ", "space":
				f.sel = (f.sel + 1) % len(f.options)
			case "left":
				f.sel = (f.sel - 1 + len(f.options)) % len(f.options)
			}
		}
		return nil
	}
	var cmd tea.Cmd
	f.in, cmd = f.in.Update(msg)
	return cmd
}

func (f *Field) value() string {
	switch f.kind {
	case kindToggle:
		return strconv.FormatBool(f.on)
	case kindSelect:
		if len(f.options) == 0 {
			return ""
		}
		return f.options[f.sel]
	}
	return f.in.Value()
}

func (f *Field) setValue(s string) {
	switch f.kind {
	case kindToggle:
		f.on = s == "true" || s == "allowed" || s == "Allowed" || s == "t"
		return
	case kindSelect:
		for i, o := range f.options {
			if o == s {
				f.sel = i
				return
			}
		}
		return
	}
	f.in.SetValue(style.SanitizeTerminal(s))
}

func (f *Field) setWidth(w int) {
	if f.kind == kindText {
		f.in.SetWidth(w)
	}
}

// inputView renders the field's input line (no label), honoring focus and the
// optional highlight background.
func (f *Field) inputView(focused bool, hl color.Color) string {
	if f.kind == kindText {
		return f.in.View()
	}
	base := lipgloss.NewStyle()
	if hl != nil && focused {
		base = base.Background(hl)
	}
	if f.kind == kindSelect {
		cur := ""
		if len(f.options) > 0 {
			cur = f.options[f.sel]
		}
		c := style.Fg
		if focused {
			c = style.Primary
		}
		capSt := base.Foreground(style.Faintc)
		return capSt.Render("‹ ") + base.Bold(true).Foreground(c).Render(cur) + capSt.Render(" ›")
	}
	choice := func(sel bool, label string) string {
		mark, st := "○", base.Foreground(style.Faintc)
		if sel {
			mark = "◉"
			c := style.Fg
			if focused {
				c = style.Primary
			}
			st = base.Bold(true).Foreground(c)
		}
		return st.Render(mark + " " + label)
	}
	return choice(f.on, f.onLabel) + base.Render("   ") + choice(!f.on, f.offLabel)
}

// Form owns focus order and completion state for its fields.
type Form struct {
	fields    []*Field
	focus     int
	completed bool
	width     int         // content width, for the focused row's full-width highlight
	height    int         // maximum rendered rows; 0 means unconstrained
	highlight color.Color // focused-row background; nil disables highlighting
}

// NewForm builds a form; the first field takes focus on Init.
func NewForm(fields ...*Field) *Form { return &Form{fields: fields} }

// SetHighlight sets the background that marks the focused field's row (label +
// input). Unfocused fields render with no background so only the active field
// is emphasized. The focused input carries the same background so its row fills
// cleanly; blurred inputs stay transparent.
func (f *Form) SetHighlight(c color.Color) {
	f.highlight = c
	for _, fl := range f.fields {
		if fl.kind != kindText {
			continue // toggles carry the highlight via inputView
		}
		st := fl.in.Styles()
		st.Focused.Text = lipgloss.NewStyle().Foreground(style.Fg).Background(c)
		st.Focused.Placeholder = lipgloss.NewStyle().Foreground(style.Faintc).Background(c)
		st.Blurred.Text = lipgloss.NewStyle().Foreground(style.Fg)
		st.Blurred.Placeholder = lipgloss.NewStyle().Foreground(style.Faintc)
		fl.in.SetStyles(st)
	}
}

// SetWidth sizes every input to the available content width.
func (f *Form) SetWidth(w int) {
	f.width = w
	iw := w - 3 // accent bar (2 cols) + gap
	if iw < 1 {
		iw = 1
	}
	for _, fl := range f.fields {
		fl.setWidth(iw)
	}
}

// SetHeight clamps the rendered form to a scrolling window. The window follows
// the focused field, including its inline validation error when possible.
func (f *Form) SetHeight(h int) {
	if h < 1 {
		h = 1
	}
	f.height = h
}

// Init focuses the first field.
func (f *Form) Init() tea.Cmd {
	if len(f.fields) == 0 {
		return nil
	}
	f.focus = 0
	return f.fields[0].focus()
}

// Update routes keys: tab/down and shift+tab/up cycle, enter submits,
// everything else edits the focused field.
func (f *Form) Update(msg tea.Msg) tea.Cmd {
	if len(f.fields) == 0 || f.completed {
		return nil
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "tab", "down":
			return f.moveFocus(1)
		case "shift+tab", "up":
			return f.moveFocus(-1)
		case "enter":
			// Enter advances to the next field; on the last field it submits.
			if f.focus >= len(f.fields)-1 {
				f.submit()
				return nil
			}
			return f.moveFocus(1)
		case "ctrl+s", "ctrl+enter":
			// Submit from any field (e.g. skip optional trailing fields). ctrl+s
			// is the portable binding; ctrl+enter also works where the terminal
			// can distinguish it.
			f.submit()
			return nil
		}
	}
	return f.fields[f.focus].update(msg)
}

func (f *Form) moveFocus(d int) tea.Cmd {
	f.fields[f.focus].blur()
	f.focus = (f.focus + d + len(f.fields)) % len(f.fields)
	return f.fields[f.focus].focus()
}

// FocusIndex focuses the field at index i (clamped), blurring the current one.
// Used to keep the cursor on a control across a form rebuild.
func (f *Form) FocusIndex(i int) tea.Cmd {
	if len(f.fields) == 0 {
		return nil
	}
	if i < 0 {
		i = 0
	}
	if i >= len(f.fields) {
		i = len(f.fields) - 1
	}
	f.fields[f.focus].blur()
	f.focus = i
	return f.fields[i].focus()
}

func (f *Form) submit() {
	ok := true
	firstInvalid := -1
	for i, fl := range f.fields {
		fl.err = ""
		if fl.validate != nil {
			if err := fl.validate(fl.value()); err != nil {
				fl.err = err.Error()
				ok = false
				if firstInvalid < 0 {
					firstInvalid = i
				}
			}
		}
	}
	f.completed = ok
	if !ok && firstInvalid >= 0 && firstInvalid != f.focus {
		f.fields[f.focus].blur()
		f.focus = firstInvalid
		if f.fields[f.focus].kind == kindText {
			f.fields[f.focus].in.Focus()
		}
	}
}

// Completed reports whether a submit passed validation.
func (f *Form) Completed() bool { return f.completed }

// Resume reopens a completed form without discarding its values, allowing
// application-level validation errors to be corrected in place.
func (f *Form) Resume() {
	f.completed = false
	if len(f.fields) > 0 {
		f.fields[f.focus].focus()
	}
}

// Values returns every field's current value, in field order (toggles as
// "true"/"false").
func (f *Form) Values() []string {
	vals := make([]string, len(f.fields))
	for i, fl := range f.fields {
		vals[i] = fl.value()
	}
	return vals
}

// SetValues fills each field with the corresponding entry in vals, in field
// order. Bounds-safe: extra vals are ignored, missing ones leave the field
// untouched.
func (f *Form) SetValues(vals []string) {
	for i, fl := range f.fields {
		if i < len(vals) {
			fl.setValue(vals[i])
		}
	}
}

// Reset clears values, errors, and completion, refocusing the first field.
func (f *Form) Reset() {
	for _, fl := range f.fields {
		fl.setValue("")
		fl.err = ""
		fl.blur()
	}
	f.completed = false
	f.focus = 0
	if len(f.fields) > 0 {
		f.fields[0].focus()
	}
}

// View renders fields stacked: label, input, optional error. The focused
// field's whole row (label + input) is filled with the highlight; the others
// render plain so only the active field is emphasized.
func (f *Form) View() string {
	blocks := make([][]string, len(f.fields))
	starts := make([]int, len(f.fields))
	var lines []string
	for i, fl := range f.fields {
		focused := i == f.focus && !f.completed
		var b strings.Builder
		if focused && f.highlight != nil {
			hl := lipgloss.NewStyle().Background(f.highlight)
			label := hl.Bold(true).Foreground(style.Primary).Width(f.width).Render(fl.label)
			b.WriteString(label + "\n" + hl.Width(f.width).Render(fl.inputView(true, f.highlight)))
		} else {
			label := lipgloss.NewStyle().Foreground(style.Muted).Render(fl.label)
			b.WriteString(label + "\n" + fl.inputView(false, nil))
		}
		if fl.err != "" {
			b.WriteString("\n" + lipgloss.NewStyle().Foreground(style.Red).Render("  "+fl.err))
		}
		block := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
		blocks[i] = block
		starts[i] = len(lines)
		if i > 0 {
			lines = append(lines, "")
			starts[i]++
		}
		lines = append(lines, block...)
	}
	if f.height > 0 && len(lines) > f.height {
		focusBlock := blocks[f.focus]
		if f.fields[f.focus].err != "" && len(focusBlock) > f.height {
			if f.height == 1 {
				return focusBlock[len(focusBlock)-1]
			}
			compact := append([]string(nil), focusBlock[:f.height]...)
			compact[f.height-1] += "  " + focusBlock[len(focusBlock)-1]
			return strings.Join(compact, "\n")
		}
		start := starts[f.focus]
		end := start + len(blocks[f.focus])
		if end-start >= f.height {
			if f.fields[f.focus].err != "" {
				start = end - f.height
			} else {
				end = start + f.height
			}
		} else {
			padding := (f.height - (end - start)) / 2
			start -= padding
			if start < 0 {
				start = 0
			}
			end = start + f.height
			if end > len(lines) {
				end = len(lines)
				start = end - f.height
			}
		}
		lines = lines[start:end]
	}
	return strings.Join(lines, "\n")
}
