package mapping_test

import (
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
		},
	}}}
	ps := mapping.Lint(d, ix)

	if got := problemsMatching(ps, `does not accept user type "user"`); len(got) != 0 {
		t.Fatalf("an accepted user type should not warn: %+v", ps)
	}
	got := problemsMatching(ps, `does not accept user type "group"`)
	if len(got) != 1 || !got[0].Warning {
		t.Fatalf("expected one unrelated-user-type warning: %+v", ps)
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
