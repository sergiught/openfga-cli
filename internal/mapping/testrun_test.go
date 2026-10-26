package mapping

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/openfga/mapper/language"
)

func runYAML(t *testing.T, src string) *TestRun {
	t.Helper()
	f, err := LoadBytes("m.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	run, err := RunTests(context.Background(), f, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestRunTestsPassesAndFails(t *testing.T) {
	f, err := Load("testdata/load/commented.yaml")
	if err != nil {
		t.Fatal(err)
	}
	run, err := RunTests(context.Background(), f, "", false)
	if err != nil || len(run.Tests) != 1 || !run.Tests[0].Passed || run.Tests[0].Line != 19 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	if got := run.Tests[0].Fired; len(got) != 1 || got[0] != "member-added" {
		t.Fatalf("fired = %v", got)
	}

	f.Doc.Tests[0].ExpectTuples[0].Relation = "admin"
	src, _, err := f.Render(f.Doc)
	if err != nil {
		t.Fatal(err)
	}
	g, err := LoadBytes("m.yaml", src)
	if err != nil {
		t.Fatal(err)
	}
	run, _ = RunTests(context.Background(), g, "", false)
	o := run.Tests[0]
	if o.Passed || len(o.Missing) != 1 || o.Missing[0].Relation != "admin" || len(o.Unexpected) != 1 || o.Unexpected[0].Relation != "member" {
		t.Fatalf("outcome = %+v", o)
	}
	if len(o.Expected) != 1 || o.Expected[0].Relation != "admin" || len(o.Actual) != 1 || o.Actual[0].Relation != "member" {
		t.Fatalf("full sets = %+v / %+v", o.Expected, o.Actual)
	}
	if run.Failed() != 1 {
		t.Fatalf("failed = %d", run.Failed())
	}
}

func TestRunTestsFilter(t *testing.T) {
	f, err := Load("testdata/load/commented.yaml")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := RunTests(context.Background(), f, "nomatch", false)
	if len(run.Tests) != 0 || run.Filtered != 1 {
		t.Fatalf("run = %+v", run)
	}
	run, _ = RunTests(context.Background(), f, "add", false)
	if len(run.Tests) != 1 {
		t.Fatalf("run = %+v", run)
	}
}

const dupExpectYAML = `version: "1"
rules:
  - name: r
    when: input.type == "x"
    tuples:
      - {user: "user:1", relation: member, object: "team:1"}
tests:
  - name: dup
    input: {type: x}
    expect_tuples:
      - {user: "user:1", relation: member, object: "team:1", action: write}
      - {user: "user:1", relation: member, object: "team:1", action: write}
`

func TestRunTestsDiffsAreMultisets(t *testing.T) {
	o := runYAML(t, dupExpectYAML).Tests[0]
	if o.Passed || len(o.Missing) != 1 || len(o.Unexpected) != 0 {
		t.Fatalf("outcome = %+v", o)
	}
}

func TestRunTestsFiltersOnlyDiffedWhenExpected(t *testing.T) {
	o := runYAML(t, `version: "1"
rules:
  - name: r
    when: input.type == "x"
    tuple_filters:
      - {relation: member, object: "team:1"}
    tuples:
      - {user: "user:1", relation: member, object: "team:1"}
tests:
  - name: nofilters
    input: {type: x}
    expect_tuples:
      - {user: "user:1", relation: member, object: "team:1", action: write}
`).Tests[0]
	if !o.Passed || o.MissingFilters != nil || o.UnexpectedFilters != nil {
		t.Fatalf("outcome = %+v", o)
	}
}

func TestRunTestsReportsUncoveredWrites(t *testing.T) {
	o := runYAML(t, `version: "1"
rules:
  - name: r
    when: input.type == "x"
    tuple_filters:
      - {relation: member, object: "team:2"}
    tuples:
      - {user: "user:1", relation: member, object: "team:1"}
tests:
  - name: uncovered
    input: {type: x}
    assert_writes_covered_by_filter: true
    expect_tuples:
      - {user: "user:1", relation: member, object: "team:1", action: write}
`).Tests[0]
	if o.Passed || len(o.Missing) != 0 || len(o.Unexpected) != 0 || len(o.Uncovered) != 1 || o.Uncovered[0].Object != "team:1" {
		t.Fatalf("outcome = %+v", o)
	}
}

// Two tests may share a name. Each outcome takes its coverage setting and its
// line from its own test, not from the first test of that name.
func TestRunTestsMatchesOutcomesToTestsByPosition(t *testing.T) {
	src := `version: "1"
rules:
  - name: r
    when: input.type == "x"
    tuple_filters:
      - {relation: member, object: "team:2"}
    tuples:
      - {user: "user:1", relation: member, object: "team:1"}
tests:
  - name: other
    input: {type: y}
  - name: same
    input: {type: x}
    expect_tuples:
      - {user: "user:1", relation: member, object: "team:1", action: write}
  - name: same
    input: {type: x}
    assert_writes_covered_by_filter: true
    expect_tuples:
      - {user: "user:1", relation: member, object: "team:1", action: write}
`
	f, err := LoadBytes("m.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	run, err := RunTests(context.Background(), f, "same", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Tests) != 2 {
		t.Fatalf("tests = %+v", run.Tests)
	}
	first, second := run.Tests[0], run.Tests[1]
	if !first.Passed || len(first.Uncovered) != 0 || second.Passed || len(second.Uncovered) != 1 {
		t.Fatalf("first = %+v\nsecond = %+v", first, second)
	}
	l1, _ := f.TestLine(1)
	l2, _ := f.TestLine(2)
	if first.Line != l1 || second.Line != l2 || l1 == l2 {
		t.Fatalf("lines = %d, %d; want %d, %d", first.Line, second.Line, l1, l2)
	}
}

func TestRunTestsCompileFailure(t *testing.T) {
	f := &File{Path: "bad.yaml", Src: []byte("version: \"1\"\nrules:\n  - name: r\n")}
	_, err := RunTests(context.Background(), f, "", false)
	if _, ok := err.(*LoadError); !ok {
		t.Fatalf("err = %T %v", err, err)
	}
}

// RunTestsWithTimeout caps each evaluation at the timeout given, where RunTests
// keeps mapper's default: a nanosecond runs out before the first rule.
func TestRunTestsWithTimeoutCapsEachEvaluation(t *testing.T) {
	f, err := Load("testdata/load/commented.yaml")
	if err != nil {
		t.Fatal(err)
	}
	run, err := RunTestsWithTimeout(context.Background(), f, "", false, time.Nanosecond)
	if err != nil || len(run.Tests) != 1 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	if o := run.Tests[0]; o.Passed || !errors.Is(o.Err, context.DeadlineExceeded) {
		t.Fatalf("outcome = %+v, want the deadline", o)
	}
	if run, err := RunTests(context.Background(), f, "", false); err != nil || !run.Tests[0].Passed {
		t.Fatalf("RunTests: run=%+v err=%v", run, err)
	}
}

// TupleText shows everything that tells two tuples apart to mapper's runner:
// the condition, its context sorted by key with each value as JSON, and a delete.
func TestTupleTextShowsWhatTellsTwoTuplesApart(t *testing.T) {
	base := language.Tuple{User: "user:1", Relation: "member", Object: "team:1", Action: language.ActionWrite}
	cond := base
	cond.Condition = "in_region"
	withContext := cond
	withContext.Context = map[string]any{"tier": "gold", "region": "eu"}
	number, numeral := cond, cond
	number.Context = map[string]any{"level": 2}
	numeral.Context = map[string]any{"level": "2"}
	del := base
	del.Action = language.ActionDelete
	for want, tu := range map[string]language.Tuple{
		"user:1 member team:1":                                      base,
		"user:1 member team:1 (in_region)":                          cond,
		`user:1 member team:1 (in_region region="eu", tier="gold")`: withContext,
		"user:1 member team:1 (in_region level=2)":                  number,
		`user:1 member team:1 (in_region level="2")`:                numeral,
		"user:1 member team:1 (delete)":                             del,
	} {
		if got := TupleText(tu, nil); got != want {
			t.Errorf("TupleText(%+v) = %q, want %q", tu, got, want)
		}
	}
	// field sees every value the tuple carries, context keys included.
	if got := TupleText(withContext, strings.ToUpper); got != `USER:1 MEMBER TEAM:1 (IN_REGION REGION="EU", TIER="GOLD")` {
		t.Errorf("escaped = %q", got)
	}
}
