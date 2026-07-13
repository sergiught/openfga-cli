package dsl

import (
	"fmt"

	"github.com/antlr4-go/antlr/v4"
	parser "github.com/openfga/language/pkg/go/gen"
)

// UndefinedTypeDiagnostics returns a diagnostic for each reference to a type
// that is not declared in the DSL (e.g. `[user]` when there is no `type user`).
// It validates types only, not relations. It should be called only when the DSL
// has no syntax errors, so the token stream is reliable.
func UndefinedTypeDiagnostics(dsl string) []Diagnostic {
	if dsl == "" {
		return nil
	}

	input := antlr.NewInputStream(dsl)
	lexer := parser.NewOpenFGALexer(input)
	lexer.RemoveErrorListeners()
	stream := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	stream.Fill()

	runes := []rune(dsl)
	declared := make(map[string]bool)
	var references []struct {
		name string
		off  int
	}

	prev := 0
	bracketDepth := 0
	braceDepth := 0

	for _, tok := range stream.GetAllTokens() {
		tt := tok.GetTokenType()
		if tt == antlr.TokenEOF {
			break
		}
		// Skip whitespace and newline; don't update prev state
		if tt == parser.OpenFGALexerWHITESPACE || tt == parser.OpenFGALexerNEWLINE {
			continue
		}

		start := tok.GetStart()

		// Track declared types: identifier after TYPE
		if prev == parser.OpenFGALexerTYPE && (tt == parser.OpenFGALexerIDENTIFIER || tt == parser.OpenFGALexerEXTENDED_IDENTIFIER) {
			if start >= 0 && start < len(runes) {
				text := tok.GetText()
				declared[text] = true
			}
		}

		// Track type references: identifier in brackets [...] after LBRACKET or COMMA.
		// Type restrictions never appear inside {...} (condition bodies), so gate on
		// braceDepth == 0 to avoid flagging CEL list-literal identifiers as types.
		if braceDepth == 0 && bracketDepth > 0 && (prev == parser.OpenFGALexerLBRACKET || prev == parser.OpenFGALexerCOMMA) &&
			(tt == parser.OpenFGALexerIDENTIFIER || tt == parser.OpenFGALexerEXTENDED_IDENTIFIER) {
			if start >= 0 && start < len(runes) {
				text := tok.GetText()
				references = append(references, struct {
					name string
					off  int
				}{text, start})
			}
		}

		// Update bracket depth
		switch tt {
		case parser.OpenFGALexerLBRACKET:
			bracketDepth++
		case parser.OpenFGALexerRBRACKET, parser.OpenFGALexerRPRACKET:
			if bracketDepth > 0 {
				bracketDepth--
			}
		case parser.OpenFGALexerLBRACE:
			braceDepth++
		case parser.OpenFGALexerRBRACE:
			if braceDepth > 0 {
				braceDepth--
			}
		}

		prev = tt
	}

	// Build diagnostics for undefined type references
	var diags []Diagnostic
	for _, ref := range references {
		if !declared[ref.name] {
			line, col := offsetLineCol(runes, ref.off)
			diags = append(diags, Diagnostic{
				Line: line,
				Col:  col,
				Msg:  fmt.Sprintf("undefined type %q", ref.name),
			})
		}
	}

	if len(diags) == 0 {
		return nil
	}
	return diags
}

// offsetLineCol converts a rune offset to a 0-based line and column, matching
// the syntax diagnostics' convention.
func offsetLineCol(runes []rune, off int) (int, int) {
	if off < 0 || off >= len(runes) {
		return 0, 0
	}

	line := 0
	col := 0

	for i := 0; i < off; i++ {
		if runes[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}

	return line, col
}
