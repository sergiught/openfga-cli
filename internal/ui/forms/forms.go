// Package forms builds task-pilot-style huh forms (themed inputs with indigo
// titles, gray borders, red-on-error) for the playground's query and mutation
// flows.
package forms

import (
	"github.com/charmbracelet/huh"

	"github.com/sergiught/openfga-cli/internal/style"
)

// Theme returns a huh theme fully recolored to the active Aurora palette, so
// forms never show huh's default purple.
func Theme() *huh.Theme {
	t := huh.ThemeCharm()

	// Focused field: cyan title, aqua prompt, mint cursor.
	t.Focused.Title = t.Focused.Title.Foreground(style.Primary).Bold(true)
	t.Focused.Base = t.Focused.Base.BorderForeground(style.Primary)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(style.Accent)
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(style.Secondary)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(style.Faintc)
	t.Focused.TextInput.Text = t.Focused.TextInput.Text.Foreground(style.Fg)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(style.Secondary)

	// Blurred (inactive) fields: muted, no purple.
	t.Blurred.Title = t.Blurred.Title.Foreground(style.Muted)
	t.Blurred.Base = t.Blurred.Base.BorderForeground(style.Subtle)
	t.Blurred.TextInput.Prompt = t.Blurred.TextInput.Prompt.Foreground(style.Faintc)
	t.Blurred.TextInput.Placeholder = t.Blurred.TextInput.Placeholder.Foreground(style.Faintc)
	t.Blurred.TextInput.Text = t.Blurred.TextInput.Text.Foreground(style.Muted)

	return t
}

// HighlightError recolors the form's focused border/title red.
func HighlightError(t *huh.Theme) {
	t.Focused.Base = t.Focused.Base.BorderForeground(style.Red)
	t.Focused.Title = t.Focused.Title.Foreground(style.Red)
}

// queryLabels returns the three input labels and placeholders for a mode.
func queryLabels(mode string) (labels [3]string, placeholders [3]string) {
	switch mode {
	case "list-objects":
		return [3]string{"Type", "Relation", "User"}, [3]string{"document", "viewer", "user:anne"}
	case "list-users":
		return [3]string{"Object", "Relation", "User type"}, [3]string{"document:roadmap", "viewer", "user"}
	default: // check
		return [3]string{"User", "Relation", "Object"}, [3]string{"user:anne", "viewer", "document:roadmap"}
	}
}

// Query builds a 3-input query form for the given mode. Read values with
// GetString("a"|"b"|"c").
func Query(mode string, width, height int) (*huh.Form, *huh.Theme) {
	labels, ph := queryLabels(mode)
	a := huh.NewInput().Key("a").Title(labels[0]).Placeholder(ph[0]).Prompt("› ")
	b := huh.NewInput().Key("b").Title(labels[1]).Placeholder(ph[1]).Prompt("› ")
	c := huh.NewInput().Key("c").Title(labels[2]).Placeholder(ph[2]).Prompt("› ")
	return build([]huh.Field{a, b, c}, width, height)
}

// WriteTuple builds the add-tuple form. Read with GetString("user"|"relation"|"object").
func WriteTuple(width, height int) (*huh.Form, *huh.Theme) {
	u := huh.NewInput().Key("user").Title("User").Placeholder("user:anne").Prompt("› ")
	r := huh.NewInput().Key("relation").Title("Relation").Placeholder("viewer").Prompt("› ")
	o := huh.NewInput().Key("object").Title("Object").Placeholder("document:roadmap").Prompt("› ")
	return build([]huh.Field{u, r, o}, width, height)
}

// CreateStore builds the create-store form. Read with GetString("name").
func CreateStore(width, height int) (*huh.Form, *huh.Theme) {
	n := huh.NewInput().Key("name").Title("Store name").Placeholder("my-store").Prompt("› ")
	return build([]huh.Field{n}, width, height)
}

func build(fields []huh.Field, width, height int) (*huh.Form, *huh.Theme) {
	t := Theme()
	f := huh.NewForm(huh.NewGroup(fields...)).
		WithTheme(t).
		WithShowHelp(false).
		WithShowErrors(false).
		WithWidth(width).
		WithHeight(height)
	return f, t
}
