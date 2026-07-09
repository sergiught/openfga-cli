// Package field provides a minimal themed form: labeled text inputs with an
// accent bar on focus, inline validation, tab-cycled focus, and enter-to-submit.
// It replaces huh, which pinned bubbletea v1 and capped the visual ceiling.
package field

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// Field is one labeled input with optional validation.
type Field struct {
	label    string
	in       textinput.Model
	validate func(string) error
	err      string
}

// New returns a field with the given label and placeholder.
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
	f := &Field{label: label, in: in}
	return f
}

// WithValidate attaches a validation function, run on submit.
func (f *Field) WithValidate(fn func(string) error) *Field {
	f.validate = fn
	return f
}

// Form owns focus order and completion state for its fields.
type Form struct {
	fields    []*Field
	focus     int
	completed bool
	bg        color.Color // surface tint; nil renders transparently (flat pane)
}

// NewForm builds a form; the first field takes focus on Init.
func NewForm(fields ...*Field) *Form { return &Form{fields: fields} }

// SetBackground tints every field's chrome (labels, accent bar, input text,
// placeholder) with bg so the form sits flush on a colored surface — e.g. the
// raised modal panel — instead of the inputs punching base-colored holes in
// it. The flat query pane leaves this unset (transparent).
func (f *Form) SetBackground(bg color.Color) {
	f.bg = bg
	for _, fl := range f.fields {
		st := fl.in.Styles()
		st.Focused.Text = st.Focused.Text.Background(bg)
		st.Focused.Placeholder = st.Focused.Placeholder.Background(bg)
		st.Blurred.Text = st.Blurred.Text.Background(bg)
		st.Blurred.Placeholder = st.Blurred.Placeholder.Background(bg)
		fl.in.SetStyles(st)
	}
}

// tint applies the form's surface background to s when one is set.
func (f *Form) tint(s lipgloss.Style) lipgloss.Style {
	if f.bg == nil {
		return s
	}
	return s.Background(f.bg)
}

// SetWidth sizes every input to the available content width.
func (f *Form) SetWidth(w int) {
	iw := w - 3 // accent bar (2 cols) + gap
	if iw < 1 {
		iw = 1
	}
	for _, fl := range f.fields {
		fl.in.SetWidth(iw)
	}
}

// Init focuses the first field.
func (f *Form) Init() tea.Cmd {
	if len(f.fields) == 0 {
		return nil
	}
	f.focus = 0
	return f.fields[0].in.Focus()
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
			f.submit()
			return nil
		}
	}
	var cmd tea.Cmd
	f.fields[f.focus].in, cmd = f.fields[f.focus].in.Update(msg)
	return cmd
}

func (f *Form) moveFocus(d int) tea.Cmd {
	f.fields[f.focus].in.Blur()
	f.focus = (f.focus + d + len(f.fields)) % len(f.fields)
	return f.fields[f.focus].in.Focus()
}

func (f *Form) submit() {
	ok := true
	for _, fl := range f.fields {
		fl.err = ""
		if fl.validate != nil {
			if err := fl.validate(fl.in.Value()); err != nil {
				fl.err = err.Error()
				ok = false
			}
		}
	}
	f.completed = ok
}

// Completed reports whether a submit passed validation.
func (f *Form) Completed() bool { return f.completed }

// Values returns every field's current text, in field order.
func (f *Form) Values() []string {
	vals := make([]string, len(f.fields))
	for i, fl := range f.fields {
		vals[i] = fl.in.Value()
	}
	return vals
}

// SetValues fills each field's input with the corresponding entry in vals, in
// field order. Bounds-safe: extra vals are ignored, missing ones leave the
// field untouched.
func (f *Form) SetValues(vals []string) {
	for i, fl := range f.fields {
		if i < len(vals) {
			fl.in.SetValue(vals[i])
		}
	}
}

// Reset clears values, errors, and completion, refocusing the first field.
func (f *Form) Reset() {
	for _, fl := range f.fields {
		fl.in.SetValue("")
		fl.err = ""
		fl.in.Blur()
	}
	f.completed = false
	f.focus = 0
	if len(f.fields) > 0 {
		f.fields[0].in.Focus()
	}
}

// View renders fields stacked: label, accent-barred input, optional error.
func (f *Form) View() string {
	var b strings.Builder
	for i, fl := range f.fields {
		focused := i == f.focus && !f.completed
		label := f.tint(lipgloss.NewStyle().Foreground(style.Muted)).Render(fl.label)
		bar := f.tint(lipgloss.NewStyle()).Render("  ")
		if focused {
			label = f.tint(lipgloss.NewStyle().Bold(true).Foreground(style.Primary)).Render(fl.label)
			bar = f.tint(lipgloss.NewStyle().Foreground(style.Primary)).Render("▐▌")
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(label + "\n")
		b.WriteString(bar + f.tint(lipgloss.NewStyle()).Render(" ") + fl.in.View())
		if fl.err != "" {
			b.WriteString("\n" + f.tint(lipgloss.NewStyle().Foreground(style.Red)).Render("  "+fl.err))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
