// Package shell renders the Crush-style playground frame: a left sidebar
// (gradient logo + context + nav + status footer), a main content pane, and a
// bottom status bar. It also provides a centered overlay compositor for modal
// dialogs. Styling is driven by the active theme via the style package.
package shell

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sergiught/openfga-cli/internal/style"
)

const (
	collapseBelow = 76 // total width under which the sidebar hides
	sidebarMin    = 24
	sidebarMax    = 34
	statusHeight  = 1
)

// NavItem is one sidebar navigation row.
type NavItem struct {
	Label  string
	Badge  string
	Active bool
}

// Shell holds the current size and the content of each region.
type Shell struct {
	width, height int

	logo    string
	context []string
	nav     []NavItem
	footer  string

	mainTitle string
	mainBody  string

	statusLeft  string
	statusRight string
}

// New returns an empty shell.
func New() *Shell { return &Shell{} }

// SetSize records the available terminal size.
func (s *Shell) SetSize(w, h int) { s.width, s.height = w, h }

// Collapsed reports whether the sidebar is hidden at the current width.
func (s *Shell) Collapsed() bool { return s.width < collapseBelow }

// sidebarWidth returns the CONTENT width passed to lipgloss Width().
// The sidebar's total column occupation = sidebarWidth() + 2 (padding) + 1 (border).
func (s *Shell) sidebarWidth() int {
	if s.Collapsed() {
		return 0
	}
	w := s.width / 4
	if w < sidebarMin {
		w = sidebarMin
	}
	if w > sidebarMax {
		w = sidebarMax
	}
	return w
}

// sidebarOccupied returns the actual column count the sidebar takes up
// (content + padding + border). Zero when collapsed.
func (s *Shell) sidebarOccupied() int {
	if s.Collapsed() {
		return 0
	}
	// Width(w) already includes padding in lipgloss v1; +1 for the right border.
	return s.sidebarWidth() + 1
}

func (s *Shell) bodyHeight() int {
	h := s.height - statusHeight
	if h < 1 {
		h = 1
	}
	return h
}

// MainSize returns the drawable interior width/height for main-pane content (inside the pane's padding).
func (s *Shell) MainSize() (int, int) {
	const header = 2 // title + blank line
	w := s.width - s.sidebarOccupied() - 2
	if w < 1 {
		w = 1
	}
	h := s.bodyHeight() - header
	if h < 1 {
		h = 1
	}
	return w, h
}

// SetSidebar sets the sidebar content (logo is pre-rendered, e.g. via style.Gradient).
func (s *Shell) SetSidebar(logo string, context []string, nav []NavItem, footer string) {
	s.logo, s.context, s.nav, s.footer = logo, context, nav, footer
}

// SetMain sets the main pane title and body.
func (s *Shell) SetMain(title, body string) { s.mainTitle, s.mainBody = title, body }

// SetStatus sets the bottom status bar's left/right text.
func (s *Shell) SetStatus(left, right string) { s.statusLeft, s.statusRight = left, right }

// View composes the full frame.
func (s *Shell) View() string {
	body := s.bodyHeight()
	main := s.renderMain(body)

	var top string
	if s.Collapsed() {
		top = main
	} else {
		top = lipgloss.JoinHorizontal(lipgloss.Top, s.renderSidebar(body), main)
	}
	return lipgloss.JoinVertical(lipgloss.Left, top, s.renderStatus())
}

func (s *Shell) renderSidebar(height int) string {
	w := s.sidebarWidth()
	var b strings.Builder
	b.WriteString(s.logo + "\n")
	b.WriteString(style.Faint.Render("OpenFGA playground") + "\n\n")
	for _, line := range s.context {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	for _, n := range s.nav {
		b.WriteString(s.renderNav(n) + "\n")
	}
	content := b.String()
	footer := s.footer
	gap := height - lipgloss.Height(content) - 1
	if gap > 0 {
		content += strings.Repeat("\n", gap)
	}
	content += footer

	return lipgloss.NewStyle().
		Width(w).Height(height).
		Background(style.BgPanel).
		BorderStyle(lipgloss.NormalBorder()).BorderRight(true).
		BorderForeground(style.Subtle).
		Padding(0, 1).
		Render(content)
}

func (s *Shell) renderNav(n NavItem) string {
	label := n.Label
	if n.Badge != "" {
		label += "  " + style.Faint.Render(n.Badge)
	}
	if n.Active {
		return lipgloss.NewStyle().Bold(true).
			Foreground(style.OnAccent).Background(style.Primary).
			Padding(0, 1).Render(label)
	}
	return lipgloss.NewStyle().Foreground(style.Muted).Padding(0, 1).Render(label)
}

func (s *Shell) renderMain(height int) string {
	// sidebarOccupied + mainContentWidth = total. Width() includes padding in lipgloss v1.
	mainContentWidth := s.width - s.sidebarOccupied()
	if mainContentWidth < 1 {
		mainContentWidth = 1
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(style.Primary).Render(s.mainTitle)
	content := title + "\n\n" + s.mainBody
	return lipgloss.NewStyle().
		Width(mainContentWidth).Height(height).
		Padding(0, 1).
		Render(content)
}

func (s *Shell) renderStatus() string {
	left := lipgloss.NewStyle().Foreground(style.Muted).Render(s.statusLeft)
	right := lipgloss.NewStyle().Foreground(style.Faintc).Render(s.statusRight)
	gap := s.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + right
	return lipgloss.NewStyle().
		Width(s.width).Background(style.BgPanel).
		Render(bar)
}
