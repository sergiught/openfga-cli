package mapping

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/openfga/mapper"
	"github.com/openfga/mapper/language"
)

// TestOutcome is one embedded test's result, with enough detail to say why it
// failed. The diffs mirror mapper's own pass logic so a failing test always
// shows a reason and a passing one never shows noise.
type TestOutcome struct {
	Name   string
	Line   int
	Passed bool
	// Missing and Unexpected are multiset differences between expected and
	// actual tuples.
	Missing, Unexpected []language.Tuple
	// MissingFilters and UnexpectedFilters are only set when the test declares
	// expect_tuple_filters, the only case in which mapper compares filters.
	MissingFilters, UnexpectedFilters []language.TupleFilter
	// Uncovered holds the write tuples no filter covers, for tests that set
	// assert_writes_covered_by_filter.
	Uncovered []language.Tuple
	// Expected and Actual are the full sets the test compared; the filter
	// pair is set only when the test declares expect_tuple_filters.
	Expected, Actual               []language.Tuple
	ExpectedFilters, ActualFilters []language.TupleFilter
	Fired                          []string // rules whose `when` matched
	Err                            error
	Duration                       time.Duration
}

// TestRun is the outcome of running a file's tests.
type TestRun struct {
	Tests             []TestOutcome
	Filtered, Skipped int
}

// Failed counts the tests that did not pass.
func (r TestRun) Failed() int {
	n := 0
	for _, o := range r.Tests {
		if !o.Passed {
			n++
		}
	}
	return n
}

// RunTests compiles f and runs its tests whose name contains filter, each
// evaluation capped at mapper's DefaultTimeout as CompileSource caps it.
// A compile failure returns a *LoadError with mapper's diagnostics.
func RunTests(ctx context.Context, f *File, filter string, failFast bool) (*TestRun, error) {
	return RunTestsWithTimeout(ctx, f, filter, failFast, mapper.DefaultTimeout)
}

// RunTestsWithTimeout is RunTests with each evaluation capped at timeout. The
// workspace passes EvalTimeout: it runs every test on each edit, synchronously,
// so a runaway expression must cost it a second a test, not mapper's three. A
// test that takes between the two then fails there and passes in
// `ofga mappings test`, which is the CLI's verdict to keep.
func RunTestsWithTimeout(ctx context.Context, f *File, filter string, failFast bool, timeout time.Duration) (*TestRun, error) {
	m, diags, err := compileSource(f.Src, timeout)
	if err != nil {
		return nil, &LoadError{Path: f.Path, Diagnostics: diags}
	}
	fr := m.RunTestsFiltered(ctx, filter, failFast)
	run := &TestRun{Filtered: fr.Filtered, Skipped: fr.Skipped}
	for _, r := range fr.Results {
		o := TestOutcome{
			Name: r.Name, Passed: r.Passed, Err: r.Error, Duration: r.Duration,
			Expected: r.Expected, Actual: r.Actual,
			Missing:    minusTuples(r.Expected, r.Actual),
			Unexpected: minusTuples(r.Actual, r.Expected),
		}
		if len(r.ExpectedTupleFilters) > 0 {
			o.ExpectedFilters, o.ActualFilters = r.ExpectedTupleFilters, r.ActualTupleFilters
			o.MissingFilters = minusFilters(r.ExpectedTupleFilters, r.ActualTupleFilters)
			o.UnexpectedFilters = minusFilters(r.ActualTupleFilters, r.ExpectedTupleFilters)
		}
		if f.assertsCoverage(r.Name) {
			o.Uncovered = uncoveredWrites(r.Actual, r.ActualTupleFilters)
		}
		if r.Trace != nil {
			for _, rt := range r.Trace.Rules {
				if rt.Status == mapper.RuleMatched {
					o.Fired = append(o.Fired, rt.Name)
				}
			}
		}
		o.Line = f.testLineByName(r.Name)
		run.Tests = append(run.Tests, o)
	}
	return run, nil
}

// TupleText is a test's tuple, expected or actual, on one line: user relation
// object, then its condition with the context sorted by key, then a delete
// marked as one. mapper's runner tells two tuples apart by every one of these,
// so a failure that left one out could print a missing and an unexpected line
// that read the same. field escapes each value taken from the tuple, for a
// caller writing straight to a terminal; nil leaves them as they are.
func TupleText(t language.Tuple, field func(string) string) string {
	if field == nil {
		field = func(s string) string { return s }
	}
	s := field(t.User) + " " + field(t.Relation) + " " + field(t.Object)
	if t.Condition != "" {
		cond := field(t.Condition)
		if len(t.Context) > 0 {
			pairs := make([]string, 0, len(t.Context))
			for _, k := range slices.Sorted(maps.Keys(t.Context)) {
				pairs = append(pairs, field(k)+"="+field(fmt.Sprint(t.Context[k])))
			}
			cond += " " + strings.Join(pairs, ", ")
		}
		s += " (" + cond + ")"
	}
	if t.Action == language.ActionDelete {
		s += " (delete)"
	}
	return s
}

func (f *File) testLineByName(name string) int {
	for i, t := range f.Doc.Tests {
		if t.Name == name {
			line, _ := f.TestLine(i)
			return line
		}
	}
	return 0
}

func (f *File) assertsCoverage(name string) bool {
	for _, t := range f.Doc.Tests {
		if t.Name == name {
			return t.AssertWritesCoveredByFilter
		}
	}
	return false
}

// minusTuples returns a without the tuples in b, counting duplicates the way
// mapper's tuplesMatch does: [A, A] minus [A] is [A].
func minusTuples(a, b []language.Tuple) []language.Tuple {
	counts := make(map[string]int, len(b))
	for _, t := range b {
		counts[t.Key()]++
	}
	var out []language.Tuple
	for _, t := range a {
		k := t.Key()
		if counts[k] > 0 {
			counts[k]--
			continue
		}
		out = append(out, t)
	}
	return out
}

// minusFilters is minusTuples for tuple filters.
func minusFilters(a, b []language.TupleFilter) []language.TupleFilter {
	counts := make(map[language.TupleFilter]int, len(b))
	for _, f := range b {
		counts[f]++
	}
	var out []language.TupleFilter
	for _, f := range a {
		if counts[f] > 0 {
			counts[f]--
			continue
		}
		out = append(out, f)
	}
	return out
}

func uncoveredWrites(tuples []language.Tuple, filters []language.TupleFilter) []language.Tuple {
	var out []language.Tuple
	for _, t := range tuples {
		if t.Action != language.ActionWrite {
			continue
		}
		covered := false
		for _, f := range filters {
			if matchesFilter(t, f) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, t)
		}
	}
	return out
}
