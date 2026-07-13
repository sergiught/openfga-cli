package dsl

import (
	"regexp"
	"strings"
	"testing"
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
