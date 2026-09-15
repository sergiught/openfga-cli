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

func TestEvaluateSurfacesRenderFailureAsEvalErr(t *testing.T) {
	d := memberAddedDoc()
	// Invalid UTF-8 makes Marshal itself fail (yaml: cannot marshal invalid
	// UTF-8 data as !!str) before mapper ever sees any YAML, so this exercises
	// the render-failure branch of Evaluate rather than a compile diagnostic.
	d.Rules[0].Name = "bad\xffname"
	p := mapping.Evaluate(context.Background(), d, memberAddedEvent())
	if p.OK() {
		t.Fatal("expected a document that fails to render to not be OK")
	}
	if p.EvalErr == nil {
		t.Fatalf("expected the render failure to surface as EvalErr, got diagnostics=%v", p.Diagnostics)
	}
	if len(p.Diagnostics) != 0 {
		t.Fatalf("a render failure carries no mapper diagnostics, got %v", p.Diagnostics)
	}
}

func TestTupleCountAddsFilterTuplesToTheRulesOwn(t *testing.T) {
	d := memberAddedDoc()
	// A patch filter routes the rule's tuples into the filter operation instead
	// of the top-level list, so a count that read only Tuples would report none.
	d.Rules[0].Filters = []mapping.TupleFilter{{Object: "organization:"}}
	p := mapping.Evaluate(context.Background(), d, memberAddedEvent())
	if !p.OK() {
		t.Fatalf("not OK: %v %v", p.Diagnostics, p.EvalErr)
	}
	if len(p.Tuples) != 0 {
		t.Fatalf("a filtered rule keeps its tuples on the operation, got %d loose", len(p.Tuples))
	}
	if got := p.TupleCount(); got != 1 {
		t.Fatalf("TupleCount = %d, want 1", got)
	}
}

func TestTupleCountIsMapperTupleBudget(t *testing.T) {
	p := mapping.Evaluate(context.Background(), memberAddedDoc(), memberAddedEvent())
	if got := p.TupleCount(); got != len(p.Tuples) {
		t.Fatalf("TupleCount = %d, want %d", got, len(p.Tuples))
	}
}

func TestEvaluatedSeparatesNoTuplesFromNothingRun(t *testing.T) {
	p := mapping.Evaluate(context.Background(), memberAddedDoc(), memberAddedEvent())
	if !p.Evaluated {
		t.Fatal("a sample that ran should be marked evaluated")
	}

	if p := mapping.Evaluate(context.Background(), memberAddedDoc(), nil); p.Evaluated {
		t.Fatal("no sample means nothing was evaluated")
	}

	// A document that does not compile never reaches evaluation, and its zero
	// tuples are the absence of a measurement rather than one.
	d := memberAddedDoc()
	d.Rules[0].Name = ""
	p = mapping.Evaluate(context.Background(), d, memberAddedEvent())
	if p.OK() {
		t.Fatal("expected a nameless rule to fail to compile")
	}
	if p.Evaluated {
		t.Fatal("a document that failed to compile was never evaluated")
	}
}
