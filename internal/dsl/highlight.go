package dsl

import (
	"image/color"
	"strings"

	"github.com/antlr4-go/antlr/v4"
	parser "github.com/openfga/language/pkg/go/gen"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// classify maps a token type to a theme color based on its semantic role.
// prev is the previous non-whitespace/non-newline token type (0 if none yet).
// bracketDepth tracks nesting depth inside [...].
// Returns nil for identifiers in RHS context (default foreground).
func classify(tokenType, prev, bracketDepth int) color.Color {
	switch tokenType {
	case parser.OpenFGALexerMODEL, parser.OpenFGALexerSCHEMA, parser.OpenFGALexerSCHEMA_VERSION,
		parser.OpenFGALexerTYPE, parser.OpenFGALexerRELATIONS, parser.OpenFGALexerRELATION,
		parser.OpenFGALexerDEFINE, parser.OpenFGALexerCONDITION, parser.OpenFGALexerEXTEND,
		parser.OpenFGALexerMODULE:
		return style.Keyword
	case parser.OpenFGALexerAND, parser.OpenFGALexerOR, parser.OpenFGALexerBUT_NOT,
		parser.OpenFGALexerFROM, parser.OpenFGALexerKEYWORD_WITH:
		return style.Accent
	case parser.OpenFGALexerCOLON, parser.OpenFGALexerCOMMA, parser.OpenFGALexerHASH,
		parser.OpenFGALexerLBRACKET, parser.OpenFGALexerRBRACKET, parser.OpenFGALexerRPRACKET,
		parser.OpenFGALexerLPAREN, parser.OpenFGALexerRPAREN, parser.OpenFGALexerLESS,
		parser.OpenFGALexerGREATER:
		return style.Muted
	case parser.OpenFGALexerSTRING, parser.OpenFGALexerNUM_FLOAT, parser.OpenFGALexerNUM_INT,
		parser.OpenFGALexerNUM_UINT, parser.OpenFGALexerCEL_TRUE, parser.OpenFGALexerCEL_FALSE:
		return style.Green
	case parser.OpenFGALexerIDENTIFIER, parser.OpenFGALexerEXTENDED_IDENTIFIER:
		switch {
		case prev == parser.OpenFGALexerTYPE:
			return style.Violet // type name
		case prev == parser.OpenFGALexerDEFINE || prev == parser.OpenFGALexerRELATION ||
			prev == parser.OpenFGALexerCONDITION:
			return style.Amber // relation/condition name
		case bracketDepth > 0:
			return style.Violet // type reference inside [...]
		default:
			return nil // RHS references stay default
		}
	default:
		return nil
	}
}

// commentMask marks runes belonging to a `#` line comment. A `#` at line start or
// immediately after whitespace begins a comment through end of line; a `#` after a
// non-whitespace rune is a userset separator (group#member) and is not a comment.
func commentMask(runes []rune) []bool {
	mask := make([]bool, len(runes))
	for i := 0; i < len(runes); i++ {
		if runes[i] != '#' {
			continue
		}
		if i != 0 && runes[i-1] != ' ' && runes[i-1] != '\t' && runes[i-1] != '\n' {
			continue
		}
		for j := i; j < len(runes) && runes[j] != '\n'; j++ {
			mask[j] = true
		}
	}
	return mask
}

// Cell is a single source rune paired with its syntax color (nil = default
// foreground). Cells preserves every rune of the input in order, including
// whitespace, newlines, and comments (which have a style.Faintc Color).
type Cell struct {
	R     rune
	Color color.Color
}

// Cells lexes dsl with the ANTLR lexer and returns per-rune colors. Text the
// lexer skips or hides (whitespace, newlines) is preserved with a nil Color by
// filling gaps between token character offsets. Comments are dimmed (style.Faintc).
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

	// Mark which runes are part of `#` line comments. This is a pure rune scan,
	// independent of ANTLR/bracket state, so comment recognition works even mid-
	// edit when brackets are unbalanced (e.g. an unclosed `[` above a comment).
	comment := commentMask(runes)

	// Color all tokens (except those in comments)
	prev := 0
	bracketDepth := 0
	for _, tok := range stream.GetAllTokens() {
		tt := tok.GetTokenType()
		if tt == antlr.TokenEOF {
			break
		}
		// Whitespace and newline on default channel; skip so they don't
		// pollute previous-significant-token state.
		if tt == parser.OpenFGALexerWHITESPACE || tt == parser.OpenFGALexerNEWLINE {
			continue
		}
		start, stop := tok.GetStart(), tok.GetStop()
		if start < 0 || stop < start || start >= len(runes) {
			continue
		}
		// Comment content dimmed below; do not classify it or track its state.
		if comment[start] {
			continue
		}

		if c := classify(tt, prev, bracketDepth); c != nil {
			for i := start; i <= stop && i < len(runes); i++ {
				colors[i] = c
			}
		}

		switch tt {
		case parser.OpenFGALexerLBRACKET:
			bracketDepth++
		case parser.OpenFGALexerRBRACKET, parser.OpenFGALexerRPRACKET:
			if bracketDepth > 0 {
				bracketDepth--
			}
		}
		prev = tt
	}

	// Dim all comment runes
	for i := range runes {
		if comment[i] {
			colors[i] = style.Faintc
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
