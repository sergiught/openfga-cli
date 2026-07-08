// Package list wraps bubbles/list with a task-pilot-style delegate: a thick
// left-border selection accent, title+description rows, and built-in filtering.
// Styling is driven by the active theme and can be refreshed via Restyle.
package list

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// Item is a generic list row carrying display text plus an id/index payload.
type Item struct {
	TitleText string
	DescText  string
	Filter    string
	ID        string
	Index     int
}

// Title implements list.DefaultItem.
func (i Item) Title() string { return i.TitleText }

// Description implements list.DefaultItem.
func (i Item) Description() string { return i.DescText }

// FilterValue implements list.Item.
func (i Item) FilterValue() string {
	if i.Filter != "" {
		return i.Filter
	}
	return i.TitleText
}

var _ list.DefaultItem = Item{}

// List wraps a bubbles list.Model.
type List struct {
	Model    list.Model
	delegate list.DefaultDelegate
}

// New creates a list with the themed delegate.
func New() *List {
	d := list.NewDefaultDelegate()
	d.SetSpacing(1)
	l := &List{delegate: d}
	model := list.New(nil, &l.delegate, 0, 0)
	model.SetShowHelp(false)
	model.SetShowTitle(false)
	model.SetShowStatusBar(false)
	l.Model = model
	l.Restyle()
	return l
}

// Restyle rebuilds the delegate styling from the current theme.
func (l *List) Restyle() {
	width := l.Model.Width()
	// Selected rows carry a thick left border (1 col, outside Width in lipgloss
	// v1), so their content width must be width-1 to keep the rendered row within
	// the list width — otherwise every row is padded 1 col too wide and gets
	// clipped by the panel that contains the list.
	selWidth := width - 1
	if selWidth < 0 {
		selWidth = 0
	}
	l.delegate.Styles = list.DefaultItemStyles{
		NormalTitle: lipgloss.NewStyle().Foreground(style.Fg).Padding(0, 0, 0, 2),
		NormalDesc:  lipgloss.NewStyle().Foreground(style.Muted).Padding(0, 0, 0, 2),
		SelectedTitle: lipgloss.NewStyle().
			Border(lipgloss.ThickBorder(), false, false, false, true).
			BorderForeground(style.Secondary).
			Foreground(style.Primary).Bold(true).
			Padding(0, 0, 0, 1).Width(selWidth),
		SelectedDesc: lipgloss.NewStyle().
			Border(lipgloss.ThickBorder(), false, false, false, true).
			BorderForeground(style.Secondary).
			Foreground(style.Muted).
			Padding(0, 0, 0, 1).Width(selWidth),
		DimmedTitle: lipgloss.NewStyle().Foreground(style.Muted).Padding(0, 0, 0, 2),
		DimmedDesc:  lipgloss.NewStyle().Foreground(style.Faintc).Padding(0, 0, 0, 2),
		FilterMatch: lipgloss.NewStyle().Underline(true).Foreground(style.Keyword),
	}
	l.Model.SetDelegate(&l.delegate)
}

// SetItems replaces the list items.
func (l *List) SetItems(items []Item) tea.Cmd {
	rows := make([]list.Item, len(items))
	for i, it := range items {
		rows[i] = it
	}
	return l.Model.SetItems(rows)
}

// SetSize sets the list dimensions.
func (l *List) SetSize(width, height int) {
	l.Model.SetWidth(width)
	l.Model.SetHeight(height)
	l.Restyle()
}

// Selected returns the highlighted item and whether one exists.
func (l *List) Selected() (Item, bool) {
	if it := l.Model.SelectedItem(); it != nil {
		return it.(Item), true
	}
	return Item{}, false
}

// SettingFilter reports whether the user is currently typing a filter.
func (l *List) SettingFilter() bool { return l.Model.SettingFilter() }

// View renders the list.
func (l *List) View() string { return l.Model.View() }

// Update forwards a message to the underlying list model.
func (l *List) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	l.Model, cmd = l.Model.Update(msg)
	return cmd
}
