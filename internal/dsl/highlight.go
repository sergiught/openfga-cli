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

// Highlight returns dsl with each lexer token wrapped in a theme-colored
// lipgloss style. Whitespace, newlines, and comments (any text the lexer skips
// or hides) are copied verbatim by filling the gaps between token character
// offsets, so stripping ANSI from Highlight(x) yields exactly x.
func Highlight(dsl string) string {
	if dsl == "" {
		return ""
	}

	input := antlr.NewInputStream(dsl)
	lexer := parser.NewOpenFGALexer(input)
	lexer.RemoveErrorListeners() // keep lex errors off stderr; text is still preserved
	stream := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	stream.Fill()

	runes := []rune(dsl)
	var b strings.Builder
	cursor := 0

	for _, tok := range stream.GetAllTokens() {
		if tok.GetTokenType() == antlr.TokenEOF {
			break
		}
		start, stop := tok.GetStart(), tok.GetStop()
		if start < 0 || stop < start {
			continue
		}
		if start > cursor {
			b.WriteString(string(runes[cursor:start])) // gap: whitespace/comments
		}
		text := string(runes[start : stop+1])
		if c, ok := tokenColor(tok.GetTokenType()); ok {
			b.WriteString(lipgloss.NewStyle().Foreground(c).Render(text))
		} else {
			b.WriteString(text)
		}
		cursor = stop + 1
	}
	if cursor < len(runes) {
		b.WriteString(string(runes[cursor:]))
	}
	return b.String()
}
