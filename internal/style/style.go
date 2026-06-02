// Package style centralizes the lipgloss palette and reusable styles. All
// values are derived from the active theme.Theme and can be swapped at runtime
// via Apply — this is what powers live theme switching in the TUI. Existing
// callers reference the package-level vars directly; Apply reassigns them.
package style

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/sergiught/openfga-cli/internal/theme"
)

// Icons used across the CLI and TUI (Crush-inspired).
const (
	IconCheck   = "✓"
	IconCross   = "✗"
	IconBullet  = "•"
	IconArrow   = "→"
	IconCaret   = "❯"
	IconDot     = "●"
	IconCircle  = "○"
	IconStore   = "▣"
	IconModel   = "◈"
	IconTuple   = "≡"
	IconChange  = "⇅"
	IconQuery   = "?"
	IconAssert  = "✦"
	IconGear    = "✱"
	IconSpark   = "✦"
	EdgeDirect  = "←"
	EdgeComputed = "="
	EdgeTTU     = "⇡"
)

// Active is the currently applied theme.
var Active = theme.Default()

// Colors (reassigned by Apply).
var (
	Primary   lipgloss.TerminalColor
	Secondary lipgloss.TerminalColor
	Accent    lipgloss.TerminalColor
	Keyword   lipgloss.TerminalColor
	Fg        lipgloss.TerminalColor
	Muted     lipgloss.TerminalColor
	Faintc    lipgloss.TerminalColor
	BgBase    lipgloss.TerminalColor
	BgPanel   lipgloss.TerminalColor
	Subtle    lipgloss.TerminalColor
	Green     lipgloss.TerminalColor
	Amber     lipgloss.TerminalColor
	Red       lipgloss.TerminalColor
	Info      lipgloss.TerminalColor
	OnAccent  lipgloss.TerminalColor

	// Back-compat aliases kept for existing call sites.
	Violet lipgloss.TerminalColor // == Primary
	Indigo lipgloss.TerminalColor // == Secondary
	Pink   lipgloss.TerminalColor // == Keyword
	Cyan   lipgloss.TerminalColor // == Accent
)

// Styles (reassigned by Apply).
var (
	Title    lipgloss.Style
	Heading  lipgloss.Style
	Subtitle lipgloss.Style
	Key      lipgloss.Style
	Value    lipgloss.Style
	Faint    lipgloss.Style
	Bold     lipgloss.Style
	Success  lipgloss.Style
	Failure  lipgloss.Style
	Warn     lipgloss.Style

	AllowedBadge lipgloss.Style
	DeniedBadge  lipgloss.Style
	TableHeader  lipgloss.Style
	Panel        lipgloss.Style
	ActivePanel  lipgloss.Style
)

func init() { Apply(theme.Default()) }

// Apply rebuilds every color and style from the given theme.
func Apply(t theme.Theme) {
	Active = t

	Primary, Secondary, Accent, Keyword = t.Primary, t.Secondary, t.Accent, t.Keyword
	Fg, Muted, Faintc = t.FgBase, t.FgSubtle, t.FgFaint
	BgBase, BgPanel, Subtle = t.BgBase, t.BgRaised, t.Separator
	Green, Amber, Red, Info = t.Success, t.Warning, t.Error, t.Info
	OnAccent = t.OnAccent

	Violet, Indigo, Pink, Cyan = Primary, Secondary, Keyword, Accent

	Title = lipgloss.NewStyle().Bold(true).Foreground(Primary)
	Heading = lipgloss.NewStyle().Bold(true).Foreground(Secondary)
	Subtitle = lipgloss.NewStyle().Foreground(Muted)
	Key = lipgloss.NewStyle().Foreground(Accent)
	Value = lipgloss.NewStyle().Foreground(Fg)
	Faint = lipgloss.NewStyle().Foreground(Faintc)
	Bold = lipgloss.NewStyle().Bold(true).Foreground(Fg)
	Success = lipgloss.NewStyle().Bold(true).Foreground(Green)
	Failure = lipgloss.NewStyle().Bold(true).Foreground(Red)
	Warn = lipgloss.NewStyle().Foreground(Amber)

	AllowedBadge = lipgloss.NewStyle().Bold(true).Foreground(OnAccent).Background(Green).Padding(0, 1)
	DeniedBadge = lipgloss.NewStyle().Bold(true).Foreground(OnAccent).Background(Red).Padding(0, 1)
	TableHeader = lipgloss.NewStyle().Bold(true).Foreground(Primary)
	Panel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(Subtle).Padding(0, 1)
	ActivePanel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(Primary).Padding(0, 1)
}

// SetTheme applies a theme by name; returns false if unknown.
func SetTheme(name string) bool {
	t, ok := theme.Get(name)
	if !ok {
		return false
	}
	Apply(t)
	return true
}

// Allowed returns a styled ALLOWED/DENIED badge for a boolean outcome.
func Allowed(ok bool) string {
	if ok {
		return AllowedBadge.Render(IconCheck + " ALLOWED")
	}
	return DeniedBadge.Render(IconCross + " DENIED")
}

// Bullet returns a primary-colored bullet prefix.
func Bullet() string { return lipgloss.NewStyle().Foreground(Primary).Render(IconBullet) }
