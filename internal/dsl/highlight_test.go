package dsl

import (
	"image/color"
	"regexp"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/style"
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func TestHighlightEmptyString(t *testing.T) {
	if got := Highlight(""); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestHighlightRoundTrips(t *testing.T) {
	// Includes a condition-style body and an inline "# comment" to exercise
	// gap-filling of non-token text.
	src := "model\n  schema 1.1\ntype user\ntype document\n  relations\n" +
		"    define viewer: [user] or owner # inline note\n"
	out := Highlight(src)
	if got := stripANSI(out); got != src {
		t.Fatalf("round-trip failed:\n got %q\nwant %q", got, src)
	}
}

func TestHighlightStylesKeywords(t *testing.T) {
	out := Highlight("type user\n")
	// "type" is a keyword and must be wrapped in an SGR sequence, i.e. it is
	// not present as bare text between newlines.
	if strings.Contains(out, "\x1b[") == false {
		t.Fatalf("expected ANSI styling in output, got %q", out)
	}
	if !strings.Contains(stripANSI(out), "type user") {
		t.Fatalf("stripped output missing source text, got %q", stripANSI(out))
	}
}

func TestCellsPreservesAllRunes(t *testing.T) {
	src := "model\n  schema 1.1\ntype user # note\n"
	cs := Cells(src)
	var b strings.Builder
	for _, c := range cs {
		b.WriteRune(c.R)
	}
	if got := b.String(); got != src {
		t.Fatalf("Cells dropped/added runes:\n got %q\nwant %q", got, src)
	}
}

func TestCellsColorsKeyword(t *testing.T) {
	cs := Cells("type user\n")
	// "type" (cols 0-3) is a keyword: those cells must carry a non-nil color;
	// the space at col 4 must be nil.
	for i := 0; i < 4; i++ {
		if cs[i].Color == nil {
			t.Fatalf("expected keyword rune %d (%q) to be colored", i, cs[i].R)
		}
	}
	if cs[4].R != ' ' || cs[4].Color != nil {
		t.Fatalf("expected space at col 4 to be uncolored, got %+v", cs[4])
	}
}

func TestCellsEmpty(t *testing.T) {
	if cs := Cells(""); cs != nil {
		t.Fatalf("expected nil for empty input, got %v", cs)
	}
}

// runeColor returns Color of first rune of sub within src.
func runeColor(t *testing.T, src, sub string) color.Color {
	t.Helper()
	idx := strings.Index(src, sub)
	if idx < 0 {
		t.Fatalf("substring %q not found in %q", sub, src)
	}
	return Cells(src)[len([]rune(src[:idx]))].Color
}

// colorsEqual compares two colors by their RGBA values.
func colorsEqual(a, b color.Color) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	r1, g1, b1, a1 := a.RGBA()
	r2, g2, b2, a2 := b.RGBA()
	return r1 == r2 && g1 == g2 && b1 == b2 && a1 == a2
}

func TestCellsColorsTypeName(t *testing.T) {
	src := "type document\n"
	if c := runeColor(t, src, "document"); !colorsEqual(c, style.Violet) {
		t.Fatalf("type name should be Violet, got %v", c)
	}
	if c := runeColor(t, src, "type"); !colorsEqual(c, style.Keyword) {
		t.Fatalf("type keyword should be Keyword, got %v", c)
	}
}

func TestCellsColorsRelationName(t *testing.T) {
	src := "type d\n  relations\n    define viewer: [user]\n"
	if c := runeColor(t, src, "viewer"); !colorsEqual(c, style.Amber) {
		t.Fatalf("relation name should be Amber, got %v", c)
	}
	if c := runeColor(t, src, "define"); !colorsEqual(c, style.Keyword) {
		t.Fatalf("define keyword should be Keyword, got %v", c)
	}
}

func TestCellsColorsConditionName(t *testing.T) {
	src := "type d\n  relations\n    define v: [user]\n  condition active(x: uint64) {\n    x > 0\n  }\n"
	if c := runeColor(t, src, "active"); !colorsEqual(c, style.Amber) {
		t.Fatalf("condition name should be Amber, got %v", c)
	}
	if c := runeColor(t, src, "condition"); !colorsEqual(c, style.Keyword) {
		t.Fatalf("condition keyword should be Keyword, got %v", c)
	}
}

func TestCellsColorsTypeReferenceInBrackets(t *testing.T) {
	src := "type d\n  relations\n    define viewer: [user]\n"
	if c := runeColor(t, src, "user"); !colorsEqual(c, style.Violet) {
		t.Fatalf("type reference in brackets should be Violet, got %v", c)
	}
}

func TestCellsRHSReferenceStaysDefault(t *testing.T) {
	// RHS references (after relation definitions) should be default (nil)
	src := "type d\n  relations\n    define viewer: owner or x from parent\n"
	if c := runeColor(t, src, "owner"); !colorsEqual(c, nil) {
		t.Fatalf("RHS reference 'owner' should be default (nil), got %v", c)
	}
	if c := runeColor(t, src, "parent"); !colorsEqual(c, nil) {
		t.Fatalf("RHS reference 'parent' should be default (nil), got %v", c)
	}
}

func TestCellsDimsComment(t *testing.T) {
	src := "type user # note\n"
	if c := runeColor(t, src, "#"); !colorsEqual(c, style.Faintc) {
		t.Fatalf("comment '#' should be Faintc, got %v", c)
	}
	if c := runeColor(t, src, "note"); !colorsEqual(c, style.Faintc) {
		t.Fatalf("comment body should be Faintc, got %v", c)
	}
}

func TestCellsCommentDimmedWithUnclosedBracketAbove(t *testing.T) {
	// Mid-typing: an unclosed '[' must not stop a later line-comment from dimming.
	src := "type d\n  relations\n    define v: [user\n# a note\n"
	if c := runeColor(t, src, "# a note"); !colorsEqual(c, style.Faintc) {
		t.Fatalf("comment after an unclosed bracket should be Faintc, got %v", c)
	}
	if c := runeColor(t, src, "note"); !colorsEqual(c, style.Faintc) {
		t.Fatalf("comment body after an unclosed bracket should be Faintc, got %v", c)
	}
}

func TestCellsUsersetHashIsNotComment(t *testing.T) {
	// `group#member` — # separator (Muted), not comment.
	src := "type d\n  relations\n    define v: [group#member]\n"
	if c := runeColor(t, src, "#"); !colorsEqual(c, style.Muted) {
		t.Fatalf("userset '#' should be Muted, got %v", c)
	}
}

func TestCellsClosingBracketIsPunctuation(t *testing.T) {
	// `]` lexes RPRACKET; still colored punctuation.
	src := "type d\n  relations\n    define v: [user]\n"
	if c := runeColor(t, src, "]"); !colorsEqual(c, style.Muted) {
		t.Fatalf("closing bracket should be Muted, got %v", c)
	}
}
