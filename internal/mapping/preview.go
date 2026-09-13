package mapping

import (
	"context"

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

	// EvalErr holds an evaluation failure that produced no diagnostics — a
	// context deadline, say. Compile failures come back as Diagnostics instead.
	EvalErr error
}

// OK reports whether the document both compiled and evaluated cleanly.
func (p Preview) OK() bool { return len(p.Diagnostics) == 0 && p.EvalErr == nil }

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
	if err != nil || m == nil || event == nil {
		return p
	}

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
