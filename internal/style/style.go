// Package style centralizes the lipgloss palette and reusable styles. All
// values are derived from the active theme.Theme and can be swapped at runtime
// via Apply — this is what powers live theme switching in the TUI. Existing
// callers reference the package-level vars directly; Apply reassigns them.
package style

import (
	"image/color"
	"math"
	"strings"
	"unicode/utf8"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	colorful "github.com/lucasb-eyer/go-colorful"

	"github.com/sergiught/openfga-cli/internal/theme"
)

// Icons used across the CLI and TUI (Crush-inspired).
const (
	IconCheck    = "✓"
	IconCross    = "✗"
	IconBullet   = "•"
	IconArrow    = "→"
	IconCaret    = "❯"
	IconDot      = "●"
	IconCircle   = "○"
	IconStore    = "▣"
	IconModel    = "◈"
	IconTuple    = "≡"
	IconChange   = "⇅"
	IconQuery    = "?"
	IconAssert   = "✦"
	IconGear     = "✱"
	IconSpark    = "✦"
	EdgeDirect   = "←"
	EdgeComputed = "="
	EdgeTTU      = "⇡"
)

// Active is the currently applied theme.
var Active = theme.Default()

// Colors (reassigned by Apply).
var (
	Primary     color.Color
	Secondary   color.Color
	Accent      color.Color
	Keyword     color.Color
	Violet      color.Color // second accent: mode chips, dialog borders/titles
	Magenta     color.Color // second accent: selection + palette highlights
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

	Violet, Magenta = t.Violet, t.Magenta
	if Violet == nil {
		Violet = t.Keyword
	}
	if Magenta == nil {
		Magenta = t.Secondary
	}

	Indigo, Pink, Cyan = Secondary, Keyword, Accent

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

// spoofingRune reports whether r can make displayed text disagree with the
// bytes behind it, independently of ANSI escapes.
//
// The first group is Unicode's explicit bidirectional formatting: the
// embeddings/overrides U+202A-U+202E and the isolates U+2066-U+2069. A store or
// tuple name carrying these renders in an order other than its byte order — the
// Trojan Source class (CVE-2021-42574) — so `document:txt.gnp` can be drawn for
// a name that actually reads `document:png.txt`.
//
// The second group is the zero-width characters with no shaping role: U+200B
// (zero-width space) and U+FEFF (zero-width no-break space), which can hide a
// difference between two otherwise identical-looking identifiers.
//
// Deliberately NOT included: U+200C/U+200D (ZWNJ/ZWJ) and U+200E/U+200F
// (LRM/RLM). Those carry real linguistic meaning — emoji ZWJ sequences and
// Indic/Persian shaping depend on them, and the marks only nudge direction
// rather than opening an override scope — so removing them would corrupt
// legitimate names to defend against a weaker attack than the overrides allow.
func spoofingRune(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) ||
		(r >= 0x2066 && r <= 0x2069) ||
		r == 0x200B || r == 0xFEFF
}

// SanitizeTerminalKeepSGR removes everything SanitizeTerminal does *except*
// SGR sequences (CSI … m), which only choose colors and weights.
//
// It exists for lines the CLI composes itself and deliberately styles — the
// success/info/warning printers are handed values already wrapped in
// style.Bold.Render — where stripping every escape would silently discard that
// styling. Sequences that can act rather than merely paint (OSC clipboard and
// title writes, cursor movement, screen clears), raw control characters and
// bidi overrides are still removed, so a hostile value interpolated into such a
// line cannot do anything beyond changing its own colour.
//
// Use SanitizeTerminal for text that arrives from the server or a file, where
// no styling is expected in the first place.
func SanitizeTerminalKeepSGR(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			r, size := utf8.DecodeRuneInString(s[i:])
			i += size
			if r == utf8.RuneError && size == 1 {
				b.WriteRune(utf8.RuneError)
				continue
			}
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || spoofingRune(r) {
				continue
			}
			b.WriteRune(r)
			continue
		}
		seq, final, n := scanEscape(s[i:])
		i += n
		// Keep only SGR: ESC [ <params> m. Every other escape is dropped.
		if final == 'm' && strings.HasPrefix(seq, "\x1b[") {
			b.WriteString(seq)
		}
	}
	return b.String()
}

// scanEscape reads one escape sequence starting at s[0] (which must be ESC) and
// returns the raw sequence, its final byte (0 if malformed or truncated) and
// how many bytes were consumed.
func scanEscape(s string) (seq string, final byte, n int) {
	if len(s) < 2 {
		return "", 0, len(s)
	}
	switch s[1] {
	case '[': // CSI: params 0x30-0x3F, intermediates 0x20-0x2F, final 0x40-0x7E
		for j := 2; j < len(s); j++ {
			c := s[j]
			if c >= 0x30 && c <= 0x3f || c >= 0x20 && c <= 0x2f {
				continue
			}
			if c >= 0x40 && c <= 0x7e {
				return s[:j+1], c, j + 1
			}
			return s[:j], 0, j // malformed; drop what we scanned
		}
		return s, 0, len(s)
	case ']': // OSC: runs to BEL or ST (ESC \)
		for j := 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return s[:j+1], 0, j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return s[:j+2], 0, j + 2
			}
		}
		return s, 0, len(s)
	default:
		// Two-byte escape (or a lone ESC at end of input).
		return s[:2], 0, 2
	}
}

// SanitizeTerminal removes escape sequences, control characters and
// text-direction spoofing characters from untrusted text before it is styled
// for an interactive terminal.
func SanitizeTerminal(s string) string {
	s = ansi.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		if spoofingRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// DotState selects the color of a status dot.
type DotState int

const (
	DotOnline  DotState = iota // mint
	DotBusy                    // amber
	DotError                   // coral
	DotOffline                 // faint
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

// FieldHighlight is the background that marks the focused form field's row:
// the base surface lifted toward the primary accent so the active field reads
// as "here" without falling back on a neutral gray.
func FieldHighlight() color.Color {
	return Blend(BgBase, Primary, 0.2)
}

// Blend returns the color k of the way (in Lab space) from a to b. k=0 is a,
// k=1 is b. Falls back to a when either color can't be converted, or under
// the mono theme (no color blending).
func Blend(a, b color.Color, k float64) color.Color {
	if Active.Name == "mono" {
		return a
	}
	ca, ok1 := colorful.MakeColor(a)
	cb, ok2 := colorful.MakeColor(b)
	if !ok1 || !ok2 {
		return a
	}
	return lipgloss.Color(ca.BlendLab(cb, k).Clamped().Hex())
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
	return GradientBlockPhase(s, 0)
}

// phaseOffset converts a looping drift phase into a smooth ping-pong ramp
// offset, so the gradient breathes back and forth with no wrap seam.
func phaseOffset(phase float64) float64 { return 0.25 * math.Sin(2*math.Pi*phase) }

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// GradientBlockPhase renders block art like GradientBlock with the ramp
// position shifted by a drift phase. Phase 0 is byte-identical to
// GradientBlock. Mono/no-gradient themes fall back identically.
func GradientBlockPhase(s string, phase float64) string {
	if Active.Name == "mono" || Active.GradStartHex == "" || Active.GradEndHex == "" {
		return lipgloss.NewStyle().Bold(true).Foreground(Primary).Render(s)
	}
	c1, err1 := colorful.Hex(Active.GradStartHex)
	c2, err2 := colorful.Hex(Active.GradEndHex)
	if err1 != nil || err2 != nil {
		return lipgloss.NewStyle().Bold(true).Foreground(Primary).Render(s)
	}
	off := phaseOffset(phase)
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
			t := clamp01(float64(i+r)/denom + off)
			c := c1.BlendLab(c2, t).Clamped()
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c.Hex())).Render(string(ch)))
		}
		if r < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// GradientPillPhase is GradientPill with a drift phase; phase 0 matches
// GradientPill exactly.
func GradientPillPhase(text string, phase float64) string {
	if Active.Name == "mono" || Active.GradStartHex == "" || Active.GradEndHex == "" {
		return Chip(text, OnAccent, Primary)
	}
	c1, err1 := colorful.Hex(Active.GradStartHex)
	c2, err2 := colorful.Hex(Active.GradEndHex)
	if err1 != nil || err2 != nil {
		return Chip(text, OnAccent, Primary)
	}
	off := phaseOffset(phase)
	padded := " " + text + " "
	runes := []rune(padded)
	n := len(runes)
	var b strings.Builder
	for i, r := range runes {
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1)
		}
		t = clamp01(t + off)
		c := c1.BlendLab(c2, t).Clamped()
		b.WriteString(lipgloss.NewStyle().Bold(true).
			Foreground(OnAccent).Background(lipgloss.Color(c.Hex())).
			Render(string(r)))
	}
	return b.String()
}

// Chip renders a small filled label: bold fg on bg with 1-col padding.
func Chip(text string, fg, bg color.Color) string {
	return lipgloss.NewStyle().Bold(true).Foreground(fg).Background(bg).Padding(0, 1).Render(text)
}

// Keycap renders a dim key hint pill (e.g. "q", "↵") on the raised surface.
func Keycap(k string) string {
	return lipgloss.NewStyle().Foreground(Muted).Background(BgRaised).Padding(0, 1).Render(k)
}

// GradientPill renders text on a per-rune brand-gradient background with
// OnAccent foreground — the active-nav treatment. Mono themes fall back to a
// plain Primary chip.
func GradientPill(text string) string {
	return GradientPillPhase(text, 0)
}

// SectionHeader renders a crush-style section header: a mid-tone bold title
// followed by a faint hairline rule filling the remaining width. This is the
// flat UI's structural primitive — panels are delimited by headers and
// whitespace, not borders.
func SectionHeader(title string, width int) string {
	return SectionHeaderTinted(title, width, Faintc)
}

// SectionHeaderTinted is SectionHeader with an explicit rule color, used for
// the one-frame verdict flash on the query Result header.
func SectionHeaderTinted(title string, width int, tint color.Color) string {
	return sectionHeader(title, width, Muted, tint)
}

// SectionHeaderFocused renders the header with both the title and rule in the
// Primary accent, marking the main panel as the focused region.
func SectionHeaderFocused(title string, width int) string {
	return sectionHeader(title, width, Primary, Primary)
}

// sectionHeader is the shared body: a bold titleColor title followed by a
// ruleColor hairline rule filling the remaining width.
func sectionHeader(title string, width int, titleColor, ruleColor color.Color) string {
	t := lipgloss.NewStyle().Bold(true).Foreground(titleColor).Render(title)
	rem := width - lipgloss.Width(t) - 1
	if rem < 1 {
		t = ansi.Truncate(t, width, "…")
		if pad := width - lipgloss.Width(t); pad > 0 {
			t += strings.Repeat(" ", pad)
		}
		return t
	}
	return t + " " + lipgloss.NewStyle().Foreground(ruleColor).Render(strings.Repeat("─", rem))
}
