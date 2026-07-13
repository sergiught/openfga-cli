package dsl

import (
	"image/color"
	"strings"

	"github.com/antlr4-go/antlr/v4"
	parser "github.com/openfga/language/pkg/go/gen"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// tokenColor maps an ANTLR lexer token type to a theme color. The bool is false
// for token types that render in the default foreground (identifiers, unknown).
func tokenColor(tokenType int) (color.Color, bool) {
	switch tokenType {
	case parser.OpenFGALexerMODEL, parser.OpenFGALexerSCHEMA, parser.OpenFGALexerSCHEMA_VERSION,
		parser.OpenFGALexerTYPE, parser.OpenFGALexerRELATIONS, parser.OpenFGALexerRELATION,
		parser.OpenFGALexerDEFINE, parser.OpenFGALexerCONDITION, parser.OpenFGALexerEXTEND,
		parser.OpenFGALexerMODULE:
		return style.Keyword, true
	case parser.OpenFGALexerAND, parser.OpenFGALexerOR, parser.OpenFGALexerBUT_NOT,
		parser.OpenFGALexerFROM, parser.OpenFGALexerKEYWORD_WITH:
		return style.Accent, true
	case parser.OpenFGALexerCOLON, parser.OpenFGALexerCOMMA, parser.OpenFGALexerHASH,
		parser.OpenFGALexerLBRACKET, parser.OpenFGALexerRBRACKET, parser.OpenFGALexerLPAREN,
		parser.OpenFGALexerRPAREN, parser.OpenFGALexerLESS, parser.OpenFGALexerGREATER:
		return style.Muted, true
	case parser.OpenFGALexerSTRING, parser.OpenFGALexerNUM_FLOAT, parser.OpenFGALexerNUM_INT,
		parser.OpenFGALexerNUM_UINT, parser.OpenFGALexerCEL_TRUE, parser.OpenFGALexerCEL_FALSE:
		return style.Green, true
	default:
		return nil, false
	}
}

// Cell is a single source rune paired with its syntax color (nil = default
// foreground). Cells preserves every rune of the input in order, including
// whitespace, newlines, and comments (which have a nil Color).
type Cell struct {
	R     rune
	Color color.Color
}

// Cells lexes dsl with the ANTLR lexer and returns per-rune colors. Text the
// lexer skips or hides (whitespace, comments) is preserved with a nil Color by
// filling the gaps between token character offsets.
func Cells(dsl string) []Cell {
	if dsl == "" {
		return nil
	}

	input := antlr.NewInputStream(dsl)
	lexer := parser.NewOpenFGALexer(input)
	lexer.RemoveErrorListeners()
	stream := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	stream.Fill()

	runes := []rune(dsl)
	colors := make([]color.Color, len(runes))
	for _, tok := range stream.GetAllTokens() {
		if tok.GetTokenType() == antlr.TokenEOF {
			break
		}
		start, stop := tok.GetStart(), tok.GetStop()
		if start < 0 || stop < start || start >= len(runes) {
			continue
		}
		c, ok := tokenColor(tok.GetTokenType())
		if !ok {
			continue
		}
		for i := start; i <= stop && i < len(runes); i++ {
			colors[i] = c
		}
	}

	cells := make([]Cell, len(runes))
	for i, r := range runes {
		cells[i] = Cell{R: r, Color: colors[i]}
	}
	return cells
}

// Highlight returns dsl with each colored run wrapped in a lipgloss style.
// Stripping ANSI from Highlight(x) yields exactly x.
func Highlight(dsl string) string {
	cells := Cells(dsl)
	if len(cells) == 0 {
		return ""
	}

	var b strings.Builder
	i := 0
	for i < len(cells) {
		c := cells[i].Color
		var seg strings.Builder
		j := i
		for j < len(cells) && sameColor(cells[j].Color, c) {
			seg.WriteRune(cells[j].R)
			j++
		}
		if c != nil {
			b.WriteString(lipgloss.NewStyle().Foreground(c).Render(seg.String()))
		} else {
			b.WriteString(seg.String())
		}
		i = j
	}
	return b.String()
}

// sameColor reports whether two theme colors are equal (both nil counts as equal).
func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a == b
}
