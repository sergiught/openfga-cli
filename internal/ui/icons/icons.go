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
	// ModeAuto is a resolution-time value only: Apply turns it into a concrete
	// rung via Detect. It is never a key into sets and never reaches Current.
	ModeAuto
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
		Profile: "◉", Store: "▣", Model: "◈", Tuple: "≡", Change: "⇅", Query: "◆", Assert: "✦", APILog: "⇄",
		Dot: "●", Caret: "❯", Check: "✓", Cross: "✗",
	},
	ModeOff: {Check: "✓", Cross: "✗", Dot: "●"},
}

var (
	current     = sets[ModeNerdFont]
	currentMode = ModeNerdFont
)

// Parse maps a config string to a Mode. The accepted values are "auto"
// (default), "nerdfont", "unicode" and "off"; an empty string means the
// default, and any other value warns on stderr before falling back to auto.
func Parse(s string) Mode {
	switch s {
	case "", "auto":
		return ModeAuto
	case "nerdfont":
		return ModeNerdFont
	case "unicode":
		return ModeUnicode
	case "off":
		return ModeOff
	default:
		// Source-neutral: the value may come from OPENFGA_ICONS or the config
		// file's `icons` key, so don't blame the env var specifically.
		fmt.Fprintf(os.Stderr, "warning: unknown icons value %q; using auto (valid: auto, nerdfont, unicode, off)\n", s)
		return ModeAuto
	}
}

// Apply activates a mode for the whole process, resolving ModeAuto through
// Detect first so sets is only ever indexed by a concrete rung.
func Apply(m Mode) {
	if m == ModeAuto {
		m = Detect()
	}
	currentMode = m
	current = sets[m]
}

// Current returns the active rung after any auto resolution. The playground's
// glyph-cycle key needs it to know where the cycle starts.
func Current() Mode { return currentMode }

// I returns the active glyph set.
func I() Set { return current }
