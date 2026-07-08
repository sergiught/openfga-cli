// Package toast renders transient bottom-right status chips that auto-expire.
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

type expireMsg struct{ id int }

// Model holds at most one active toast (last write wins).
type Model struct {
	id     int
	text   string
	level  Level
	active bool
}

// New returns an empty toast model.
func New() Model { return Model{} }

// Push shows a toast and returns the command that expires it after 3s.
func (m *Model) Push(l Level, text string) tea.Cmd {
	m.id++
	m.text, m.level, m.active = text, l, true
	id := m.id
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return expireMsg{id: id} })
}

// Update expires the toast when its own timer fires (stale timers no-op).
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	if e, ok := msg.(expireMsg); ok && e.id == m.id {
		m.active = false
	}
	return nil
}

// Active reports whether a toast is showing.
func (m Model) Active() bool { return m.active }

// View renders the chip, or "" when inactive.
func (m Model) View() string {
	if !m.active {
		return ""
	}
	ic, c := icons.I().Dot, style.Info
	switch m.level {
	case Success:
		ic, c = icons.I().Check, style.Green
	case Error:
		ic, c = icons.I().Cross, style.Red
	}
	icon := lipgloss.NewStyle().Foreground(c).Background(style.BgRaised).Render(ic)
	return lipgloss.NewStyle().Background(style.BgRaised).Padding(0, 1).Render(
		icon + " " + lipgloss.NewStyle().Foreground(style.Fg).Background(style.BgRaised).Render(m.text))
}
