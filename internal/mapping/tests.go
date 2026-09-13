package mapping

import (
	"context"
	"encoding/json"
)

// BuildTests turns each rule's preview sample into a `tests:` entry, so a
// saved file carries a regression test for exactly what the wizard showed.
//
// Each sample is evaluated through the whole document, not through its own
// rule alone: the `tests:` entry asserts everything the mapping emits for
// that input, so if the sample also trips a neighbouring rule, the expected
// tuples include that rule's tuples too, and the generated test fails the
// moment that stops being true. Samples shared by multiple rules collapse
// into one entry for the same reason.
//
// Rules without a sample, samples that emit nothing, and documents that do
// not compile all yield no tests: BuildTests never ships a test that asserts
// nothing or one that cannot run.
func BuildTests(ctx context.Context, d *Document) []TestCase {
	var out []TestCase
	seen := map[string]bool{}

	for _, r := range d.Rules {
		if r.Sample == nil || len(r.Sample.Event) == 0 {
			continue
		}
		key, err := sampleKey(r.Sample.Event)
		if err != nil || seen[key] {
			continue
		}
		seen[key] = true

		p := Evaluate(ctx, d, r.Sample.Event)
		if !p.OK() || (len(p.Tuples) == 0 && len(p.Filters) == 0) {
			continue
		}

		tc := TestCase{Name: r.Name, Input: r.Sample.Event, ExpectTuples: p.Tuples}
		for _, op := range p.Filters {
			tc.ExpectTupleFilters = append(tc.ExpectTupleFilters, op.Filters...)
		}
		out = append(out, tc)
	}

	return out
}

// sampleKey identifies a sample by content. json.Marshal sorts map keys, so
// two rules pointing at equal events produce the same key.
func sampleKey(event map[string]any) (string, error) {
	b, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
