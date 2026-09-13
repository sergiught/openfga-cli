package auth0

import (
	"context"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// modelFor builds an index from the DSL a recipe declares it needs, so the
// recipe is linted against its own stated assumptions.
func modelFor(t *testing.T, r Recipe) *mapping.ModelIndex {
	t.Helper()
	var b strings.Builder
	b.WriteString("model\n  schema 1.1\n\n")
	for _, req := range r.Requires {
		b.WriteString(req.DSL)
		b.WriteString("\n\n")
	}
	loaded, err := modeltest.LoadModelBytes([]byte(b.String()))
	if err != nil {
		t.Fatalf("recipe DSL does not parse: %v\n%s", err, b.String())
	}
	return mapping.IndexModel(loaded.SDK)
}

// Every recipe must lint clean against the model it claims to need, and must
// actually produce something when evaluated against the very payload it was
// written for. This is what makes twelve hand-authored recipes maintainable:
// a recipe that contradicts itself cannot be committed.
func TestEveryRecipeLintsAndEvaluatesAgainstItsOwnEvent(t *testing.T) {
	for _, e := range Catalog() {
		if !e.Recipe.Maps() {
			continue
		}
		t.Run(e.Type, func(t *testing.T) {
			doc := &mapping.Document{Rules: []mapping.Rule{e.Recipe.Rule}}

			for _, p := range mapping.Lint(doc, modelFor(t, e.Recipe)) {
				t.Errorf("lint: [%s/%s] %s", p.Section, p.Field, p.Message)
			}

			p := mapping.Evaluate(context.Background(), doc, e.Sample)
			if p.EvalErr != nil {
				t.Fatalf("evaluate: %v", p.EvalErr)
			}
			for _, d := range p.Diagnostics {
				t.Errorf("diagnostic: %s", d.Message)
			}
			if len(p.Tuples) == 0 && len(p.Filters) == 0 {
				t.Fatal("recipe produced neither a tuple nor a filter from its own sample")
			}
		})
	}
}

// Every event is classified exactly once. A sample added later fails here until
// someone decides what it means.
func TestEveryEventIsClassified(t *testing.T) {
	// Events that deliberately map to nothing, with the reason they do.
	noMapping := map[string]bool{
		// An object needs no tuple to exist in FGA, only to be related.
		"user.created": true, "organization.created": true,
		"group.created": true, "connection.created": true,
		// Attribute changes are not relationship changes.
		"user.updated": true, "organization.updated": true,
		"group.updated": true, "connection.updated": true,
		"organization.connection.updated": true,
	}

	var mapped, empty int
	for _, e := range Catalog() {
		if e.Recipe.Explain == "" {
			t.Errorf("%s has no explanation", e.Type)
		}
		switch {
		case e.Recipe.Maps():
			mapped++
			if noMapping[e.Type] {
				t.Errorf("%s is listed as no-mapping but carries a rule", e.Type)
			}
		default:
			empty++
			if !noMapping[e.Type] {
				t.Errorf("%s has no rule and is not listed as no-mapping", e.Type)
			}
		}
	}
	if mapped != 12 {
		t.Errorf("got %d recipes, want 12", mapped)
	}
	if empty != 9 {
		t.Errorf("got %d explained-empties, want 9", empty)
	}
}
