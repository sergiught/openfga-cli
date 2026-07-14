// Package icons resolves the glyph set once per run: Nerd Font when available
// (default), universal Unicode fallback, or off for decorative glyphs.
package icons

import (
	"fmt"
	"os"
)

// Mode selects a glyph capability rung.
type Mode int

const (
	ModeNerdFont Mode = iota
	ModeUnicode
	ModeOff
)

// Set holds every glyph the UI uses.
type Set struct {
	Profile, Store, Model, Tuple, Change, Query, Assert, APILog string
	Dot, Caret, Check, Cross                                    string
	CapL, CapR                                                  string // powerline chip caps
}

var sets = map[Mode]Set{
	ModeNerdFont: {
		Profile: "\U0000F007", Store: "\U0000F1C0", Model: "\U0000E725", Tuple: "\U0000F0C1",
		Change: "\U0000F021", Query: "\U0000F002", Assert: "\U0000F058", APILog: "\U0000F022",
		Dot: "●", Caret: "❯", Check: "✓", Cross: "✗",
		CapL: "\U0000E0B6", CapR: "\U0000E0B4",
	},
	ModeUnicode: {
		Profile: "◉", Store: "▣", Model: "◈", Tuple: "≡", Change: "⇅", Query: "?", Assert: "✦", APILog: "⇄",
		Dot: "●", Caret: "❯", Check: "✓", Cross: "✗",
	},
	ModeOff: {Check: "✓", Cross: "✗", Dot: "●"},
}

var current = sets[ModeNerdFont]

// Parse maps a config string to a Mode. The accepted values are "nerdfont"
// (default), "unicode" and "off"; an empty string means the default, and any
// other value warns on stderr before falling back to nerdfont.
func Parse(s string) Mode {
	switch s {
	case "", "nerdfont":
		return ModeNerdFont
	case "unicode":
		return ModeUnicode
	case "off":
		return ModeOff
	default:
		// Source-neutral: the value may come from OPENFGA_ICONS or the config
		// file's `icons` key, so don't blame the env var specifically.
		fmt.Fprintf(os.Stderr, "warning: unknown icons value %q; using nerdfont (valid: nerdfont, unicode, off)\n", s)
		return ModeNerdFont
	}
}

// Apply activates a mode for the whole process.
func Apply(m Mode) { current = sets[m] }

// I returns the active glyph set.
func I() Set { return current }
