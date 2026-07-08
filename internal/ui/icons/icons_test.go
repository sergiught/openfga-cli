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
