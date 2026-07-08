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
	if Parse("bogus") != ModeNerdFont {
		t.Fatal("unknown mode defaults to nerdfont")
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
	if s.CapL != "\U0000E0B6" || s.CapR != "\U0000E0B4" {
		t.Fatal("nerdfont rung must define powerline caps")
	}
	Apply(ModeUnicode)
	if I().CapL != "" || I().CapR != "" {
		t.Fatal("unicode rung must not define caps")
	}
	Apply(ModeNerdFont)
}
