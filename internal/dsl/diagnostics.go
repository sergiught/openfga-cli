// Package dsl provides syntax highlighting and syntax-error diagnostics for
// OpenFGA DSL text, built on the openfga/language ANTLR lexer and parser.
package dsl

import (
	"regexp"
	"strconv"

	transformer "github.com/openfga/language/pkg/go/transformer"
)

// Diagnostic is a single syntax error located in DSL source. Line and Col are
// zero-based (Line matches the library's stored line index; render as Line+1,
// Col+1 for humans).
type Diagnostic struct {
	Line int
	Col  int
	Msg  string
}

// diagRe extracts positions from the library's error string, formatted as
// "syntax error at line=N, column=M: message". The library's fields are
// unexported, so string parsing is the only route to structured positions.
var diagRe = regexp.MustCompile(`line=(\d+), column=(\d+): (.*)`)

// Diagnostics parses dsl and returns one Diagnostic per syntax error, or nil
// when the DSL is syntactically valid. It performs no semantic validation.
func Diagnostics(dsl string) []Diagnostic {
	_, errListener := transformer.ParseDSL(dsl)

	if errListener.Errors == nil {
		return nil
	}

	errs := errListener.Errors.Errors
	diags := make([]Diagnostic, 0, len(errs))

	for _, err := range errs {
		matches := diagRe.FindStringSubmatch(err.Error())
		if len(matches) != 4 {
			// Unexpected format, skip
			continue
		}

		line, _ := strconv.Atoi(matches[1])
		col, _ := strconv.Atoi(matches[2])
		msg := matches[3]

		// Library already stores line as 0-based in the error string
		diags = append(diags, Diagnostic{
			Line: line,
			Col:  col,
			Msg:  msg,
		})
	}

	if len(diags) == 0 {
		return nil
	}

	return diags
}
