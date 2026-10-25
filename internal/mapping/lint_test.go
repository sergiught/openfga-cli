package mapping_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func problemsMatching(ps []mapping.Problem, substr string) []mapping.Problem {
	var out []mapping.Problem
	for _, p := range ps {
		if strings.Contains(p.Message, substr) {
			out = append(out, p)
		}
	}
	return out
}

func TestLintEmptyDocumentBlocks(t *testing.T) {
	ps := mapping.Lint(&mapping.Document{}, nil)
	if len(mapping.Blocking(ps)) == 0 {
		t.Fatalf("expected a blocking problem, got %+v", ps)
	}
	if len(problemsMatching(ps, "at least one rule")) == 0 {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestLintRuleWithoutTuplesBlocks(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{Name: "r", When: "true"}}}
	ps := mapping.Lint(d, nil)
	got := problemsMatching(ps, "no tuples")
	if len(got) != 1 || got[0].Warning {
		t.Fatalf("problems = %+v", ps)
	}
	if got[0].Rule != 0 || got[0].Section != "tuple" {
		t.Fatalf("problem = %+v", got[0])
	}
}

func TestLintIteratorTuplesSatisfyTheTupleRequirement(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r",
		Iterator: &mapping.Iterator{
			Source: "input.data.object.identities", As: "identity",
			Tuples: []mapping.Tuple{{User: "user:1", Relation: "r", Object: "doc:1"}},
		},
	}}}
	if got := problemsMatching(mapping.Lint(d, nil), "no tuples"); len(got) != 0 {
		t.Fatalf("iterator tuples should count: %+v", got)
	}
}

func TestLintMissingNameAndDuplicateNamesBlock(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{
		{Name: "", Tuples: []mapping.Tuple{{User: "user:1", Relation: "r", Object: "doc:1"}}},
		{Name: "dup", Tuples: []mapping.Tuple{{User: "user:1", Relation: "r", Object: "doc:1"}}},
		{Name: "dup", Tuples: []mapping.Tuple{{User: "user:1", Relation: "r", Object: "doc:1"}}},
	}}
	ps := mapping.Lint(d, nil)
	if len(problemsMatching(ps, "needs a name")) != 1 {
		t.Fatalf("expected one unnamed-rule problem: %+v", ps)
	}
	dups := problemsMatching(ps, "duplicate")
	if len(dups) != 1 || dups[0].Rule != 2 || dups[0].Warning {
		t.Fatalf("expected one blocking duplicate on rule 2: %+v", ps)
	}
}

func TestLintIncompleteTupleBlocks(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name:   "r",
		Tuples: []mapping.Tuple{{User: "user:1", Object: "doc:1"}},
	}}}
	got := problemsMatching(mapping.Lint(d, nil), "relation")
	if len(got) == 0 || got[0].Warning {
		t.Fatalf("expected a blocking missing-relation problem: %+v", got)
	}
}

func TestLintRuleActionWithTupleActionBlocks(t *testing.T) {
	// The language forbids setting both; mapper would reject it, but catching it
	// here lets the section editor mark itself red before a compile runs.
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name:   "r",
		Action: "write",
		Tuples: []mapping.Tuple{{User: "user:1", Relation: "r", Object: "doc:1", Action: "delete"}},
	}}}
	got := problemsMatching(mapping.Lint(d, nil), "rule-level action")
	if len(got) == 0 || got[0].Warning {
		t.Fatalf("expected a blocking action conflict: %+v", mapping.Lint(d, nil))
	}
}

func TestLintUnknownTypeAndRelationWarn(t *testing.T) {
	ix := mapping.IndexModel(testModel())
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r",
		Tuples: []mapping.Tuple{
			{User: "user:1", Relation: "member", Object: "widget:{{ input.id }}"},
			{User: "user:1", Relation: "owner", Object: "organization:{{ input.id }}"},
		},
	}}}
	ps := mapping.Lint(d, ix)

	typ := problemsMatching(ps, `type "widget"`)
	if len(typ) != 1 || !typ[0].Warning {
		t.Fatalf("expected one unknown-type warning: %+v", ps)
	}
	rel := problemsMatching(ps, `relation "owner"`)
	if len(rel) != 1 || !rel[0].Warning {
		t.Fatalf("expected one unknown-relation warning: %+v", ps)
	}
	if len(mapping.Blocking(ps)) != 0 {
		t.Fatalf("model mismatches must not block: %+v", mapping.Blocking(ps))
	}
}

func TestLintTemplatedTypeIsNotCheckedAgainstTheModel(t *testing.T) {
	// `{{ … }}:{{ … }}` cannot be resolved statically, so it must not be flagged.
	ix := mapping.IndexModel(testModel())
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r",
		Tuples: []mapping.Tuple{{
			User: "user:1", Relation: "{{ lower(input.role) }}", Object: "{{ input.kind }}:{{ input.id }}",
		}},
	}}}
	if ps := mapping.Lint(d, ix); len(ps) != 0 {
		t.Fatalf("templated identifiers should not be linted: %+v", ps)
	}
}

func TestLintWithoutAModelSkipsModelChecks(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name:   "r",
		Tuples: []mapping.Tuple{{User: "user:1", Relation: "nope", Object: "widget:1"}},
	}}}
	if ps := mapping.Lint(d, nil); len(ps) != 0 {
		t.Fatalf("no model means no model checks: %+v", ps)
	}
}

func TestLintUnknownConditionAndParams(t *testing.T) {
	ix := mapping.IndexModel(testModel())
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r",
		Tuples: []mapping.Tuple{{
			User: "user:1", Relation: "member", Object: "organization:1",
			Condition: "no_such_condition",
		}, {
			User: "user:1", Relation: "member", Object: "organization:1",
			Condition: "in_business_hours",
			Context: []mapping.ContextEntry{
				{Key: "timezone", Template: "UTC"},
				{Key: "bogus", Template: "x"},
			},
		}},
	}}}
	ps := mapping.Lint(d, ix)
	if got := problemsMatching(ps, `condition "no_such_condition"`); len(got) != 1 || !got[0].Warning {
		t.Fatalf("expected an unknown-condition warning: %+v", ps)
	}
	// end_time and grace_min are unset.
	if got := problemsMatching(ps, "end_time"); len(got) != 1 || !got[0].Warning {
		t.Fatalf("expected a missing-parameter warning: %+v", ps)
	}
	// "bogus" is not a declared parameter of in_business_hours.
	if got := problemsMatching(ps, `has no parameter "bogus"`); len(got) != 1 || !got[0].Warning {
		t.Fatalf("expected an unknown-context-key warning: %+v", ps)
	}
}

func TestLintUserTypeNotDirectlyRelatedWarns(t *testing.T) {
	ix := mapping.IndexModel(testModel())
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r",
		Tuples: []mapping.Tuple{
			{User: "user:1", Relation: "member", Object: "organization:1"},
			{User: "group:eng", Relation: "member", Object: "organization:1"},
			{User: "{{ input.kind }}:1", Relation: "member", Object: "organization:1"},
			// A templated relation half is only known at evaluation time, same as
			// a templated type half — it must not be flagged either.
			{User: "group:eng#{{ input.rel }}", Relation: "member", Object: "organization:1"},
		},
	}}}
	ps := mapping.Lint(d, ix)

	if len(ps) != 1 {
		t.Fatalf("expected exactly one problem: %+v", ps)
	}
	if !strings.Contains(ps[0].Message, `does not accept user type "group"`) || !ps[0].Warning {
		t.Fatalf("expected the unrelated-user-type warning for the plain group reference: %+v", ps[0])
	}
}

func TestLintIteratorNeedsSourceAndAlias(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name:     "r",
		Iterator: &mapping.Iterator{Tuples: []mapping.Tuple{{User: "u:1", Relation: "r", Object: "d:1"}}},
	}}}
	ps := mapping.Lint(d, nil)
	if len(problemsMatching(ps, "iterator needs a source")) != 1 {
		t.Fatalf("problems = %+v", ps)
	}
	if len(problemsMatching(ps, "iterator needs an alias")) != 1 {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestLintVariableNeedsNameAndExpression(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name:      "r",
		Variables: []mapping.Variable{{Name: "", Expr: "1"}, {Name: "ok", Expr: ""}},
		Tuples:    []mapping.Tuple{{User: "u:1", Relation: "r", Object: "d:1"}},
	}}}
	ps := mapping.Lint(d, nil)
	if len(mapping.Blocking(ps)) != 2 {
		t.Fatalf("expected two blocking variable problems: %+v", ps)
	}
}

func TestLintFilterNeedsAnObject(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name:    "r",
		Filters: []mapping.TupleFilter{{User: "user:1"}},
		Tuples:  []mapping.Tuple{{User: "u:1", Relation: "r", Object: "d:1"}},
	}}}
	if got := problemsMatching(mapping.Lint(d, nil), "filter needs an object"); len(got) != 1 {
		t.Fatalf("problems = %+v", mapping.Lint(d, nil))
	}
}

func TestLintCleanDocumentHasNoProblems(t *testing.T) {
	ix := mapping.IndexModel(testModel())
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "member-added",
		When: `input.type == "organization.member.added"`,
		Tuples: []mapping.Tuple{{
			User:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
			Relation: "member",
			Object:   "organization:{{ input.data.object.organization.id }}",
		}},
	}}}
	if ps := mapping.Lint(d, ix); len(ps) != 0 {
		t.Fatalf("expected a clean document: %+v", ps)
	}
}

// A rule that only deletes tuples has no tuples of its own. mapper compiles
// such a rule, so the workspace must let it be saved: this is the entire shape of
// the four Auth0 deletion recipes.
func TestAFilterOnlyRuleIsNotMissingTuples(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name:    "organization.deleted",
		When:    `input.type == "organization.deleted"`,
		Filters: []mapping.TupleFilter{{Object: "organization:{{ input.data.object.id }}", Action: "delete"}},
	}}}

	for _, p := range mapping.Blocking(mapping.Lint(d, nil)) {
		t.Errorf("blocking problem on a valid filter-only rule: [%s/%s] %s", p.Section, p.Field, p.Message)
	}
}

// A rule with neither tuples nor filters still does nothing, and still says so.
func TestARuleWithNeitherTuplesNorFiltersIsFlagged(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{Name: "r", When: "true"}}}

	var found bool
	for _, p := range mapping.Lint(d, nil) {
		if p.Section == "tuple" && strings.Contains(p.Message, "no tuples") {
			found = true
		}
	}
	if !found {
		t.Fatal("an empty rule should still be flagged")
	}
}

// A filter with no action is a patch, and a patch reconciles the tuples the
// rule produced against the ones it matches. A rule that produces none has an
// empty desired state, which mapper refuses rather than reading as "delete
// everything" — and that fails the whole event. Nothing reveals that
// until the first event arrives, so the save has to.
func TestLintPatchFilterWithoutTuplesBlocks(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r", When: "true",
		Filters: []mapping.TupleFilter{{Object: "connection:"}},
	}}}
	got := problemsMatching(mapping.Lint(d, nil), "reconcile against")
	if len(got) != 1 || got[0].Warning {
		t.Fatalf("problems = %+v", mapping.Lint(d, nil))
	}
	if got[0].Section != "filters" || got[0].Field != "action" {
		t.Fatalf("problem = %+v", got[0])
	}
}

func TestLintDeleteFilterWithoutTuplesIsFine(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r", When: "true",
		Filters: []mapping.TupleFilter{{Object: "connection:", Action: "delete"}},
	}}}
	if got := problemsMatching(mapping.Lint(d, nil), "reconcile against"); len(got) != 0 {
		t.Fatalf("a delete filter needs no tuples: %+v", got)
	}
}

// The iterator's tuples are the desired state too — they are appended to the
// rule's own before mapper diffs them against the filter.
func TestLintAnIteratorSatisfiesAPatchFilter(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r", When: "true",
		Filters: []mapping.TupleFilter{{Object: "connection:"}},
		Iterator: &mapping.Iterator{
			Source: "input.data.object.identities", As: "identity",
			Tuples: []mapping.Tuple{{User: "user:1", Relation: "identity", Object: "connection:c"}},
		},
	}}}
	if got := problemsMatching(mapping.Lint(d, nil), "reconcile against"); len(got) != 0 {
		t.Fatalf("iterator tuples are a desired state: %+v", got)
	}
}

func TestLintChecksTupleFiltersAgainstTheModel(t *testing.T) {
	ix := mapping.IndexModel(testModel())
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r",
		Filters: []mapping.TupleFilter{
			{User: "user:{{ input.u }}", Relation: "memebr", Object: "organization:", Action: "delete"},
			{User: "user:{{ input.u }}", Relation: "member", Object: "orgnization:", Action: "delete"},
			{User: "widget:{{ input.u }}", Relation: "member", Object: "organization:", Action: "delete"},
			{User: "gadget:{{ input.u }}", Object: "organization:", Action: "delete"},
			// Templated parts are only known at evaluation time.
			{User: "{{ input.kind }}:1", Relation: "{{ input.rel }}", Object: "{{ input.type }}:1", Action: "delete"},
			{User: "group:eng#member", Relation: "member", Object: "organization:{{ input.org }}", Action: "delete"},
		},
	}}}
	ps := mapping.Lint(d, ix)
	want := []string{
		`type "organization" has no relation "memebr"`,
		`the model has no type "orgnization"`,
		`relation "member" on type "organization" does not accept user type "widget"`,
		`the model has no type "gadget"`,
	}
	if len(ps) != len(want) {
		t.Fatalf("problems = %+v", ps)
	}
	for i, p := range ps {
		if p.Message != want[i] || !p.Warning || p.Section != "filters" || p.Rule != 0 {
			t.Errorf("problem %d = %+v, want %q", i, p, want[i])
		}
	}
}

// Each structural rule mapper enforces at compile time has a Lint check, so the
// workspace reports it on the row where it is fixed instead of only as a raw
// diagnostic in the preview. Every case is checked against mapper too: a check
// that drifts from what mapper rejects fails here.
func TestLintCatchesWhatMapperRejects(t *testing.T) {
	tup := func(action string) mapping.Tuple {
		return mapping.Tuple{User: "user:{{ input.id }}", Relation: "member", Object: "group:g", Action: action}
	}
	tests := []struct {
		name           string
		rule           mapping.Rule
		section, field string
	}{
		{"a filter fixing all three fields", mapping.Rule{
			Filters: []mapping.TupleFilter{{User: "user:alice", Relation: "viewer", Object: "document:roadmap", Action: "delete"}},
		}, "filters", ""},
		{"delete filters beside tuples", mapping.Rule{
			Filters: []mapping.TupleFilter{{Object: "group:", User: "user:{{ input.id }}", Action: "delete"}},
			Tuples:  []mapping.Tuple{tup("")},
		}, "filters", "action"},
		{"a patch filter with a tuple that deletes", mapping.Rule{
			Filters: []mapping.TupleFilter{{Object: "group:g", Relation: "member"}},
			Tuples:  []mapping.Tuple{tup(""), tup("delete")},
		}, "tuple", "action"},
		{"an explicit patch filter with a tuple that deletes", mapping.Rule{
			Filters: []mapping.TupleFilter{{Object: "group:g", Relation: "member", Action: "patch"}},
			Tuples:  []mapping.Tuple{tup("delete")},
		}, "tuple", "action"},
		{"a patch filter on a rule that deletes", mapping.Rule{
			Action:  "delete",
			Filters: []mapping.TupleFilter{{Object: "group:g", Relation: "member"}},
			Tuples:  []mapping.Tuple{tup("")},
		}, "action", ""},
		{"an iterator alias of input", mapping.Rule{
			Iterator: &mapping.Iterator{Source: "input.ids", As: "input", Tuples: []mapping.Tuple{tup("")}},
		}, "iterator", "as"},
		{"an iterator alias of variables", mapping.Rule{
			Iterator: &mapping.Iterator{Source: "input.ids", As: "variables", Tuples: []mapping.Tuple{tup("")}},
		}, "iterator", "as"},
		{"a trigger reading a variable", mapping.Rule{
			When:      `variables.uid != ""`,
			Variables: []mapping.Variable{{Name: "uid", Expr: "input.id"}},
			Tuples:    []mapping.Tuple{tup("")},
		}, "trigger", "when"},
		{"a trigger indexing the variables", mapping.Rule{
			When:      `variables["uid"] != ""`,
			Variables: []mapping.Variable{{Name: "uid", Expr: "input.id"}},
			Tuples:    []mapping.Tuple{tup("")},
		}, "trigger", "when"},
		{"a variable name mapper cannot parse", mapping.Rule{
			Variables: []mapping.Variable{{Name: "örg", Expr: "input.id"}},
			Tuples:    []mapping.Tuple{tup("")},
		}, "variables", "name"},
		{"two variables with one name", mapping.Rule{
			Variables: []mapping.Variable{{Name: "uid", Expr: "input.id"}, {Name: "uid", Expr: "input.other"}},
			Tuples:    []mapping.Tuple{tup("")},
		}, "variables", "name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.rule.Name = "r"
			d := &mapping.Document{Rules: []mapping.Rule{tc.rule}}
			if _, _, diags, err := mapping.Compile(d); err == nil && len(diags) == 0 {
				t.Fatalf("mapper accepts this, so Lint must not block it")
			}
			var found bool
			for _, p := range mapping.Blocking(mapping.Lint(d, nil)) {
				if p.Section == tc.section && p.Field == tc.field {
					found = true
				}
			}
			if !found {
				t.Fatalf("no blocking problem on %s/%s: %+v", tc.section, tc.field, mapping.Lint(d, nil))
			}
		})
	}
}

// The near misses of the checks above compile, so Lint must leave them alone.
func TestLintLeavesTheValidNeighboursAlone(t *testing.T) {
	tup := mapping.Tuple{User: "user:{{ input.id }}", Relation: "member", Object: "group:g"}
	tests := []struct {
		name string
		rule mapping.Rule
	}{
		{"all three fixed, but the object is a type", mapping.Rule{
			Filters: []mapping.TupleFilter{{User: "user:alice", Relation: "viewer", Object: "document:", Action: "delete"}},
		}},
		{"all three set, one templated", mapping.Rule{
			Filters: []mapping.TupleFilter{{User: "user:{{ input.id }}", Relation: "viewer", Object: "document:d", Action: "delete"}},
		}},
		{"a patch and a delete filter beside tuples", mapping.Rule{
			Filters: []mapping.TupleFilter{{Object: "group:g", Relation: "member"}, {Object: "group:h", Action: "delete"}},
			Tuples:  []mapping.Tuple{tup},
		}},
		{"a delete tuple without a patch filter", mapping.Rule{
			Tuples: []mapping.Tuple{{User: tup.User, Relation: tup.Relation, Object: tup.Object, Action: "delete"}},
		}},
		{"a trigger naming variables only inside a string", mapping.Rule{
			When:   `input.type == "variables.changed"`,
			Tuples: []mapping.Tuple{tup},
		}},
		{"a field called variables on the event", mapping.Rule{
			When:   `input.variables.x != ""`,
			Tuples: []mapping.Tuple{tup},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.rule.Name = "r"
			d := &mapping.Document{Rules: []mapping.Rule{tc.rule}}
			if _, _, diags, err := mapping.Compile(d); err != nil || len(diags) > 0 {
				t.Fatalf("mapper rejects this (%v %v), so the case is not a near miss", err, diags)
			}
			if got := mapping.Blocking(mapping.Lint(d, nil)); len(got) > 0 {
				t.Fatalf("Lint blocks a document mapper accepts: %+v", got)
			}
		})
	}
}

func TestMissingTypesSkipsTemplates(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{
		{Tuples: []mapping.Tuple{
			{User: "user:1", Relation: "member", Object: "team:1"},
			{User: "{{ input.t }}:1", Relation: "member", Object: "{{ input.o }}:1"},
			{User: "group:eng#member", Relation: "member", Object: "folder:1"},
		}},
		{Iterator: &mapping.Iterator{Tuples: []mapping.Tuple{{User: "robot:*", Relation: "member", Object: "team:{{ it }}"}}}},
	}}
	ix := &mapping.ModelIndex{Types: map[string][]string{"user": nil, "team": nil}}
	got := mapping.MissingTypes(d, ix)
	if want := []string{"folder", "group", "robot"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing = %v, want %v", got, want)
	}
	if got := mapping.MissingTypes(d, nil); got != nil {
		t.Fatalf("no model: missing = %v, want none", got)
	}
}
