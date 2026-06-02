// Package layout provides task-pilot-style bordered flexbox panels: a rounded
// panel with the title centered inside its top border, an active/passive border
// color, and a centered, truncated help row beneath. It is adapted from
// task-pilot-cli and driven by the active theme via the style package.
package layout

import (
	"github.com/76creates/stickers/flexbox"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/style"
)

func borderActive() lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(style.Primary)
}

func borderPassive() lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(style.Subtle)
}

// Column is a bordered panel containing a centered title and content.
type Column struct {
	cell    *flexbox.Cell
	title   string
	content string
	gapSize int
	active  bool
}

// GetContentSize returns the interior width and height available for content.
func (c *Column) GetContentSize() (int, int) {
	const titleHeight = 1
	b := borderPassive()
	width := c.cell.GetWidth() - b.GetHorizontalBorderSize()*2
	height := c.cell.GetHeight() - b.GetVerticalBorderSize()*2 - titleHeight - c.gapSize
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return width, height
}

// IsActive reports whether the column is active.
func (c *Column) IsActive() bool { return c.active }

// SetActive sets the active state and updates the border color.
func (c *Column) SetActive(active bool) {
	c.active = active
	c.cell.SetStyle(borderPassive())
	if c.active {
		c.cell.SetStyle(borderActive())
	}
}

// SetTitle centers the title within the panel width and renders it inside a
// matching border so it reads as a header bar.
func (c *Column) SetTitle(title string) {
	border := borderPassive()
	if c.active {
		border = borderActive()
	}
	columnWidth := c.cell.GetWidth() - border.GetHorizontalBorderSize()*2
	centered := lipgloss.PlaceHorizontal(columnWidth, lipgloss.Center, title)
	c.title = border.Width(columnWidth).Render(centered)
}

// SetContent sets the column's body content.
func (c *Column) SetContent(content string) { c.content = content }

func (c *Column) gap() string {
	out := ""
	for i := 0; i < c.gapSize; i++ {
		out += "\n"
	}
	return out
}

func (c *Column) render() { c.cell.SetContent(c.title + c.gap() + c.content) }

// SingleColumn is a full-width bordered panel with a help row beneath it.
type SingleColumn struct {
	flexBox *flexbox.FlexBox
	helpRow *flexbox.Cell
	Column  *Column
}

// NewSingleColumn returns a new single-column layout.
func NewSingleColumn() *SingleColumn {
	return &SingleColumn{
		flexBox: flexbox.New(0, 0),
		Column: &Column{
			cell:    flexbox.NewCell(1, 12).SetStyle(borderActive()),
			gapSize: 1,
			active:  true,
		},
		helpRow: flexbox.NewCell(1, 1),
	}
}

// GetWidth returns the layout width.
func (l *SingleColumn) GetWidth() int { return l.flexBox.GetWidth() }

// GetHeight returns the layout height.
func (l *SingleColumn) GetHeight() int { return l.flexBox.GetHeight() }

// SetHelp sets the centered, truncated help row content.
func (l *SingleColumn) SetHelp(content string) {
	l.helpRow.SetContent(lipgloss.PlaceHorizontal(
		l.GetWidth(), lipgloss.Center, ansi.Truncate(content, l.GetWidth(), "…")))
}

// SetSize lays out the panel above the help row.
func (l *SingleColumn) SetSize(width, height int) {
	l.flexBox.SetWidth(width)
	l.flexBox.SetHeight(height)
	l.flexBox.SetRows([]*flexbox.Row{
		l.flexBox.NewRow().AddCells(l.Column.cell),
		l.flexBox.NewRow().AddCells(l.helpRow),
	})
	l.flexBox.ForceRecalculate()
}

// View renders the layout.
func (l *SingleColumn) View() string {
	l.Column.render()
	return l.flexBox.Render()
}
