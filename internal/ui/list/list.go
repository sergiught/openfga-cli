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
	Model      list.Model
	delegate   list.DefaultDelegate
	compact    bool
	filterHint string
}

// New creates a list with the themed delegate.
func New() *List {
	d := list.NewDefaultDelegate()
	l := &List{delegate: d}
	model := list.New(nil, &l.delegate, 0, 0)
	model.SetShowHelp(false)
	model.SetShowStatusBar(false)
	// The title bar line is already reserved whenever filtering is enabled (the
	// "/" filter input is drawn there). Turn the title on so that reserved line
	// carries a faint "press / to filter" hint when the user isn't filtering —
	// no extra height, just discoverability. The filter input replaces it the
	// moment "/" is pressed.
	model.SetShowTitle(true)
	model.Styles.TitleBar = lipgloss.NewStyle().Padding(0, 0, 0, 2)
	model.Styles.Title = style.Faint
	model.FilterInput.Prompt = "filter: "
	// The app owns quitting (ctrl+c / q are handled by the playground's key
	// router). Leaving the list's built-in q/esc quit bindings active would let
	// a bare q hard-quit the whole TUI from any list-backed section, bypassing
	// that routing.
	model.DisableQuitKeybindings()
	l.Model = model
	l.Restyle()
	return l
}

// Restyle rebuilds the delegate styling from the current theme.
func (l *List) Restyle() {
	if l.compact {
		l.delegate.ShowDescription = false
		l.delegate.SetSpacing(0)
	} else {
		l.delegate.ShowDescription = true
		l.delegate.SetSpacing(1)
	}
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
	cmd := l.Model.SetItems(rows)
	l.applyFilterHint()
	return cmd
}

// SetFilterHint sets the faint helper text shown in place of the title while
// the user is not actively filtering, advertising the "/" filter. It is only
// shown when the list has items; an empty list shows nothing.
func (l *List) SetFilterHint(hint string) {
	l.filterHint = hint
	l.applyFilterHint()
}

// SetFilterPlaceholder sets the example text shown inside the "/" filter input
// before the user types anything, hinting at what can be matched.
func (l *List) SetFilterPlaceholder(ph string) {
	l.Model.FilterInput.Placeholder = ph
}

// applyFilterHint shows the hint only when there are rows to filter.
func (l *List) applyFilterHint() {
	if len(l.Model.Items()) == 0 {
		l.Model.Title = ""
		return
	}
	l.Model.Title = l.filterHint
}

// SetSize sets the list dimensions.
func (l *List) SetSize(width, height int) {
	l.Model.SetWidth(width)
	l.Model.SetHeight(height)
	l.Restyle()
}

// SetCompact toggles single-line rows (title only, no inter-row spacing) and
// restyles. Callers that want the description folded into the row build a
// combined single-line title before SetItems; this only controls the delegate.
func (l *List) SetCompact(b bool) {
	l.compact = b
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

// IndexAt maps a 0-based visible row (from the top of the rendered list) to the
// absolute item index, or -1 if the row is past the last visible item. It
// accounts for the delegate's item height + spacing and the current page.
func (l *List) IndexAt(row int) int {
	if row < 0 {
		return -1
	}
	stride := l.delegate.Height() + l.delegate.Spacing()
	if stride < 1 {
		stride = 1
	}
	itemInPage := row / stride
	p := l.Model.Paginator
	if p.PerPage > 0 && itemInPage >= p.PerPage {
		return -1
	}
	abs := p.Page*p.PerPage + itemInPage
	if abs < 0 || abs >= len(l.Model.Items()) {
		return -1
	}
	return abs
}

// SelectIndex highlights the item at the given absolute index.
func (l *List) SelectIndex(i int) { l.Model.Select(i) }

// View renders the list.
func (l *List) View() string { return l.Model.View() }

// Update forwards a message to the underlying list model.
func (l *List) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	l.Model, cmd = l.Model.Update(msg)
	return cmd
}
