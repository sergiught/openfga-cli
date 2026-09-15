package auth0

import (
	"context"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// canonicalModel indexes the model this package ships. Recipes are linted
// against it rather than against their own Requires fragments: the fragments
// are per-requirement snippets for the screen, each valid on its own but not
// concatenable — two of them naming the same type would declare it twice.
//
// Linting against the shipped model is also the stronger claim. One file has
// to satisfy every recipe at once, which is what makes it usable as a starting
// point rather than twenty-one fragments a user has to reconcile themselves.
func canonicalModel(t *testing.T) *mapping.ModelIndex {
	t.Helper()
	loaded, err := modeltest.LoadModelBytes([]byte(Model()))
	if err != nil {
		t.Fatalf("the shipped model does not parse: %v", err)
	}
	return mapping.IndexModel(loaded.SDK)
}

// Every recipe must lint clean against the shipped model, and must actually
// produce something when evaluated against the very payload it was written
// for. This is what makes twenty hand-authored recipes maintainable: a recipe
// that contradicts itself cannot be committed.
func TestEveryRecipeLintsAndEvaluatesAgainstItsOwnEvent(t *testing.T) {
	index := canonicalModel(t)
	for _, e := range Catalog() {
		if !e.Recipe.Maps() {
			continue
		}
		t.Run(e.Type, func(t *testing.T) {
			doc := &mapping.Document{Rules: []mapping.Rule{e.Recipe.Rule}}

			for _, p := range mapping.Lint(doc, index) {
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

// The requirements a recipe states are what the recipe screen shows the user,
// and the shipped model is what they get if they take the file. If the two
// disagree, the screen sends someone editing a model that already had what
// they needed — so every requirement of every recipe must be satisfied by it.
func TestTheShippedModelSatisfiesEveryRequirement(t *testing.T) {
	index := canonicalModel(t)
	for _, e := range Catalog() {
		if len(e.Recipe.Requires) == 0 {
			continue
		}
		t.Run(e.Type, func(t *testing.T) {
			for _, s := range mapping.CheckRequirements(index, e.Recipe.Requires) {
				if !s.Satisfied() {
					t.Errorf("the shipped model does not satisfy %+v", s.Requirement)
				}
			}
		})
	}
}

// Nothing in the shipped model is decoration. A relation no recipe writes is a
// type the user has to reason about for nothing, and this catalog is the only
// reason the file exists — so every relation in it must be reachable from some
// recipe's requirements.
func TestTheShippedModelHasNoUnusedRelations(t *testing.T) {
	required := map[string]bool{}
	for _, e := range Catalog() {
		for _, req := range e.Recipe.Requires {
			if req.Relation != "" {
				required[req.Type+"#"+req.Relation] = true
			}
		}
	}
	// The role recipes name their relation from the payload, so no requirement
	// can spell them out. They are the reason the model carries two example
	// role relations at all.
	required["organization#admin"] = true
	required["organization#role-name"] = true

	for _, line := range strings.Split(Model(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "define ") {
			continue
		}
		relation, _, _ := strings.Cut(strings.TrimPrefix(line, "define "), ":")
		found := false
		for key := range required {
			if _, rel, _ := strings.Cut(key, "#"); rel == relation {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the model defines %q but no recipe requires it", relation)
		}
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
		// The only event in the catalog with nothing relational in its payload.
		"organization.updated": true,
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
	if mapped != 20 {
		t.Errorf("got %d recipes, want 20", mapped)
	}
	if empty != 1 {
		t.Errorf("got %d explained-empties, want 1", empty)
	}
}
