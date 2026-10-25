package mapping

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// BuildTests turns each rule's preview sample into a `tests:` entry, so a
// saved file carries a regression test for exactly what the workspace showed.
//
// Each sample is evaluated through the whole document, not through its own
// rule alone: the `tests:` entry asserts everything the mapping emits for
// that input, so if the sample also trips a neighbouring rule, the expected
// tuples include that rule's tuples too, and the generated test fails the
// moment that stops being true. Samples shared by multiple rules collapse
// into one entry for the same reason.
//
// Rules without a sample, samples that emit nothing or a tuple CheckTuple
// rejects, and documents that do not compile all yield no tests: BuildTests
// never ships a test that asserts nothing, one that cannot run, or one that
// asserts a tuple FGA would refuse. judgeSample decides, for CheckSamples too.
func BuildTests(ctx context.Context, d *Document) []TestCase {
	var out []TestCase
	for _, i := range uniqueSamples(d) {
		if tc, _, _ := judgeSample(ctx, d, i); tc != nil {
			out = append(out, *tc)
		}
	}
	return out
}

// NewTest is a test of event asserting what the whole document produces for it
// now: the direct tuples, every filter operation's tuples and filters, and
// filter coverage when it holds — the test a save would generate from a sample
// of that event. An event no rule matches makes a test expecting nothing.
//
// When the document does not compile, the event fails to evaluate, or it
// produces a tuple CheckTuple rejects, the error says why and the test comes
// back expecting nothing, so a caller can still keep it and let it fail with
// that reason. The last two are judgeSample's faults, through the same
// sampleFaults, so an own test never asserts a tuple no save would generate a
// test for.
func NewTest(ctx context.Context, d *Document, name string, event map[string]any) (TestCase, error) {
	p := Evaluate(ctx, d, event)
	if err := compileFailure(p); err != nil {
		return TestCase{Name: name, Input: event}, err
	}
	if faults := sampleFaults(p); len(faults) > 0 {
		return TestCase{Name: name, Input: event}, errors.New(faults[0].msg)
	}
	return testFrom(name, event, p), nil
}

// compileFailure is why p was not evaluated: the compile's first diagnostic,
// or the failure that left none. nil when the event ran, or there was none.
func compileFailure(p Preview) error {
	if p.Evaluated {
		return nil
	}
	if ps := p.Problems(); len(ps) > 0 {
		return fmt.Errorf("the mapping does not compile: %s", ps[0].Message)
	}
	if p.EvalErr != nil {
		return fmt.Errorf("the mapping does not compile: %w", p.EvalErr)
	}
	return nil
}

// SavedTests is the `tests:` a save writes: the user's own tests (d.Tests)
// first, then the ones BuildTests generates from the rules' samples.
//
// A generated test whose input is already an own test's is dropped, so the
// user's expectations win; that is how a generated test becomes the user's own
// — copied into d.Tests, then edited. A generated test whose name is taken is
// renamed with UniqueTestName, since mapper reports results by name.
func SavedTests(ctx context.Context, d *Document) []TestCase {
	tests, _ := SavedTestsAndOrigins(ctx, d)
	return tests
}

// SavedTestsAndOrigins is SavedTests, beside a map from the name of each
// generated test in it to the index of the rule whose sample it came from; a
// test the map does not name is one of the user's own. Every generated test
// costs an evaluation of its sample, so a caller that needs both asks once.
func SavedTestsAndOrigins(ctx context.Context, d *Document) ([]TestCase, map[string]int) {
	out := append([]TestCase(nil), d.Tests...)
	own := map[string]bool{}
	for _, tc := range d.Tests {
		if key, err := SampleKey(tc.Input); err == nil {
			own[key] = true
		}
	}
	from := map[string]int{}
	for _, i := range uniqueSamples(d) {
		tc, _, _ := judgeSample(ctx, d, i)
		if tc == nil {
			continue
		}
		if key, err := SampleKey(tc.Input); err == nil && own[key] {
			continue
		}
		tc.Name = UniqueTestName(out, tc.Name)
		from[tc.Name] = i
		out = append(out, *tc)
	}
	return out, from
}

// UniqueTestName is name, or name with " 2", " 3"… appended when tests already
// has a test of that name.
func UniqueTestName(tests []TestCase, name string) string {
	taken := make(map[string]bool, len(tests))
	for _, tc := range tests {
		taken[tc.Name] = true
	}
	if !taken[name] {
		return name
	}
	for n := 2; ; n++ {
		if next := fmt.Sprintf("%s %d", name, n); !taken[next] {
			return next
		}
	}
}

// SampleKey identifies an event by content. json.Marshal sorts map keys, so
// two rules pointing at equal events produce the same key.
func SampleKey(event map[string]any) (string, error) {
	b, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Event is one event to apply, named for where it came from.
type Event struct {
	Label   string // "test <name>" or "rule <name> sample"
	Payload map[string]any
}

// Events lists d's test inputs, then its rules' samples that are not already
// among them, deduplicated by content. An unnamed rule is named by its
// position, and empty events are left out: they have nothing to map.
func Events(d *Document) []Event {
	var out []Event
	seen := map[string]bool{}
	add := func(label string, event map[string]any) {
		if len(event) == 0 {
			return
		}
		key, err := SampleKey(event)
		if err != nil || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Event{Label: label, Payload: event})
	}
	for _, tc := range d.Tests {
		add("test "+tc.Name, tc.Input)
	}
	for i, r := range d.Rules {
		if r.Sample == nil {
			continue
		}
		name := r.Name
		if name == "" {
			name = strconv.Itoa(i + 1)
		}
		add("rule "+name+" sample", r.Sample.Event)
	}
	return out
}
