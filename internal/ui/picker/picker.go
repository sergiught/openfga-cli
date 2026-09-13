// Package picker is a small vertical single-select list with per-row
// descriptions. It is the chooser `ofga init` and `ofga mapping init` use for
// short, fixed sets of options, where bubbles' filterable list would be more
// machinery than the choice deserves.
package picker

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// Item is one row: a title, an optional description line, and the value the
// caller switches on once it is selected.
type Item struct {
	Title string
	Desc  string
	Value string
}

// Picker holds the rows and the cursor.
type Picker struct {
	items  []Item
	cursor int
}

// New builds a picker over items, with the first row selected.
func New(items []Item) *Picker { return &Picker{items: items} }

// Move advances the cursor by d rows, wrapping at both ends.
func (p *Picker) Move(d int) {
	if len(p.items) == 0 {
		return
	}
	p.cursor = (p.cursor + d + len(p.items)) % len(p.items)
}

// Selected returns the highlighted item, or the zero Item when there are none.
func (p *Picker) Selected() Item {
	if p.cursor < 0 || p.cursor >= len(p.items) {
		return Item{}
	}
	return p.items[p.cursor]
}

// SetCursor highlights row i, clamped into range. Used to pre-select the row
// matching a value carried in from a flag or an existing document.
func (p *Picker) SetCursor(i int) {
	switch {
	case len(p.items) == 0:
		p.cursor = 0
	case i < 0:
		p.cursor = 0
	case i >= len(p.items):
		p.cursor = len(p.items) - 1
	default:
		p.cursor = i
	}
}

// Cursor returns the highlighted row index.
func (p *Picker) Cursor() int { return p.cursor }

// Len returns the number of rows.
func (p *Picker) Len() int { return len(p.items) }

// View renders the rows stacked, the selected one marked and bolded.
func (p *Picker) View(width int) string {
	var b strings.Builder
	for i, it := range p.items {
		if i > 0 {
			b.WriteString("\n")
		}
		if i == p.cursor {
			b.WriteString(lipgloss.NewStyle().Foreground(style.Primary).Render("▌ "))
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(style.Fg).Render(it.Title))
		} else {
			b.WriteString("  " + lipgloss.NewStyle().Foreground(style.Muted).Render(it.Title))
		}
		if it.Desc != "" {
			b.WriteString("\n    " + lipgloss.NewStyle().Foreground(style.Faintc).Render(Clamp(it.Desc, width-4)))
		}
	}
	return b.String()
}

// Clamp truncates s to w display cells, marking the cut with an ellipsis. A
// non-positive width leaves s alone.
func Clamp(s string, w int) string {
	r := []rune(s)
	if w < 1 || len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}
