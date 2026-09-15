package mapping

import (
	"context"
	"fmt"
	"strings"

	"github.com/openfga/mapper"
	"github.com/openfga/mapper/language"
)

// Preview is everything the wizard shows about a document at one moment: the
// bytes it would save, whether they compile, and what the current sample event
// turns into.
type Preview struct {
	YAML        []byte
	Diagnostics mapper.Diagnostics
	Tuples      []language.Tuple
	Filters     []mapper.TupleFilterOperation
	Rules       []mapper.RuleTrace

	// Evaluated reports whether the sample was actually run through the mapping.
	// It is false when there was no sample, and false when the document did not
	// compile — and the two are indistinguishable from the fields above, because
	// both leave Tuples empty. A caller that counts tuples needs the difference:
	// zero tuples measured is a fact about the mapping, zero tuples because
	// nothing ran is a fact about nothing.
	Evaluated bool

	// EvalErr holds a failure that produced no diagnostics to show instead — a
	// Marshal rendering failure, say, where there is no mapper error to derive
	// diagnostics from. An evaluation error from mapper itself is total over
	// DiagnosticsFrom, so it lands in Diagnostics, not here.
	EvalErr error
}

// OK reports whether the document both compiled and evaluated cleanly.
func (p Preview) OK() bool { return len(p.Diagnostics) == 0 && p.EvalErr == nil }

// TupleCount is how many tuples the sample produced, counted the way mapper
// counts them against MaxTuples: a rule's own tuples plus the tuples every
// filter reconciled against. Both halves count, so a rule that writes twenty
// tuples and patches twenty more has already spent the whole budget.
func (p Preview) TupleCount() int {
	n := len(p.Tuples)
	for _, op := range p.Filters {
		n += len(op.Tuples)
	}
	return n
}

// Problems renders the diagnostics as document-level problems, so a caller can
// list mapper's verdict alongside Lint's. Only the first line of a diagnostic
// is kept: the rest is the caret art the preview pane already draws.
//
// EvalErr is deliberately not included. A diagnostic means the document itself
// is wrong; EvalErr is the residue mapper produced no diagnostic for, and one
// sample event that fails to evaluate does not make the mapping invalid.
func (p Preview) Problems() []Problem {
	var ps []Problem
	for _, d := range p.Diagnostics {
		msg, _, _ := strings.Cut(d.Message, "\n")
		if d.Field != "" {
			msg = d.Field + ": " + msg
		}
		// A diagnostic mapper could not place carries StartLine 0, and "line 0" is
		// a place no file has. The field path already in msg locates it anyway.
		if d.Position.StartLine > 0 {
			msg = fmt.Sprintf("line %d: %s", d.Position.StartLine, msg)
		}
		ps = append(ps, Problem{Rule: -1, Section: "document", Message: msg})
	}
	return ps
}

// Compile renders d and compiles it, returning the rendered bytes even when
// compilation fails — the preview pane shows the YAML the diagnostics point at.
func Compile(d *Document) ([]byte, *mapper.Mapping, mapper.Diagnostics, error) {
	src, err := Marshal(d)
	if err != nil {
		return nil, nil, nil, err
	}
	m, err := mapper.Compile(src,
		mapper.WithTrace(true),
		mapper.WithTimeout(EvalTimeout),
		mapper.WithMaxTuples(MaxTuples),
		mapper.WithMaxRules(MaxRules),
		mapper.WithMaxIteratorItems(MaxIteratorItems),
	)
	if err != nil {
		return src, nil, mapper.DiagnosticsFrom(err), err
	}
	return src, m, nil, nil
}

// Evaluate compiles d and runs event through it. A nil event yields the compile
// half only, which is what the pane shows before a sample has been chosen.
func Evaluate(ctx context.Context, d *Document, event map[string]any) Preview {
	src, m, diags, err := Compile(d)
	p := Preview{YAML: src, Diagnostics: diags}
	if err != nil {
		if len(diags) == 0 {
			p.EvalErr = err
		}
		return p
	}
	if m == nil || event == nil {
		return p
	}

	p.Evaluated = true
	res, evalErr := m.Evaluate(ctx, event)
	if evalErr != nil {
		if evalDiags := mapper.DiagnosticsFrom(evalErr); len(evalDiags) > 0 {
			p.Diagnostics = append(p.Diagnostics, evalDiags...)
		} else {
			p.EvalErr = evalErr
		}
	}
	if res != nil {
		p.Tuples = res.Tuples
		p.Filters = res.TupleFilterOperations
		if res.Trace != nil {
			p.Rules = res.Trace.Rules
		}
	}
	return p
}
