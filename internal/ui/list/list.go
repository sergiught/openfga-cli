// Package list wraps bubbles/list with a task-pilot-style delegate: a thick
// left-border selection accent, title+description rows, and built-in filtering.
// Styling is driven by the active theme and can be refreshed via Restyle.
package list

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	// v1), so cap the rendered row at width-1 to keep it within the list width.
	// Use MaxWidth (truncate) rather than Width (which forces a wrap): a title the
	// `/` filter has styled with FilterMatch is ANSI-laden, and wrapping it splits
	// the highlighted row across several lines and tears the viewport. MaxWidth
	// keeps the row on one line while preserving the selection border and padding.
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
			Padding(0, 0, 0, 1).MaxWidth(selWidth),
		SelectedDesc: lipgloss.NewStyle().
			Border(lipgloss.ThickBorder(), false, false, false, true).
			BorderForeground(style.Secondary).
			Foreground(style.Muted).
			Padding(0, 0, 0, 1).MaxWidth(selWidth),
		DimmedTitle: lipgloss.NewStyle().Foreground(style.Muted).Padding(0, 0, 0, 2),
		DimmedDesc:  lipgloss.NewStyle().Foreground(style.Faintc).Padding(0, 0, 0, 2),
		FilterMatch: lipgloss.NewStyle().Underline(true).Foreground(style.Keyword),
	}
	l.Model.SetDelegate(&l.delegate)
}

// SetItems replaces the list items, re-running an applied "/" filter over them.
//
// bubbles hands that re-run back as a command carrying a FilterMatchesMsg, but
// that message is applied by whichever list receives it — so a caller that
// routes messages by "the section on screen" can hand one list's matches to
// another, or drop it entirely and leave a filtered list rendering nothing. It
// is pure fuzzy matching over the items just set, with nothing to wait on, so
// it runs inline here instead and never escapes. (bubbles does the same in its
// own Model.SetFilterText.)
func (l *List) SetItems(items []Item) {
	rows := make([]list.Item, len(items))
	for i, it := range items {
		rows[i] = it
	}
	if cmd := l.Model.SetItems(rows); cmd != nil {
		// Update early-returns on a FilterMatchesMsg, so the command it hands back
		// is always nil.
		if msg := cmd(); msg != nil {
			l.Model, _ = l.Model.Update(msg)
		}
	}
	// SetItems paginates from the rows it is replacing, not the ones it just set:
	// with a filter applied the matches do not exist yet, and either way the row
	// budget and the page count are computed from each other — the first pass
	// sizes the rows while the pager still reads the old count, and only then sets
	// the new one. A multi-page pager is a line taller than a single-page one in
	// compact view, so a stale count leaves the list a line too tall, which pushes
	// the app's footer off screen. Two passes reach the fixed point; SetSize is
	// the exported way to reach the same repagination SetFilterText does.
	l.Model.SetSize(l.Model.Width(), l.Model.Height())
	l.Model.SetSize(l.Model.Width(), l.Model.Height())
	// A reload can leave fewer rows than the cursor was sitting on. bubbles
	// rebuilds the paginator but not the cursor within the page, so on a partly
	// filled last page the cursor can end up past the last visible row and the
	// pane renders with nothing highlighted until an arrow key nudges it. This
	// is not filter-specific; an unfiltered list shrinks the same way.
	if n := len(l.Model.VisibleItems()); n > 0 && l.Model.Index() >= n {
		l.Model.Select(n - 1)
	}
	l.applyFilterHint()
}

// ResetFilter clears an applied "/" filter and puts the title bar back to its
// key hint. Callers must not reach through to Model.ResetFilter, which leaves
// the title naming a filter that is no longer applied.
func (l *List) ResetFilter() {
	l.Model.ResetFilter()
	l.applyFilterHint()
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

// SetFilterPrompt overrides the "filter: " label shown at the head of the "/"
// input.
func (l *List) SetFilterPrompt(p string) {
	l.Model.FilterInput.Prompt = p
}

// applyFilterHint fills the title bar: the term of an applied "/" filter if
// there is one, otherwise the hint advertising the key — and nothing at all
// when there are no rows to filter.
//
// Naming the applied term matters because bubbles hides the input once the
// filter is accepted, so an applied filter is otherwise invisible: the rows are
// narrowed and nothing on screen says by what.
func (l *List) applyFilterHint() {
	switch {
	case l.Model.FilterState() == list.FilterApplied:
		l.Model.Title = truncateHint(l.Model.FilterInput.Prompt+l.Model.FilterValue(), l.hintWidth())
	case len(l.Model.Items()) == 0:
		l.Model.Title = ""
	default:
		l.Model.Title = truncateHint(l.filterHint, l.hintWidth())
	}
}

// hintWidth is the room the title bar has. Measured, not derived: bubbles
// truncates the title against the pane width but renders it inside a padded
// bar, so the last few columns overflow — a title of Width()-3 already puts the
// line one column over, and an overflowing title wraps, which costs the app a
// row and pushes its footer off screen. TestTitleBarBudget and
// TestTitleBarBudgetIsTight pin it from above and below.
func (l *List) hintWidth() int { return l.Model.Width() - 4 }

// truncateHint keeps the title bar inside its pane: overflowing it wraps, and
// the extra line pushes the app's own footer off screen.
func truncateHint(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// SetSize sets the list dimensions.
func (l *List) SetSize(width, height int) {
	l.Model.SetWidth(width)
	l.Model.SetHeight(height)
	// The title is truncated against the pane, so a narrower pane needs it
	// recomputed — otherwise a title sized for the old width overflows, wraps,
	// and costs the app the row its footer sits on until some message happens to
	// reach this list again.
	l.applyFilterHint()
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
	// The title/filter bar is always rendered above the item viewport. Mouse
	// rows are relative to the whole list, so remove that persistent row before
	// mapping into the delegate's compact or regular item stride.
	if l.Model.ShowTitle() {
		row--
	}
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

// SelectID highlights an item by ID within the currently visible (filtered)
// rows. It returns false when the filter excludes the item.
func (l *List) SelectID(id string) bool {
	for i, row := range l.Model.VisibleItems() {
		if item, ok := row.(Item); ok && item.ID == id {
			l.Model.Select(i)
			return true
		}
	}
	return false
}

// View renders the list.
func (l *List) View() string { return l.Model.View() }

// Update forwards a message to the underlying list model.
func (l *List) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	l.Model, cmd = l.Model.Update(msg)
	// This is where the filter is applied and cleared, so it is where the title
	// bar has to be recomputed — driving it from SetItems alone left the title a
	// reload behind the state it describes, in both directions.
	l.applyFilterHint()
	return cmd
}
