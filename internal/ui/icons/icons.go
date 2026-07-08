// Package icons resolves the glyph set once per run: Nerd Font when available
// (default), universal Unicode fallback, or off for decorative glyphs.
package icons

// Mode selects a glyph capability rung.
type Mode int

const (
	ModeNerdFont Mode = iota
	ModeUnicode
	ModeOff
)

// Set holds every glyph the UI uses.
type Set struct {
	Store, Model, Tuple, Change, Query, Assert string
	Dot, Caret, Check, Cross                   string
}

var sets = map[Mode]Set{
	ModeNerdFont: {
		Store: "\U000F01BC", Model: "", Tuple: "\U000F0337",
		Change: "\U000F02DA", Query: "", Assert: "\U000F0668",
		Dot: "●", Caret: "❯", Check: "✓", Cross: "✗",
	},
	ModeUnicode: {
		Store: "▣", Model: "◈", Tuple: "≡", Change: "⇅", Query: "?", Assert: "✦",
		Dot: "●", Caret: "❯", Check: "✓", Cross: "✗",
	},
	ModeOff: {Check: "✓", Cross: "✗", Dot: "●"},
}

var current = sets[ModeNerdFont]

// Parse maps a config string to a Mode; unknown values mean nerdfont.
func Parse(s string) Mode {
	switch s {
	case "unicode":
		return ModeUnicode
	case "off":
		return ModeOff
	default:
		return ModeNerdFont
	}
}

// Apply activates a mode for the whole process.
func Apply(m Mode) { current = sets[m] }

// I returns the active glyph set.
func I() Set { return current }
