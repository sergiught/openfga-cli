package mapping_test

import (
	"context"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func memberAddedDoc() *mapping.Document {
	return &mapping.Document{Rules: []mapping.Rule{{
		Name: "member-added",
		When: `input.type == "organization.member.added"`,
		Tuples: []mapping.Tuple{{
			User:     "user:{{ fga_escape(input.data.object.user.user_id) }}",
			Relation: "member",
			Object:   "organization:{{ input.data.object.organization.id }}",
		}},
	}}}
}

func memberAddedEvent() map[string]any {
	return map[string]any{
		"type": "organization.member.added",
		"data": map[string]any{
			"object": map[string]any{
				"organization": map[string]any{"id": "org_1234"},
				"user":         map[string]any{"user_id": "auth0|507f"},
			},
			"context": map[string]any{"tenant": map[string]any{"id": "my-tenant"}},
		},
	}
}

func TestEvaluateProducesTuplesAndTrace(t *testing.T) {
	p := mapping.Evaluate(context.Background(), memberAddedDoc(), memberAddedEvent())
	if !p.OK() {
		t.Fatalf("not OK: %v %v", p.Diagnostics, p.EvalErr)
	}
	if len(p.Tuples) != 1 {
		t.Fatalf("tuples = %d, want 1", len(p.Tuples))
	}
	if p.Tuples[0].Object != "organization:org_1234" {
		t.Fatalf("tuple = %+v", p.Tuples[0])
	}
	if len(p.Rules) != 1 || p.Rules[0].Name != "member-added" {
		t.Fatalf("rules trace = %+v", p.Rules)
	}
	if len(p.YAML) == 0 {
		t.Fatal("expected rendered YAML")
	}
}

func TestEvaluateWithoutEventStillRendersAndCompiles(t *testing.T) {
	p := mapping.Evaluate(context.Background(), memberAddedDoc(), nil)
	if len(p.YAML) == 0 {
		t.Fatal("expected rendered YAML")
	}
	if !p.OK() {
		t.Fatalf("not OK: %v %v", p.Diagnostics, p.EvalErr)
	}
	if len(p.Tuples) != 0 {
		t.Fatalf("expected no tuples without event, got %d", len(p.Tuples))
	}
}

func TestEvaluateSurfacesCompileDiagnostics(t *testing.T) {
	d := memberAddedDoc()
	d.Rules[0].Tuples[0].User = "user:{{ input. }}"
	p := mapping.Evaluate(context.Background(), d, memberAddedEvent())
	if p.OK() {
		t.Fatal("expected broken template to fail")
	}
	if len(p.Diagnostics) == 0 {
		t.Fatal("expected diagnostics")
	}
	if len(p.YAML) == 0 {
		t.Fatal("YAML must still be returned so preview pane can show the bad line")
	}
}

func TestEvaluateSkippedRuleIsTraced(t *testing.T) {
	d := memberAddedDoc()
	d.Rules[0].When = `input.type == "user.deleted"`
	p := mapping.Evaluate(context.Background(), d, memberAddedEvent())
	if !p.OK() {
		t.Fatalf("not OK: %v %v", p.Diagnostics, p.EvalErr)
	}
	if len(p.Tuples) != 0 {
		t.Fatalf("expected no tuples, got %d", len(p.Tuples))
	}
	if len(p.Rules) != 1 || p.Rules[0].Status != "skipped" {
		t.Fatalf("rules trace = %+v", p.Rules)
	}
}

func TestEvaluateReportsTupleFilterOperations(t *testing.T) {
	d := memberAddedDoc()
	d.Rules[0].Filters = []mapping.TupleFilter{{
		Object: "organization:{{ input.data.object.organization.id }}",
		User:   "user:{{ fga_escape(input.data.object.user.user_id) }}",
		Action: "patch",
	}}
	p := mapping.Evaluate(context.Background(), d, memberAddedEvent())
	if !p.OK() {
		t.Fatalf("not OK: %v %v", p.Diagnostics, p.EvalErr)
	}
	if len(p.Filters) != 1 || len(p.Filters[0].Filters) != 1 {
		t.Fatalf("filters = %+v", p.Filters)
	}
	if p.Filters[0].Filters[0].Object != "organization:org_1234" {
		t.Fatalf("filter = %+v", p.Filters[0].Filters[0])
	}
}

func TestCompileEmptyDocument(t *testing.T) {
	_, _, _, err := mapping.Compile(&mapping.Document{})
	if err == nil {
		t.Fatal("expected a zero-rule document to fail compilation")
	}
	p := mapping.Evaluate(context.Background(), &mapping.Document{}, nil)
	if len(p.YAML) == 0 {
		t.Fatal("expected rendered YAML even for an empty document")
	}
	if p.OK() {
		t.Fatal("expected a zero-rule document to not be OK")
	}
	if len(p.Diagnostics) == 0 {
		t.Fatal("expected diagnostics for a zero-rule document")
	}
}
