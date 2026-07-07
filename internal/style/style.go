// Package style centralizes the lipgloss palette and reusable styles. All
// values are derived from the active theme.Theme and can be swapped at runtime
// via Apply — this is what powers live theme switching in the TUI. Existing
// callers reference the package-level vars directly; Apply reassigns them.
package style

import (
	"image/color"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	colorful "github.com/lucasb-eyer/go-colorful"

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
	Primary     color.Color
	Secondary   color.Color
	Accent      color.Color
	Keyword     color.Color
	Fg          color.Color
	Muted       color.Color
	Faintc      color.Color
	BgBase      color.Color
	BgPanel     color.Color // sidebar column
	BgRaised    color.Color // main pane, cards (old BgPanel call sites move here)
	BgHighlight color.Color // chips, badges, keycaps
	BgOverlay   color.Color // dialog scrim/shadow
	Subtle      color.Color
	Green       color.Color
	Amber       color.Color
	Red         color.Color
	Info        color.Color
	OnAccent    color.Color

	// Back-compat aliases kept for existing call sites.
	Violet color.Color // == Primary
	Indigo color.Color // == Secondary
	Pink   color.Color // == Keyword
	Cyan   color.Color // == Accent
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
	BgBase, BgRaised, Subtle = t.BgBase, t.BgRaised, t.Separator
	BgPanel, BgHighlight, BgOverlay = t.BgPanel, t.BgHighlight, t.BgOverlay
	if BgPanel == nil {
		BgPanel = t.BgBase
	}
	if BgHighlight == nil {
		BgHighlight = t.BgRaised
	}
	if BgOverlay == nil {
		BgOverlay = t.BgBase
	}
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

// DotState selects the color of a status dot.
type DotState int

const (
	DotOnline DotState = iota // mint
	DotBusy                   // amber
	DotError                  // coral
	DotOffline                // faint
)

// Dot returns a colored ● for the given state.
func Dot(state DotState) string {
	c := Faintc
	switch state {
	case DotOnline:
		c = Green
	case DotBusy:
		c = Amber
	case DotError:
		c = Red
	}
	return lipgloss.NewStyle().Foreground(c).Render(IconDot)
}

// Gradient renders s with a per-rune color blend between the active theme's
// GradStartHex and GradEndHex (Lab space). Under the mono theme it returns the
// text unstyled; when the theme defines no gradient it falls back to a solid
// bold Primary.
func Gradient(s string) string {
	if Active.Name == "mono" {
		return s
	}
	if Active.GradStartHex == "" || Active.GradEndHex == "" {
		return lipgloss.NewStyle().Bold(true).Foreground(Primary).Render(s)
	}
	c1, err1 := colorful.Hex(Active.GradStartHex)
	c2, err2 := colorful.Hex(Active.GradEndHex)
	if err1 != nil || err2 != nil {
		return lipgloss.NewStyle().Bold(true).Foreground(Primary).Render(s)
	}
	runes := []rune(s)
	n := len(runes)
	var b strings.Builder
	for i, r := range runes {
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1)
		}
		c := c1.BlendLab(c2, t).Clamped()
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c.Hex())).Render(string(r)))
	}
	return b.String()
}

// GradientBlock applies the brand gradient diagonally across multi-line block
// art (e.g. a wordmark): color advances with column + row so it flows from the
// top-left start color to the bottom-right end color. Mono/no-gradient themes
// fall back to solid bold Primary.
func GradientBlock(s string) string {
	if Active.Name == "mono" || Active.GradStartHex == "" || Active.GradEndHex == "" {
		return lipgloss.NewStyle().Bold(true).Foreground(Primary).Render(s)
	}
	c1, err1 := colorful.Hex(Active.GradStartHex)
	c2, err2 := colorful.Hex(Active.GradEndHex)
	if err1 != nil || err2 != nil {
		return lipgloss.NewStyle().Bold(true).Foreground(Primary).Render(s)
	}
	lines := strings.Split(s, "\n")
	maxW := 0
	for _, ln := range lines {
		if w := len([]rune(ln)); w > maxW {
			maxW = w
		}
	}
	denom := float64(maxW + len(lines) - 2)
	if denom < 1 {
		denom = 1
	}
	var b strings.Builder
	for r, ln := range lines {
		for i, ch := range ln {
			t := float64(i+r) / denom
			if t > 1 {
				t = 1
			}
			c := c1.BlendLab(c2, t).Clamped()
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c.Hex())).Render(string(ch)))
		}
		if r < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
