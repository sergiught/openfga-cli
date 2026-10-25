package mapping_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

func TestCheckTupleWantsTypeAndIDOnBothEnds(t *testing.T) {
	for _, c := range []struct {
		user, object string
		ok           bool
	}{
		{"user:1", "organization:acme", true},
		{"user:*", "organization:acme", true},
		{"group:eng#member", "organization:acme", true},
		{"user:1", "acme", false},
		{"user:1", "organization:", false},
		{"user:1", ":acme", false},
		{"user:", "organization:acme", false},
		{"1", "organization:acme", false},
		{"group:#member", "organization:acme", false},
		{"group:eng#", "organization:acme", false},
	} {
		err := mapping.CheckTuple(language.Tuple{User: c.user, Relation: "member", Object: c.object})
		if (err == nil) != c.ok {
			t.Fatalf("CheckTuple(%s, %s) = %v, want ok=%v", c.user, c.object, err, c.ok)
		}
	}
}

// overBudgetRule writes one tuple per id in its sample: 41 of them, one over
// mapper's per-event budget.
func overBudgetRule() mapping.Rule {
	ids := make([]any, mapping.MaxTuples+1)
	for i := range ids {
		ids[i] = map[string]any{"id": fmt.Sprintf("u%d", i)}
	}
	return mapping.Rule{
		Name: "B", When: `input.type == "b"`,
		Sample: &mapping.Sample{Label: "b", Event: map[string]any{"type": "b", "ids": ids}},
		Iterator: &mapping.Iterator{Source: "input.ids", As: "it", Tuples: []mapping.Tuple{
			{User: "user:{{ it.id }}", Relation: "member", Object: "organization:1"},
		}},
	}
}

func cleanRule(name, typ string) mapping.Rule {
	return mapping.Rule{
		Name: name, When: `input.type == "` + typ + `"`,
		Sample: &mapping.Sample{Label: typ, Event: map[string]any{"type": typ}},
		Tuples: []mapping.Tuple{{User: "user:1", Relation: "member", Object: "organization:1"}},
	}
}

// A5: the save gate evaluated only the rule under the cursor. CheckSamples
// evaluates every sample, so its answer cannot depend on where the cursor is.
func TestCheckSamplesEvaluatesEveryRulesSample(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{cleanRule("A", "a"), overBudgetRule()}}
	ps := mapping.CheckSamples(context.Background(), d)
	if len(ps) != 1 {
		t.Fatalf("problems = %+v, want exactly B's", ps)
	}
	if ps[0].Rule != 1 || ps[0].Warning || !strings.Contains(ps[0].Message, "exceeding limit of 40") {
		t.Fatalf("problem = %+v, want rule 1's budget overrun, blocking", ps[0])
	}
}

// A4: a template that resolves to "" or leaves out the type compiles and
// evaluates cleanly, so nothing caught it before.
func TestCheckSamplesRejectsMalformedTuples(t *testing.T) {
	for name, tuple := range map[string]mapping.Tuple{
		"object without a type": {User: "user:1", Relation: "member", Object: "acme"},
		"user with an empty id": {User: "user:{{ input.empty }}", Relation: "member", Object: "organization:acme"},
	} {
		r := cleanRule("A", "a")
		r.Sample.Event["empty"] = ""
		r.Tuples = []mapping.Tuple{tuple}
		d := &mapping.Document{Rules: []mapping.Rule{r}}
		ps := mapping.CheckSamples(context.Background(), d)
		if len(ps) != 1 || ps[0].Rule != 0 || ps[0].Warning || !strings.Contains(ps[0].Message, "is not type:id") {
			t.Fatalf("%s: problems = %+v, want one blocking problem", name, ps)
		}
		if tests := mapping.BuildTests(context.Background(), d); len(tests) != 0 {
			t.Fatalf("%s: BuildTests made %d tests asserting a tuple FGA refuses", name, len(tests))
		}
	}
}

// Review focus: a rule with no sample has nothing to evaluate, and that is not
// a fault — the spec has it fire no sample until the user picks one.
func TestCheckSamplesSkipsRulesWithoutASample(t *testing.T) {
	bare := cleanRule("A", "a")
	bare.Sample = nil
	d := &mapping.Document{Rules: []mapping.Rule{bare, cleanRule("B", "b")}}
	if ps := mapping.CheckSamples(context.Background(), d); len(ps) != 0 {
		t.Fatalf("problems = %+v, want none", ps)
	}
}

func TestCheckSamplesLeavesACompileFailureToTheCompile(t *testing.T) {
	r := cleanRule("A", "a")
	r.When = "input.type =="
	d := &mapping.Document{Rules: []mapping.Rule{r}}
	if ps := mapping.CheckSamples(context.Background(), d); len(ps) != 0 {
		t.Fatalf("problems = %+v, want none: the compile diagnostics report this", ps)
	}
}

// A7: mapper checks the budget before appending a rule's batch, so the
// overrunning rule's tuples never reach the result and the count reads 0.
func TestEvaluateFlagsAnOverBudgetSample(t *testing.T) {
	r := overBudgetRule()
	d := &mapping.Document{Rules: []mapping.Rule{r}}
	p := mapping.Evaluate(context.Background(), d, r.Sample.Event)
	if !p.OverBudget {
		t.Fatalf("OverBudget = false; preview = %+v", p)
	}
	ok := mapping.Evaluate(context.Background(), d, map[string]any{"type": "b", "ids": []any{}})
	if ok.OverBudget {
		t.Fatal("an empty list is over budget")
	}
}

func TestBuildTestsKeepsCleanSamples(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{cleanRule("A", "a"), overBudgetRule()}}
	tests := mapping.BuildTests(context.Background(), d)
	if len(tests) != 1 || tests[0].Name != "A" {
		t.Fatalf("tests = %+v, want A's only", tests)
	}
}

// Two rules sharing one bad sample are one sample: one problem, filed under the
// first rule.
func TestCheckSamplesFilesASharedSampleUnderTheFirstRule(t *testing.T) {
	a, b := cleanRule("A", "a"), cleanRule("B", "a")
	bad := mapping.Tuple{User: "user:1", Relation: "member", Object: "acme"}
	a.Tuples, b.Tuples = []mapping.Tuple{bad}, []mapping.Tuple{bad}
	d := &mapping.Document{Rules: []mapping.Rule{a, b}}
	ps := mapping.CheckSamples(context.Background(), d)
	if len(ps) != 1 || ps[0].Rule != 0 || ps[0].Section != "sample" ||
		!strings.HasPrefix(ps[0].Message, "the sample makes no test: ") {
		t.Fatalf("problems = %+v, want one, under rule 0", ps)
	}
}

// A rule whose when does not match its own sample evaluates cleanly to
// nothing, so BuildTests makes no test of it. CheckSamples said nothing about
// that, so the hub showed no mark and the save dialog no line. It is a warning:
// the file is still worth saving. Iterator tuples and filters count as output
// the sample was meant to exercise, the same as the rule's own tuples.
func TestCheckSamplesWarnsWhenTheSampleMakesNoTest(t *testing.T) {
	iter := cleanRule("A", "a")
	iter.Tuples = nil
	iter.Sample.Event["ids"] = []any{map[string]any{"id": "u1"}}
	iter.Iterator = &mapping.Iterator{Source: "input.ids", As: "it", Tuples: []mapping.Tuple{
		{User: "user:{{ it.id }}", Relation: "member", Object: "organization:1"},
	}}
	filter := cleanRule("A", "a")
	filter.Tuples = nil
	filter.Filters = []mapping.TupleFilter{{Object: "organization:1", Action: "delete"}}
	for name, r := range map[string]mapping.Rule{"tuples": cleanRule("A", "a"), "iterator": iter, "filters": filter} {
		r.When = `input.type == "other"`
		d := &mapping.Document{Rules: []mapping.Rule{r}}
		want := mapping.Problem{Rule: 0, Section: "sample", Warning: true,
			Message: "the sample makes no test: no rule produces anything for it"}
		ps := mapping.CheckSamples(context.Background(), d)
		if len(ps) != 1 || ps[0] != want {
			t.Fatalf("%s: problems = %+v, want %+v", name, ps, want)
		}
		if blocking := mapping.Blocking(ps); len(blocking) != 0 {
			t.Fatalf("%s: a sample that makes no test blocks the save: %+v", name, blocking)
		}
		if tests := mapping.BuildTests(context.Background(), d); len(tests) != 0 {
			t.Fatalf("%s: BuildTests made %d tests, want 0", name, len(tests))
		}
	}
}

// An explain-only rule writes nothing on purpose, so a sample that makes no
// test of it is not news.
func TestCheckSamplesSaysNothingOfARuleThatWritesNothing(t *testing.T) {
	empty := cleanRule("A", "a")
	empty.Tuples = nil
	bareIter := cleanRule("B", "b")
	bareIter.Tuples = nil
	bareIter.Iterator = &mapping.Iterator{Source: "input.ids", As: "it"}
	for _, r := range []mapping.Rule{empty, bareIter} {
		d := &mapping.Document{Rules: []mapping.Rule{r}}
		if ps := mapping.CheckSamples(context.Background(), d); len(ps) != 0 {
			t.Fatalf("%s: problems = %+v, want none", r.Name, ps)
		}
	}
}

// Every recipe's sample matches its own rule, so each makes exactly the test
// it made before CheckSamples learned to warn, and none draws a problem.
func TestEveryRecipeSampleMakesItsTestWithoutAProblem(t *testing.T) {
	ctx := context.Background()
	for _, ev := range auth0.Catalog() {
		r := ev.Recipe.Rule
		r.Sample = &mapping.Sample{Label: ev.Type, Event: ev.Sample}
		d := &mapping.Document{Rules: []mapping.Rule{r}}
		if ps := mapping.CheckSamples(ctx, d); len(ps) != 0 {
			t.Errorf("%s: problems = %+v, want none", ev.Type, ps)
		}
		want := 0
		if r.WritesTuples() || len(r.Filters) > 0 {
			want = 1
		}
		if got := len(mapping.BuildTests(ctx, d)); got != want {
			t.Errorf("%s: BuildTests made %d tests, want %d", ev.Type, got, want)
		}
	}
}

func testOf(name, typ string) mapping.TestCase {
	return mapping.TestCase{Name: name, Input: map[string]any{"type": typ}}
}

func ruleOn(name, typ string) mapping.Rule {
	r := cleanRule(name, typ)
	r.Sample = nil
	return r
}

func TestSamplesFromTestsTakesTheFirstFiringTest(t *testing.T) {
	d := &mapping.Document{
		Rules: []mapping.Rule{ruleOn("A", "a"), ruleOn("B", "b"), ruleOn("C", "c")},
		Tests: []mapping.TestCase{testOf("t1", "a"), testOf("t2", "b"), testOf("t3", "a")},
	}
	mapping.SamplesFromTests(context.Background(), d)
	for i, want := range []string{"test t1", "test t2"} {
		s := d.Rules[i].Sample
		if s == nil || s.Label != want {
			t.Fatalf("rule %d sample = %+v, want label %q", i, s, want)
		}
	}
	if d.Rules[2].Sample != nil {
		t.Fatalf("rule C fired for no test but has %+v", d.Rules[2].Sample)
	}
	if d.Rules[0].Name != "A" {
		t.Fatalf("a rule was renamed: %q", d.Rules[0].Name)
	}
}

func TestSamplesFromTestsMatchesRulesByPositionNotName(t *testing.T) {
	d := &mapping.Document{
		Rules: []mapping.Rule{ruleOn("same", "a"), ruleOn("same", "b")},
		Tests: []mapping.TestCase{testOf("only-b", "b")},
	}
	mapping.SamplesFromTests(context.Background(), d)
	if d.Rules[0].Sample != nil {
		t.Fatalf("the first rule did not fire but has %+v", d.Rules[0].Sample)
	}
	if s := d.Rules[1].Sample; s == nil || s.Label != "test only-b" {
		t.Fatalf("second rule sample = %+v", s)
	}
}

func TestSamplesFromTestsKeepsAnExistingSample(t *testing.T) {
	r := cleanRule("A", "a")
	r.Sample.Label = "mine"
	d := &mapping.Document{Rules: []mapping.Rule{r}, Tests: []mapping.TestCase{testOf("t", "a")}}
	mapping.SamplesFromTests(context.Background(), d)
	if d.Rules[0].Sample.Label != "mine" {
		t.Fatalf("sample = %+v, want the existing one", d.Rules[0].Sample)
	}
}

func TestSamplesFromTestsOnADocumentThatDoesNotCompile(t *testing.T) {
	r := ruleOn("A", "a")
	r.When = "input.type =="
	d := &mapping.Document{Rules: []mapping.Rule{r}, Tests: []mapping.TestCase{testOf("t", "a")}}
	mapping.SamplesFromTests(context.Background(), d)
	if d.Rules[0].Sample != nil {
		t.Fatalf("sample = %+v, want none", d.Rules[0].Sample)
	}
}

// Item 8: the fault was filed under the first rule carrying the sample, and said
// nothing of the rule that wrote the tuple.
func TestASharedSampleFilesFaultsUnderTheEmittingRule(t *testing.T) {
	a, b := cleanRule("A", "a"), cleanRule("B", "a")
	b.Tuples = []mapping.Tuple{{User: "user:1", Relation: "member", Object: "acme"}}
	d := &mapping.Document{Rules: []mapping.Rule{a, b}}
	ps := mapping.CheckSamples(context.Background(), d)
	want := `the sample of rule "A" makes rule "B" write a bad tuple, so it makes no test: object "acme" is not type:id`
	if len(ps) != 1 || ps[0].Rule != 1 || ps[0].Section != "sample" || ps[0].Message != want {
		t.Fatalf("problems = %+v, want one under rule 1: %s", ps, want)
	}
}

// Two samples that both run a rule whose write no filter covers file the same
// warning on it. It shows once, with the count.
func TestIdenticalWarningsShowOnce(t *testing.T) {
	x := mapping.Rule{
		Name: "X", When: `true`,
		Tuples:  []mapping.Tuple{{User: "user:1", Relation: "member", Object: "organization:1"}},
		Filters: []mapping.TupleFilter{{User: "user:2", Object: "organization:2"}},
	}
	y, z := cleanRule("Y", "y"), cleanRule("Z", "z")
	d := &mapping.Document{Rules: []mapping.Rule{x, y, z}}
	var got []mapping.Problem
	for _, p := range mapping.CheckSamples(context.Background(), d) {
		if p.Section == "filters" {
			got = append(got, p)
		}
	}
	if len(got) != 1 || got[0].Rule != 0 || !strings.HasSuffix(got[0].Message, " (2 samples)") {
		t.Fatalf("problems = %+v, want one on rule 0 ending (2 samples)", got)
	}
}
