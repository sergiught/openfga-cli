// Package shell renders the Crush-style playground frame: a left sidebar
// (gradient logo + context + nav + status footer), a main content pane, and a
// bottom status bar. It also provides a centered overlay compositor for modal
// dialogs. Styling is driven by the active theme via the style package.
package shell

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/logo"
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

// sidebarOccupied returns the column count the sidebar takes up. The sidebar is
// a filled panel with no border; Width(w) already includes its padding in
// lipgloss v1. Zero when collapsed.
func (s *Shell) sidebarOccupied() int {
	if s.Collapsed() {
		return 0
	}
	return s.sidebarWidth()
}

func (s *Shell) bodyHeight() int {
	h := s.height - statusHeight
	if h < 1 {
		h = 1
	}
	return h
}

// MainSize returns the drawable interior for main-pane content. The main pane is
// a rounded-border panel: the border consumes 2 cols + 2 rows, padding 2 cols,
// and the title + blank header 2 rows.
func (s *Shell) MainSize() (int, int) {
	w := s.width - s.sidebarOccupied() - 4 // border(2) + padding(2)
	if w < 1 {
		w = 1
	}
	h := s.bodyHeight() - 4 // border(2) + title+blank(2)
	if h < 1 {
		h = 1
	}
	return w, h
}

// SetSidebar sets the sidebar content. The logo is rendered by the shell itself
// (sized to the sidebar width).
func (s *Shell) SetSidebar(context []string, nav []NavItem, footer string) {
	s.context, s.nav, s.footer = context, nav, footer
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
	frame := lipgloss.JoinVertical(lipgloss.Left, top, s.renderStatus())
	// Safety net: never emit more than height rows or wider than width. Any
	// residual overflow would scroll the terminal and corrupt the layout.
	return clampFrame(frame, s.width, s.height)
}

// fitLines truncates every line of s to at most w display columns (ANSI-aware)
// so lipgloss never wraps over-wide content into extra rows.
func fitLines(s string, w int) string {
	if w < 1 {
		w = 1
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = ansi.Truncate(ln, w, "…")
	}
	return strings.Join(lines, "\n")
}

// clampFrame forces s to exactly h lines, each no wider than w columns.
func clampFrame(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if lipgloss.Width(ln) > w {
			lines[i] = ansi.Truncate(ln, w, "")
		}
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (s *Shell) renderSidebar(height int) string {
	w := s.sidebarWidth()
	inner := w - 2
	var b strings.Builder
	// Logo: the big block wordmark when the sidebar is wide enough, otherwise a
	// compact wordmark with a diagonal field tail (Crush's small-logo treatment).
	word := logo.Word("ofga")
	if inner >= lipgloss.Width(word) {
		b.WriteString(style.GradientBlock(word) + "\n")
		b.WriteString(lipgloss.NewStyle().Foreground(style.Faintc).Render(strings.Repeat("╱", inner)) + "\n")
	} else {
		line := style.Gradient("ofga")
		if rem := inner - lipgloss.Width(line) - 1; rem > 0 {
			line += " " + lipgloss.NewStyle().Foreground(style.Faintc).Render(strings.Repeat("╱", rem))
		}
		b.WriteString(line + "\n")
	}
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
	// Truncate each line to the interior width (Width(w) includes the 2 padding
	// cols in lipgloss v1) so long store names/IDs never wrap and push rows down.
	content = fitLines(content, w-2)

	return lipgloss.NewStyle().
		Width(w).Height(height).
		Background(style.BgPanel).
		Padding(0, 1).
		Render(content)
}

func (s *Shell) renderNav(n NavItem) string {
	label := n.Label
	if n.Badge != "" {
		label += "  " + n.Badge // plain badge: a nested style here would reset the bg
	}
	// Every item carries an explicit background so the padding cells never fall
	// back to the terminal default (which showed as a stray mark beside labels).
	st := lipgloss.NewStyle().Padding(0, 1).Background(style.BgPanel).Foreground(style.Muted)
	if n.Active {
		st = lipgloss.NewStyle().Padding(0, 1).Bold(true).Background(style.Primary).Foreground(style.OnAccent)
	}
	return st.Render(label)
}

func (s *Shell) renderMain(height int) string {
	// The main pane is a rounded-border panel filling the area beside the sidebar.
	mainTotal := s.width - s.sidebarOccupied()
	if mainTotal < 6 {
		mainTotal = 6
	}
	innerW := mainTotal - 4 // border(2) + padding(2)
	title := lipgloss.NewStyle().Bold(true).Foreground(style.Primary).Render(s.mainTitle)
	// Truncate each body line to the interior width so over-wide content (graphs,
	// long rows) is clipped rather than wrapped into extra rows.
	content := title + "\n\n" + fitLines(s.mainBody, innerW)
	return lipgloss.NewStyle().
		Width(mainTotal-2).Height(height-2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Subtle).
		BorderBackground(style.BgBase).
		Background(style.BgBase).
		Padding(0, 1).
		Render(content)
}

func (s *Shell) renderStatus() string {
	right := lipgloss.NewStyle().Foreground(style.Faintc).Render(s.statusRight)
	rw := lipgloss.Width(right)
	// Truncate the (possibly long) status text so the bar fits one line and never
	// wraps; keep the right-side key hints visible.
	maxLeft := s.width - rw - 1
	if maxLeft < 0 {
		maxLeft = 0
	}
	left := ansi.Truncate(lipgloss.NewStyle().Foreground(style.Muted).Render(s.statusLeft), maxLeft, "…")
	gap := s.width - lipgloss.Width(left) - rw
	if gap < 1 {
		gap = 1
	}
	bar := ansi.Truncate(left+strings.Repeat(" ", gap)+right, s.width, "")
	return lipgloss.NewStyle().
		Width(s.width).Background(style.BgPanel).
		Render(bar)
}
