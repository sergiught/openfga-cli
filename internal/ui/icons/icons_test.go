package icons

import "testing"

func TestModesSwapGlyphs(t *testing.T) {
	Apply(ModeNerdFont)
	nf := I()
	Apply(ModeUnicode)
	uni := I()
	if nf.Store == uni.Store {
		t.Fatal("nerdfont and unicode store glyphs should differ")
	}
	Apply(ModeOff)
	if I().Store != "" || I().Check == "" {
		t.Fatal("off mode drops decorative glyphs but keeps semantic check/cross")
	}
	if Parse("bogus") != ModeAuto {
		t.Fatal("unknown mode falls back to auto")
	}
}

func TestNerdFontGlyphsAreV2Safe(t *testing.T) {
	Apply(ModeNerdFont)
	s := I()
	for name, g := range map[string]string{
		"Store": s.Store, "Model": s.Model, "Tuple": s.Tuple,
		"Change": s.Change, "Query": s.Query, "Assert": s.Assert,
	} {
		r := []rune(g)[0]
		if r > 0xF2FF {
			t.Fatalf("%s glyph %U is outside the Nerd-Font-v2-safe range", name, r)
		}
	}
	want := map[string][2]string{
		"Store":  {s.Store, "\U0000F1C0"},
		"Model":  {s.Model, "\U0000E725"},
		"Tuple":  {s.Tuple, "\U0000F0C1"},
		"Change": {s.Change, "\U0000F021"},
		"Query":  {s.Query, "\U0000F002"},
		"Assert": {s.Assert, "\U0000F058"},
	}
	for name, pair := range want {
		if pair[0] != pair[1] {
			t.Fatalf("%s glyph = %q, want %q", name, pair[0], pair[1])
		}
	}
	if s.CapL != "\U0000E0B6" || s.CapR != "\U0000E0B4" {
		t.Fatal("nerdfont rung must define powerline caps")
	}
	Apply(ModeUnicode)
	if I().CapL != "" || I().CapR != "" {
		t.Fatal("unicode rung must not define caps")
	}
	Apply(ModeNerdFont)
}

func TestParseAuto(t *testing.T) {
	if Parse("") != ModeAuto {
		t.Fatal("empty config value means auto")
	}
	if Parse("auto") != ModeAuto {
		t.Fatal(`"auto" parses to ModeAuto`)
	}
	// A typo must not fall back to nerdfont: that is exactly the setting that
	// prints "?" on every tab for users without the font.
	if Parse("bogus") != ModeAuto {
		t.Fatal("unknown values fall back to auto, not nerdfont")
	}
	for s, want := range map[string]Mode{"nerdfont": ModeNerdFont, "unicode": ModeUnicode, "off": ModeOff} {
		if got := Parse(s); got != want {
			t.Fatalf("Parse(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestApplyResolvesAuto(t *testing.T) {
	t.Cleanup(func() { Apply(ModeNerdFont) })

	Apply(ModeAuto)
	// ModeAuto is a resolution-time value only: it must never survive into
	// Current(), because sets has no entry for it.
	if got := Current(); got != ModeNerdFont && got != ModeUnicode {
		t.Fatalf("Current() = %v, want a concrete resolved rung", got)
	}
	if I().Check == "" {
		t.Fatal("auto must resolve to a populated glyph set")
	}

	Apply(ModeUnicode)
	if Current() != ModeUnicode {
		t.Fatal("Current() tracks the applied mode")
	}
}

func TestFallbackGlyphsAreNotQuestionMarks(t *testing.T) {
	t.Cleanup(func() { Apply(ModeNerdFont) })
	Apply(ModeUnicode)
	s := I()
	for name, g := range map[string]string{
		"Profile": s.Profile, "Store": s.Store, "Model": s.Model, "Tuple": s.Tuple,
		"Change": s.Change, "Query": s.Query, "Assert": s.Assert, "APILog": s.APILog,
		"Dot": s.Dot, "Caret": s.Caret, "Check": s.Check, "Cross": s.Cross,
	} {
		// The whole point of this rung is to be readable where Nerd Font
		// glyphs are not. A literal "?" is indistinguishable from the missing
		// -glyph symptom it exists to avoid.
		if g == "?" {
			t.Fatalf("unicode %s glyph is a literal question mark", name)
		}
		if g == "" {
			t.Fatalf("unicode %s glyph is empty", name)
		}
	}
	if s.Query == s.Model {
		t.Fatal("Query and Model glyphs must be distinguishable")
	}
}
