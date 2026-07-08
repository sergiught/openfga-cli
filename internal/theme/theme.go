// Package theme defines named color palettes for the CLI and TUI. Each theme
// assigns semantic roles (primary, accent, success, …) to concrete colors,
// inspired by charmbracelet/crush's Styles approach. The active theme drives
// every style in the style package.
package theme

import (
	"image/color"
	"sort"

	lipgloss "charm.land/lipgloss/v2"
)

// Theme is a semantic palette. Colors are concrete (dark-terminal tuned) so the
// look is consistent and vivid, matching the Crush aesthetic.
type Theme struct {
	Name string

	// Identity
	Primary   color.Color // headline accent (logo, active tab, cursor)
	Secondary color.Color // supporting accent
	Accent    color.Color // links, computed edges
	Keyword   color.Color // emphasis (modes, keywords)
	Violet    color.Color // second accent: mode chips, dialog borders/titles
	Magenta   color.Color // second accent: selection + palette highlights

	// Foreground tiers
	FgBase   color.Color
	FgSubtle color.Color
	FgFaint  color.Color

	// Background tiers
	BgBase      color.Color
	BgPanel     color.Color // sidebar column — darker than base
	BgRaised    color.Color // main pane, cards
	BgHighlight color.Color // chips, badges, pills, keycaps
	BgOverlay   color.Color // scrim/shadow behind dialogs
	Separator   color.Color

	// Status
	Success color.Color
	Warning color.Color
	Error   color.Color
	Info    color.Color

	// OnAccent is the text color placed on top of Primary backgrounds.
	OnAccent color.Color

	// GradStartHex/GradEndHex define the wordmark gradient endpoints as hex
	// strings (blended per-rune by style.Gradient). Empty = solid Primary.
	GradStartHex string
	GradEndHex   string
}

func col(hex string) color.Color { return lipgloss.Color(hex) }

var registry = map[string]Theme{
	// aurora is the signature ofga theme: a cool slate built around OpenFGA's
	// brand gradient (electric cyan → mint green, from the logo SVG), with warm
	// complements for status. It is the default.
	"aurora": {
		Name:         "aurora",
		Primary:      col("#00FAFF"), // cyan — focus, active nav, cursor, titles
		Secondary:    col("#8BFF95"), // mint — selection accent
		Accent:       col("#2EE6C6"), // aqua — keys, links, computed edges
		Keyword:      col("#56B6FF"), // sky — emphasis (modes, "or")
		Violet:       col("#9D7CFF"),
		Magenta:      col("#FF6AC1"),
		FgBase:       col("#E4EAEF"),
		FgSubtle:     col("#8893A0"),
		FgFaint:      col("#4C5663"),
		BgBase:       col("#0E1116"),
		BgPanel:      col("#06080C"),
		BgRaised:     col("#1E2633"),
		BgHighlight:  col("#2B3547"),
		BgOverlay:    col("#070A0E"),
		Separator:    col("#39455A"),
		Success:      col("#8BFF95"),
		Warning:      col("#FFC24B"),
		Error:        col("#FF5C7A"),
		Info:         col("#56B6FF"),
		OnAccent:     col("#08130F"),
		GradStartHex: "#00FAFF",
		GradEndHex:   "#8BFF95",
	},
	// taskpilot mirrors the task-pilot-cli palette: adaptive indigo active
	// borders, green selection, gray passive borders, magenta accents, and a
	// high-contrast adaptive mono foreground. It is the default.
	"taskpilot": {
		Name:      "taskpilot",
		Primary:   col("#7571F9"), // indigo
		Secondary: col("#02BF87"), // green
		Accent:    col("#7571F9"), // indigo
		Keyword:   col("#EE6FF8"), // magenta
		FgBase:    col("#DDDDDD"),
		FgSubtle:  col("#777777"),
		FgFaint:   col("#4D4D4D"),
		BgBase:    lipgloss.NoColor{},
		BgRaised:  col("#2A2A2A"),
		Separator: col("#777777"),
		Success:   col("#02BF87"),
		Warning:   col("#FFB454"),
		Error:     col("#FE5A62"),
		Info:      col("#7571F9"),
		OnAccent:  col("#FFFFFF"),
	},
	"charm": {
		Name:      "charm",
		Primary:   col("#8B5CF6"),
		Secondary: col("#2DD4BF"),
		Accent:    col("#22D3EE"),
		Keyword:   col("#FF6AC1"),
		FgBase:    col("#E5E7EB"),
		FgSubtle:  col("#9CA3AF"),
		FgFaint:   col("#6B7280"),
		BgBase:    col("#16161E"),
		BgRaised:  col("#26263A"),
		Separator: col("#3A3A4E"),
		Success:   col("#4ADE80"),
		Warning:   col("#FBBF24"),
		Error:     col("#F87171"),
		Info:      col("#60A5FA"),
		OnAccent:  col("#0B0B12"),
	},
	"catppuccin": {
		Name:      "catppuccin",
		Primary:   col("#CBA6F7"),
		Secondary: col("#A6E3A1"),
		Accent:    col("#89DCEB"),
		Keyword:   col("#F5C2E7"),
		FgBase:    col("#CDD6F4"),
		FgSubtle:  col("#A6ADC8"),
		FgFaint:   col("#6C7086"),
		BgBase:    col("#1E1E2E"),
		BgRaised:  col("#313244"),
		Separator: col("#45475A"),
		Success:   col("#A6E3A1"),
		Warning:   col("#F9E2AF"),
		Error:     col("#F38BA8"),
		Info:      col("#89B4FA"),
		OnAccent:  col("#1E1E2E"),
	},
	"dracula": {
		Name:      "dracula",
		Primary:   col("#BD93F9"),
		Secondary: col("#50FA7B"),
		Accent:    col("#8BE9FD"),
		Keyword:   col("#FF79C6"),
		FgBase:    col("#F8F8F2"),
		FgSubtle:  col("#BFBFD0"),
		FgFaint:   col("#6272A4"),
		BgBase:    col("#282A36"),
		BgRaised:  col("#343746"),
		Separator: col("#44475A"),
		Success:   col("#50FA7B"),
		Warning:   col("#F1FA8C"),
		Error:     col("#FF5555"),
		Info:      col("#8BE9FD"),
		OnAccent:  col("#282A36"),
	},
	"nord": {
		Name:      "nord",
		Primary:   col("#88C0D0"),
		Secondary: col("#A3BE8C"),
		Accent:    col("#8FBCBB"),
		Keyword:   col("#B48EAD"),
		FgBase:    col("#ECEFF4"),
		FgSubtle:  col("#D8DEE9"),
		FgFaint:   col("#616E88"),
		BgBase:    col("#2E3440"),
		BgRaised:  col("#3B4252"),
		Separator: col("#434C5E"),
		Success:   col("#A3BE8C"),
		Warning:   col("#EBCB8B"),
		Error:     col("#BF616A"),
		Info:      col("#81A1C1"),
		OnAccent:  col("#2E3440"),
	},
	"tokyonight": {
		Name:      "tokyonight",
		Primary:   col("#7AA2F7"),
		Secondary: col("#9ECE6A"),
		Accent:    col("#2AC3DE"),
		Keyword:   col("#BB9AF7"),
		FgBase:    col("#C0CAF5"),
		FgSubtle:  col("#A9B1D6"),
		FgFaint:   col("#565F89"),
		BgBase:    col("#1A1B26"),
		BgRaised:  col("#24283B"),
		Separator: col("#414868"),
		Success:   col("#9ECE6A"),
		Warning:   col("#E0AF68"),
		Error:     col("#F7768E"),
		Info:      col("#7AA2F7"),
		OnAccent:  col("#1A1B26"),
	},
	"gruvbox": {
		Name:      "gruvbox",
		Primary:   col("#FE8019"),
		Secondary: col("#B8BB26"),
		Accent:    col("#83A598"),
		Keyword:   col("#D3869B"),
		FgBase:    col("#EBDBB2"),
		FgSubtle:  col("#BDAE93"),
		FgFaint:   col("#928374"),
		BgBase:    col("#282828"),
		BgRaised:  col("#3C3836"),
		Separator: col("#504945"),
		Success:   col("#B8BB26"),
		Warning:   col("#FABD2F"),
		Error:     col("#FB4934"),
		Info:      col("#83A598"),
		OnAccent:  col("#282828"),
	},
}

// monoTheme uses the terminal's default colors only (NO_COLOR friendly).
func monoTheme() Theme {
	n := lipgloss.NoColor{}
	return Theme{
		Name: "mono", Primary: n, Secondary: n, Accent: n, Keyword: n, Violet: n, Magenta: n,
		FgBase: n, FgSubtle: n, FgFaint: n,
		BgBase: n, BgPanel: n, BgRaised: n, BgHighlight: n, BgOverlay: n, Separator: n,
		Success: n, Warning: n, Error: n, Info: n, OnAccent: n,
	}
}

// Names returns the available theme names, sorted, with "mono" last.
func Names() []string {
	names := make([]string, 0, len(registry)+1)
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return append(names, "mono")
}

// Get returns a theme by name and whether it was found.
func Get(name string) (Theme, bool) {
	if name == "mono" {
		return monoTheme(), true
	}
	t, ok := registry[name]
	return t, ok
}

// Default returns the default theme (Aurora — OpenFGA's signature look).
func Default() Theme { return registry["aurora"] }

// Mono returns the no-color theme.
func Mono() Theme { return monoTheme() }
