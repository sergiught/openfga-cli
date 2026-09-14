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

// renderRecipe evaluates one recipe against its own sample. Templates are the
// whole point of a recipe, so the rendered values are the only ones worth
// asserting on.
func renderRecipe(t *testing.T, e Event) mapping.Preview {
	t.Helper()
	doc := &mapping.Document{Rules: []mapping.Rule{e.Recipe.Rule}}
	p := mapping.Evaluate(context.Background(), doc, e.Sample)
	if p.EvalErr != nil {
		t.Fatalf("%s: evaluate: %v", e.Type, p.EvalErr)
	}
	return p
}

// typePrefix is the type half of "type:id", or the whole string when there is
// no colon. A bare "organization:" prefix yields "organization", which is what
// makes a wildcard filter comparable with a concrete tuple.
func typePrefix(ref string) string {
	typ, _, _ := strings.Cut(ref, ":")
	return typ
}

// canMatch reports whether a filter aimed at these fields could ever select one
// of the tuples, each held as its {user type, object type} prefixes. A blank
// filter field is a wildcard in the FGA Read API, so it constrains nothing.
func canMatch(user, object string, tuples [][2]string) bool {
	for _, tp := range tuples {
		if typePrefix(object) != tp[1] {
			continue
		}
		if user != "" && typePrefix(user) != tp[0] {
			continue
		}
		return true
	}
	return false
}

// A tuple filter that names a field no tuple in this catalog uses deletes
// nothing, and nothing else notices: lint only requires a filter to have an
// object, and a rule whose filter matches nothing still evaluates cleanly. So
// every rendered filter must be capable of selecting a tuple some recipe here
// actually writes.
//
// Only type prefixes are compared. Each sample carries its own ids, so matching
// whole rendered values would fail for unrelated reasons.
func TestEveryFilterCanMatchATupleTheCatalogWrites(t *testing.T) {
	var tuples [][2]string
	for _, e := range Catalog() {
		if len(e.Recipe.Rule.Tuples) == 0 {
			continue
		}
		for _, tup := range renderRecipe(t, e).Tuples {
			tuples = append(tuples, [2]string{typePrefix(tup.User), typePrefix(tup.Object)})
		}
	}
	if len(tuples) == 0 {
		t.Fatal("no recipe rendered a tuple, so there is nothing for a filter to match")
	}

	for _, e := range Catalog() {
		if len(e.Recipe.Rule.Filters) == 0 {
			continue
		}
		t.Run(e.Type, func(t *testing.T) {
			for _, op := range renderRecipe(t, e).Filters {
				for _, f := range op.Filters {
					if !canMatch(f.User, f.Object, tuples) {
						t.Errorf("filter user=%q object=%q matches no tuple this catalog writes", f.User, f.Object)
					}
				}
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
			// The picker prints this beside the event type. Without it the row
			// says only that nothing is there, which reads as the user's model
			// falling short rather than the event carrying no relationship.
			if e.Recipe.Note == "" {
				t.Errorf("%s maps nothing and does not say why", e.Type)
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
