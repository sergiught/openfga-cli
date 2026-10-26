package mapping_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/openfga/mapper"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

func TestBuildTestsUsesEachRuleSample(t *testing.T) {
	d := memberAddedDoc()
	d.Rules[0].Sample = &mapping.Sample{Label: "organization.member.added", Event: memberAddedEvent()}

	tests := mapping.BuildTests(context.Background(), d)
	if len(tests) != 1 {
		t.Fatalf("tests = %d, want 1", len(tests))
	}
	tc := tests[0]
	if tc.Name != "member-added" {
		t.Fatalf("name = %q", tc.Name)
	}
	if len(tc.ExpectTuples) != 1 || tc.ExpectTuples[0].Object != "organization:org_1234" {
		t.Fatalf("expected tuples = %+v", tc.ExpectTuples)
	}
	if tc.Input["type"] != "organization.member.added" {
		t.Fatalf("input = %+v", tc.Input)
	}
}

func TestBuildTestsSkipsRulesWithoutASample(t *testing.T) {
	d := memberAddedDoc()
	if got := mapping.BuildTests(context.Background(), d); len(got) != 0 {
		t.Fatalf("tests = %+v, want none", got)
	}
}

func TestBuildTestsSkipsRulesWhoseSampleProducesNothing(t *testing.T) {
	// An always-passing `expect_tuples: []` test is noise, not a regression test.
	d := memberAddedDoc()
	d.Rules[0].When = `input.type == "user.deleted"`
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: memberAddedEvent()}
	if got := mapping.BuildTests(context.Background(), d); len(got) != 0 {
		t.Fatalf("tests = %+v, want none", got)
	}
}

func TestBuildTestsSkipsWhenTheDocumentDoesNotCompile(t *testing.T) {
	d := memberAddedDoc()
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: memberAddedEvent()}
	d.Rules[0].Tuples[0].User = "user:{{ input. }}"
	if got := mapping.BuildTests(context.Background(), d); len(got) != 0 {
		t.Fatalf("a broken document must not produce tests: %+v", got)
	}
}

func TestBuildTestsDeduplicatesIdenticalSamples(t *testing.T) {
	// Two rules previewed against the same event would otherwise generate two
	// identical tests under different names.
	ev := memberAddedEvent()
	d := memberAddedDoc()
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: ev}
	d.Rules = append(d.Rules, mapping.Rule{
		Name:   "member-added-mirror",
		When:   `input.type == "organization.member.added"`,
		Sample: &mapping.Sample{Label: "x", Event: ev},
		Tuples: []mapping.Tuple{{
			User:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
			Relation: "member",
			Object:   "tenant:{{ input.data.context.tenant.id }}",
		}},
	})
	tests := mapping.BuildTests(context.Background(), d)
	if len(tests) != 1 {
		t.Fatalf("tests = %d, want 1 (deduplicated)", len(tests))
	}
	// The single test must still assert the whole document's output.
	if len(tests[0].ExpectTuples) != 2 {
		t.Fatalf("expected both rules' tuples: %+v", tests[0].ExpectTuples)
	}
}

func TestBuildTestsIncludesTupleFilters(t *testing.T) {
	d := memberAddedDoc()
	d.Rules[0].Filters = []mapping.TupleFilter{{
		Object: "organization:{{ input.data.object.organization.id }}",
		Action: "patch",
	}}
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: memberAddedEvent()}
	tests := mapping.BuildTests(context.Background(), d)
	if len(tests) != 1 || len(tests[0].ExpectTupleFilters) != 1 {
		t.Fatalf("tests = %+v", tests)
	}
	if tests[0].ExpectTupleFilters[0].Object != "organization:org_1234" {
		t.Fatalf("filter = %+v", tests[0].ExpectTupleFilters[0])
	}
}

// TestGeneratedTestsPassThroughMapper closes the loop: what BuildTests writes
// must actually pass when mapper runs it.
func TestGeneratedTestsPassThroughMapper(t *testing.T) {
	d := memberAddedDoc()
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: memberAddedEvent()}
	d.Tests = mapping.BuildTests(context.Background(), d)

	src, err := mapping.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	m, err := mapper.Compile(src)
	if err != nil {
		t.Fatalf("compile:\n%s\n%v", src, err)
	}
	results := m.RunTests(context.Background())
	if len(results) != 1 {
		t.Fatalf("mapper ran %d tests, want 1", len(results))
	}
	if !results[0].Passed {
		t.Fatalf("generated test failed: %+v\n%s", results[0], src)
	}
}

// Every recipe's generated test must pass mapper's own runner: the workspace
// saves these, and `ofga mappings test` runs them.
func TestGeneratedTestsPassForEveryRecipe(t *testing.T) {
	ctx := context.Background()
	for _, ev := range auth0.Catalog() {
		r := ev.Recipe.Rule
		if len(r.Tuples) == 0 && r.Iterator == nil && len(r.Filters) == 0 {
			continue // explain-only: nothing to test
		}
		t.Run(ev.Type, func(t *testing.T) {
			r.Sample = &mapping.Sample{Label: ev.Type, Event: ev.Sample}
			doc := &mapping.Document{Rules: []mapping.Rule{r}}
			doc.Tests = mapping.BuildTests(ctx, doc)
			if len(doc.Tests) == 0 {
				t.Skip("recipe emits nothing for its sample")
			}
			src, err := mapping.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			m, err := mapper.Compile(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			for _, r := range m.RunTests(ctx) {
				if !r.Passed {
					t.Errorf("%s: expected %v, actual %v, err %v", r.Name, r.Expected, r.Actual, r.Error)
				}
			}
		})
	}
}

// syncDoc is a patch rule keeping an organization's members in step, plus a
// second tuple on its tenant that the filter may or may not cover.
func syncDoc(tenantFilter bool) *mapping.Document {
	d := memberAddedDoc()
	d.Rules[0].Filters = []mapping.TupleFilter{{
		Relation: "member", Object: "organization:{{ input.data.object.organization.id }}",
	}}
	d.Rules[0].Tuples = append(d.Rules[0].Tuples, mapping.Tuple{
		User: "tenant:t1", Relation: "tenant", Object: "organization:{{ input.data.object.organization.id }}",
	})
	if tenantFilter {
		d.Rules[0].Filters = append(d.Rules[0].Filters, mapping.TupleFilter{
			Relation: "tenant", Object: "organization:{{ input.data.object.organization.id }}",
		})
	}
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: memberAddedEvent()}
	return d
}

// A generated test for a filter rule pins that every tuple it writes stays
// under a filter: the guard against a later edit leaving a tuple the patch can
// never clean up.
func TestAGeneratedTestAssertsFilterCoverage(t *testing.T) {
	d := syncDoc(true)
	d.Tests = mapping.BuildTests(context.Background(), d)
	if len(d.Tests) != 1 || !d.Tests[0].AssertWritesCoveredByFilter {
		t.Fatalf("tests = %+v, want one asserting coverage", d.Tests)
	}
	src, err := mapping.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	m, err := mapper.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if r := m.RunTests(context.Background()); len(r) != 1 || !r[0].Passed {
		t.Fatalf("the coverage test does not pass mapper: %+v\n%s", r, src)
	}
}

// A write no filter covers is the bug the assertion exists for, so it is
// reported rather than asserted into a test that would fail on day one.
func TestAWriteNoFilterCoversIsWarnedNotAsserted(t *testing.T) {
	d := syncDoc(false)
	tests := mapping.BuildTests(context.Background(), d)
	if len(tests) != 1 || tests[0].AssertWritesCoveredByFilter {
		t.Fatalf("tests = %+v, want one without the assertion", tests)
	}
	ps := mapping.CheckSamples(context.Background(), d)
	if len(ps) != 1 || !ps[0].Warning || ps[0].Section != "filters" ||
		!strings.Contains(ps[0].Message, "tenant:t1 tenant organization:org_1234") {
		t.Fatalf("problems = %+v, want one filters warning naming the tenant tuple", ps)
	}
}

// Without filters there is nothing to cover, and the flag stays off.
func TestARuleWithoutFiltersAssertsNoCoverage(t *testing.T) {
	d := memberAddedDoc()
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: memberAddedEvent()}
	if tests := mapping.BuildTests(context.Background(), d); len(tests) != 1 || tests[0].AssertWritesCoveredByFilter {
		t.Fatalf("tests = %+v", tests)
	}
}

// NewTest asserts what the whole document produces for an event now.
func TestNewTestExpectsWhatTheMappingProduces(t *testing.T) {
	d := memberAddedDoc()
	tc, err := mapping.NewTest(context.Background(), d, "mine", memberAddedEvent())
	if err != nil {
		t.Fatal(err)
	}
	if tc.Name != "mine" || tc.Input["type"] != "organization.member.added" {
		t.Fatalf("test = %+v", tc)
	}
	if len(tc.ExpectTuples) != 1 || tc.ExpectTuples[0].Object != "organization:org_1234" {
		t.Fatalf("expected tuples = %+v", tc.ExpectTuples)
	}
	if tc.AssertWritesCoveredByFilter {
		t.Fatal("a rule without filters has no coverage to assert")
	}
}

// A filter operation's tuples and filters are both expected, and coverage is
// pinned exactly when every write falls under a filter, as for a generated test.
func TestNewTestCarriesFiltersAndCoverage(t *testing.T) {
	for _, covered := range []bool{true, false} {
		d := syncDoc(covered)
		tc, err := mapping.NewTest(context.Background(), d, "sync", memberAddedEvent())
		if err != nil {
			t.Fatal(err)
		}
		wantFilters := 1
		if covered {
			wantFilters = 2
		}
		if len(tc.ExpectTuples) != 2 || len(tc.ExpectTupleFilters) != wantFilters {
			t.Fatalf("covered=%v: test = %+v", covered, tc)
		}
		if tc.AssertWritesCoveredByFilter != covered {
			t.Fatalf("covered=%v: coverage asserted = %v", covered, tc.AssertWritesCoveredByFilter)
		}
	}
}

// An event no rule matches makes a test that expects nothing: a negative test.
func TestNewTestOfAnEventNoRuleMatchesExpectsNothing(t *testing.T) {
	tc, err := mapping.NewTest(context.Background(), memberAddedDoc(), "quiet", map[string]any{"type": "user.deleted"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tc.ExpectTuples) != 0 || len(tc.ExpectTupleFilters) != 0 || tc.AssertWritesCoveredByFilter {
		t.Fatalf("test = %+v, want one expecting nothing", tc)
	}
}

// An event the mapping fails on still makes a test, with nothing expected, so
// the caller can keep it and show why it fails.
func TestNewTestReturnsTheEvaluationErrorWithAnEmptyTest(t *testing.T) {
	ev := map[string]any{"type": "organization.member.added"}
	tc, err := mapping.NewTest(context.Background(), memberAddedDoc(), "broken", ev)
	if err == nil {
		t.Fatal("an event missing every field the rule reads evaluated cleanly")
	}
	if tc.Name != "broken" || tc.Input["type"] != "organization.member.added" ||
		len(tc.ExpectTuples) != 0 || len(tc.ExpectTupleFilters) != 0 {
		t.Fatalf("test = %+v, want the named input with nothing expected", tc)
	}
}

func TestNewTestOnADocumentThatDoesNotCompileSaysSo(t *testing.T) {
	d := memberAddedDoc()
	d.Rules[0].Tuples[0].User = "user:{{ input. }}"
	tc, err := mapping.NewTest(context.Background(), d, "x", memberAddedEvent())
	if err == nil || !strings.Contains(err.Error(), "does not compile") {
		t.Fatalf("err = %v, want the compile failure", err)
	}
	if tc.Input == nil || len(tc.ExpectTuples) != 0 {
		t.Fatalf("test = %+v", tc)
	}
}

// A tuple FGA would refuse fails NewTest the way it fails a sample, through
// the same check: a test the user adds never asserts a tuple a save would not
// generate a test for.
func TestNewTestRefusesATupleCheckTupleRejects(t *testing.T) {
	ctx := context.Background()
	d := memberAddedDoc()
	ev := memberAddedEvent()
	ev["data"].(map[string]any)["object"].(map[string]any)["organization"] = map[string]any{"id": ""}
	tc, err := mapping.NewTest(ctx, d, "no org", ev)
	if err == nil || err.Error() != `object "organization:" is not type:id` {
		t.Fatalf("err = %v, want CheckTuple's", err)
	}
	if tc.Name != "no org" || tc.Input == nil || len(tc.ExpectTuples) != 0 || len(tc.ExpectTupleFilters) != 0 {
		t.Fatalf("test = %+v, want the named input with nothing expected", tc)
	}
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: ev}
	if ps := mapping.CheckSamples(ctx, d); len(ps) != 1 || ps[0].Message != "the sample makes no test: "+err.Error() {
		t.Fatalf("CheckSamples = %+v, want the same fault", ps)
	}
}

// otherMemberEvent is memberAddedEvent for another organization: same rule,
// different input, so its test never replaces the sample's.
func otherMemberEvent() map[string]any {
	ev := memberAddedEvent()
	ev["data"] = map[string]any{"object": map[string]any{
		"organization": map[string]any{"id": "org_5678"},
		"user":         map[string]any{"user_id": "auth0|9999"},
	}}
	return ev
}

func savedNames(tests []mapping.TestCase) string {
	var out []string
	for _, tc := range tests {
		out = append(out, tc.Name)
	}
	return strings.Join(out, ",")
}

// origins is where SavedTestsAndOrigins says each generated test came from.
func origins(ctx context.Context, d *mapping.Document) map[string]int {
	_, from := mapping.SavedTestsAndOrigins(ctx, d)
	return from
}

func TestSavedTestsPutTheUsersOwnFirst(t *testing.T) {
	ctx := context.Background()
	d := memberAddedDoc()
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: memberAddedEvent()}
	own, err := mapping.NewTest(ctx, d, "other org", otherMemberEvent())
	if err != nil {
		t.Fatal(err)
	}
	d.Tests = []mapping.TestCase{own}
	if got := savedNames(mapping.SavedTests(ctx, d)); got != "other org,member-added" {
		t.Fatalf("saved = %s", got)
	}
	if from := origins(ctx, d); len(from) != 1 || from["member-added"] != 0 {
		t.Fatalf("from = %v, want the generated test traced to rule 0", from)
	}
}

// An own test on the sample's input replaces the generated one: that is how a
// generated test becomes the user's own.
func TestAnOwnTestReplacesTheGeneratedTestForItsInput(t *testing.T) {
	ctx := context.Background()
	d := memberAddedDoc()
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: memberAddedEvent()}
	d.Tests = []mapping.TestCase{{Name: "negative", Input: memberAddedEvent()}}
	saved := mapping.SavedTests(ctx, d)
	if got := savedNames(saved); got != "negative" {
		t.Fatalf("saved = %s, want only the own test", got)
	}
	if len(saved[0].ExpectTuples) != 0 {
		t.Fatalf("the own test was rewritten: %+v", saved[0])
	}
	if from := origins(ctx, d); len(from) != 0 {
		t.Fatalf("from = %v, want nothing generated", from)
	}
}

func TestAGeneratedTestNeverTakesAnOwnTestsName(t *testing.T) {
	ctx := context.Background()
	d := memberAddedDoc()
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: memberAddedEvent()}
	d.Tests = []mapping.TestCase{
		{Name: "member-added", Input: otherMemberEvent()},
		{Name: "member-added 2", Input: map[string]any{"type": "user.deleted"}},
	}
	if got := savedNames(mapping.SavedTests(ctx, d)); got != "member-added,member-added 2,member-added 3" {
		t.Fatalf("saved = %s", got)
	}
	if from := origins(ctx, d); from["member-added 3"] != 0 || len(from) != 1 {
		t.Fatalf("from = %v, want the renamed test traced to rule 0", from)
	}
}

// Two rules of the same name with different samples generate two tests, and
// the second one is renamed rather than shadowing the first.
func TestGeneratedTestsOfTwoSameNamedRulesStayApart(t *testing.T) {
	ctx := context.Background()
	d := memberAddedDoc()
	d.Rules[0].Sample = &mapping.Sample{Label: "x", Event: memberAddedEvent()}
	twin := d.Rules[0]
	twin.Sample = &mapping.Sample{Label: "y", Event: otherMemberEvent()}
	d.Rules = append(d.Rules, twin)
	if got := savedNames(mapping.SavedTests(ctx, d)); got != "member-added,member-added 2" {
		t.Fatalf("saved = %s", got)
	}
	if from := origins(ctx, d); from["member-added"] != 0 || from["member-added 2"] != 1 {
		t.Fatalf("from = %v", from)
	}
}

func TestUniqueTestNameCountsUpFromTwo(t *testing.T) {
	tests := []mapping.TestCase{{Name: "a"}, {Name: "a 2"}}
	for name, want := range map[string]string{"b": "b", "a": "a 3"} {
		if got := mapping.UniqueTestName(tests, name); got != want {
			t.Fatalf("UniqueTestName(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestSavedTestsPassThroughMapper closes the loop for what a save writes: the
// user's unedited own tests and the generated ones all pass mapper's runner.
func TestSavedTestsPassThroughMapper(t *testing.T) {
	ctx := context.Background()
	d := syncDoc(true)
	own, err := mapping.NewTest(ctx, d, "other org", otherMemberEvent())
	if err != nil {
		t.Fatal(err)
	}
	negative, err := mapping.NewTest(ctx, d, "deleted users map to nothing", map[string]any{"type": "user.deleted"})
	if err != nil {
		t.Fatal(err)
	}
	d.Tests = []mapping.TestCase{own, negative}
	d.Tests = mapping.SavedTests(ctx, d)

	src, err := mapping.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	f, err := mapping.LoadBytes("mapping.yaml", src)
	if err != nil {
		t.Fatalf("load:\n%s\n%v", src, err)
	}
	run, err := mapping.RunTests(ctx, f, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Tests) != 3 || run.Failed() != 0 {
		t.Fatalf("run = %+v\n%s", run.Tests, src)
	}
}

// shapeEvent feeds every rule shapeDocs builds: an organization, a user, a
// list to iterate, and a number and a bool for a condition's context.
func shapeEvent() map[string]any {
	return map[string]any{
		"type": "x.happened",
		"data": map[string]any{
			"org":   "acme",
			"user":  "alice",
			"items": []any{"a", "b"},
			"n":     float64(3),
			"flag":  true,
		},
	}
}

// shapeDocs is one document per shape of output an own test has to carry
// through a save: filters, iterators, condition context and both levels of
// delete.
func shapeDocs() map[string]*mapping.Document {
	w := `input.type == "x.happened"`
	org := "organization:{{ input.data.org }}"
	user := "user:{{ input.data.user }}"
	return map[string]*mapping.Document{
		"patch filter": {Rules: []mapping.Rule{{Name: "p", When: w,
			Filters: []mapping.TupleFilter{{Object: org, Relation: "member"}},
			Tuples:  []mapping.Tuple{{User: user, Relation: "member", Object: org}},
		}}},
		"delete filter only": {Rules: []mapping.Rule{{Name: "d", When: w,
			Filters: []mapping.TupleFilter{{Object: org, Action: "delete"}},
		}}},
		"iterator": {Rules: []mapping.Rule{{Name: "i", When: w,
			Iterator: &mapping.Iterator{Source: "input.data.items", As: "it",
				Tuples: []mapping.Tuple{{User: "user:{{ it }}", Relation: "viewer", Object: "doc:{{ input.data.org }}"}}},
		}}},
		"condition with context": {Rules: []mapping.Rule{{Name: "c", When: w,
			Tuples: []mapping.Tuple{{User: user, Relation: "viewer", Object: "doc:1", Condition: "in_region",
				Context: []mapping.ContextEntry{{Key: "region", Template: "{{ input.data.n }}"}, {Key: "on", Template: "{{ input.data.flag }}"}}}},
		}}},
		"rule-level delete": {Rules: []mapping.Rule{{Name: "rd", When: w, Action: "delete",
			Tuples: []mapping.Tuple{{User: user, Relation: "member", Object: org}},
		}}},
		"tuple-level delete": {Rules: []mapping.Rule{{Name: "td", When: w,
			Tuples: []mapping.Tuple{
				{User: user, Relation: "member", Object: org, Action: "delete"},
				{User: user, Relation: "owner", Object: org},
			},
		}}},
		"filtered plus unfiltered rule": {Rules: []mapping.Rule{
			{Name: "p", When: w,
				Filters: []mapping.TupleFilter{{Object: "organization:"}},
				Tuples:  []mapping.Tuple{{User: user, Relation: "member", Object: org}}},
			{Name: "u", When: w,
				Tuples: []mapping.Tuple{{User: user, Relation: "viewer", Object: "doc:1"}}},
		}},
	}
}

// An own test of every shape survives a save exactly as NewTest built it, and
// passes mapper's runner: what the screens show is what the file asserts.
func TestAnOwnTestOfEveryShapeRoundTripsAndPasses(t *testing.T) {
	ctx := context.Background()
	for name, d := range shapeDocs() {
		t.Run(name, func(t *testing.T) {
			tc, err := mapping.NewTest(ctx, d, "own", shapeEvent())
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.ExpectTuples) == 0 && len(tc.ExpectTupleFilters) == 0 {
				t.Fatalf("test = %+v, want the shape to produce something", tc)
			}
			d.Tests = []mapping.TestCase{tc}
			d.Tests = mapping.SavedTests(ctx, d)
			src, err := mapping.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			f, err := mapping.LoadBytes("mapping.yaml", src)
			if err != nil {
				t.Fatalf("load: %v\n%s", err, src)
			}
			run, err := mapping.RunTests(ctx, f, "", false)
			if err != nil {
				t.Fatalf("run: %v\n%s", err, src)
			}
			if len(run.Tests) != 1 || run.Failed() != 0 {
				t.Fatalf("run = %+v\n%s", run.Tests, src)
			}
			got := f.Doc.Tests[0]
			if len(got.ExpectTuples) != len(tc.ExpectTuples) || len(got.ExpectTupleFilters) != len(tc.ExpectTupleFilters) ||
				got.AssertWritesCoveredByFilter != tc.AssertWritesCoveredByFilter {
				t.Fatalf("the save changed the test:\nbuilt  %+v\nloaded %+v\n%s", tc, got, src)
			}
			for i := range got.ExpectTuples {
				if got.ExpectTuples[i].Key() != tc.ExpectTuples[i].Key() {
					t.Fatalf("tuple %d: built %q, loaded %q\n%s", i, tc.ExpectTuples[i].Key(), got.ExpectTuples[i].Key(), src)
				}
			}
			for i := range got.ExpectTupleFilters {
				if got.ExpectTupleFilters[i] != tc.ExpectTupleFilters[i] {
					t.Fatalf("filter %d: built %+v, loaded %+v", i, tc.ExpectTupleFilters[i], got.ExpectTupleFilters[i])
				}
			}
		})
	}
}

// A condition's context holds what mapper renders, strings, whatever the
// event's values were: the number and the bool come out as "3" and "true".
func TestAnOwnTestsContextIsWhatMapperRenders(t *testing.T) {
	tc, err := mapping.NewTest(context.Background(), shapeDocs()["condition with context"], "own", shapeEvent())
	if err != nil {
		t.Fatal(err)
	}
	if len(tc.ExpectTuples) != 1 || !reflect.DeepEqual(tc.ExpectTuples[0].Context, map[string]any{"region": "3", "on": "true"}) {
		t.Fatalf("tuples = %+v", tc.ExpectTuples)
	}
}

func TestEventsListsTestInputsThenNewSamplesOnce(t *testing.T) {
	a := map[string]any{"type": "a", "n": 1}
	b := map[string]any{"type": "b"}
	c := map[string]any{"type": "c"}
	d := &mapping.Document{
		Tests: []mapping.TestCase{
			{Name: "first", Input: a},
			{Name: "again", Input: map[string]any{"type": "a", "n": 1}}, // equal to first's, another map
			{Name: "empty"},
			{Name: "blank", Input: map[string]any{}},
		},
		Rules: []mapping.Rule{
			{Name: "ra", Sample: &mapping.Sample{Event: a}}, // already a test input
			{Name: "rb", Sample: &mapping.Sample{Event: b}},
			{Name: "rb2", Sample: &mapping.Sample{Event: b}}, // same as rb's
			{Name: "none"},
			{Sample: &mapping.Sample{Event: c}}, // unnamed
			{Name: "rblank", Sample: &mapping.Sample{Event: map[string]any{}}}, // same as blank's
		},
	}
	got := mapping.Events(d)
	want := []mapping.Event{
		{Label: "test first", Payload: a},
		{Label: "test blank", Payload: map[string]any{}},
		{Label: "rule rb sample", Payload: b},
		{Label: "rule 5 sample", Payload: c},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Events = %+v\nwant %+v", got, want)
	}
	if got := mapping.Events(&mapping.Document{}); len(got) != 0 {
		t.Fatalf("an empty document lists %+v", got)
	}
}
