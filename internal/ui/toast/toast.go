// Package toast renders transient bottom-right status chips that auto-expire.
// Multiple toasts stack; errors linger longer; long text wraps rather than
// being truncated.
package toast

import (
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
)

// Level selects the toast's icon and color.
type Level int

const (
	Info Level = iota
	Success
	Error
)

const (
	maxToasts  = 4  // stack cap; oldest drop off
	wrapWidth  = 40 // text wrap width (content columns)
	infoDwell  = 3 * time.Second
	errorDwell = 12 * time.Second // errors linger longer so they can be read, then clear
)

type expireMsg struct{ id int }

type entry struct {
	id    int
	text  string
	level Level
}

// Model holds a stack of active toasts (newest at the bottom).
type Model struct {
	nextID int
	items  []entry
}

// New returns an empty toast model.
func New() Model { return Model{} }

// Push adds a toast and returns the command that expires it. Errors dwell
// longer than info/success so a failure can be read, but every toast eventually
// auto-expires so none linger on screen indefinitely. When the stack is full the
// oldest toast drops off.
func (m *Model) Push(l Level, text string) tea.Cmd {
	m.nextID++
	id := m.nextID
	m.items = append(m.items, entry{id: id, text: text, level: l})
	if len(m.items) > maxToasts {
		m.items = m.items[len(m.items)-maxToasts:]
	}
	dwell := infoDwell
	if l == Error {
		dwell = errorDwell
	}
	return tea.Tick(dwell, func(time.Time) tea.Msg { return expireMsg{id: id} })
}

// Update removes a toast when its own timer fires (stale timers no-op).
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	if e, ok := msg.(expireMsg); ok {
		for i, t := range m.items {
			if t.id == e.id {
				m.items = append(m.items[:i], m.items[i+1:]...)
				break
			}
		}
	}
	return nil
}

// Active reports whether any toast is showing.
func (m Model) Active() bool { return len(m.items) > 0 }

// View renders the stack of chips, or "" when empty.
func (m Model) View() string {
	if len(m.items) == 0 {
		return ""
	}
	chips := make([]string, 0, len(m.items)*2)
	for i, t := range m.items {
		if i > 0 {
			chips = append(chips, "") // 1-row gap between chips
		}
		chips = append(chips, chip(t))
	}
	return lipgloss.JoinVertical(lipgloss.Right, chips...)
}

// chip renders one toast: a colored icon beside wrapped text on a raised card.
func chip(t entry) string {
	ic, c := icons.I().Dot, style.Info
	switch t.level {
	case Success:
		ic, c = icons.I().Check, style.Green
	case Error:
		ic, c = icons.I().Cross, style.Red
	}
	icon := lipgloss.NewStyle().Foreground(c).Background(style.BgRaised).Render(ic)
	body := lipgloss.NewStyle().
		Foreground(style.Fg).Background(style.BgRaised).
		Width(wrapWidth).Render(t.text)
	return lipgloss.NewStyle().Background(style.BgRaised).Padding(0, 1).Render(
		lipgloss.JoinHorizontal(lipgloss.Top, icon+" ", body))
}
