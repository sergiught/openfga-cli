package mapping_test

import (
	"context"
	"testing"

	"github.com/openfga/mapper"

	"github.com/sergiught/openfga-cli/internal/mapping"
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
