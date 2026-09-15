package auth0

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/language/pkg/go/transformer"
	"github.com/openfga/openfga/pkg/typesystem"
	"google.golang.org/protobuf/encoding/protojson"

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

// Parsing is a weaker claim than a server accepting. The DSL transformer checks
// syntax; it does not check that a relation after `from` is directly assignable,
// or restricted to bare concrete types, or that every rewrite names a relation
// that exists. Those are the rules a model breaks silently here and loudly at
// `ofga model write`, so the shipped model is put through the same typesystem
// the server validates with.
func TestAServerWouldAcceptTheShippedModel(t *testing.T) {
	js, err := transformer.TransformDSLToJSON(Model())
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var m openfgav1.AuthorizationModel
	if err := protojson.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Validation requires an id and never looks at which one.
	m.Id = "01HQMVAJMJMPBKN6XN7XKJ1Q2R"
	if _, err := typesystem.NewAndValidate(t.Context(), &m); err != nil {
		t.Fatalf("a server would reject the shipped model: %v", err)
	}
}

// modelHalves splits the shipped model at the marker that separates the part
// this catalog writes from the worked example the user replaces. The two halves
// are held to different standards, so the file has to say where one ends.
func modelHalves(t *testing.T) (auth0Part, examplePart string) {
	t.Helper()
	before, after, found := strings.Cut(Model(), "what you replace")
	if !found {
		t.Fatal("the model has lost the marker separating the Auth0 half from the example half")
	}
	return before, after
}

// Nothing in the Auth0 half of the shipped model is decoration. A relation no
// recipe writes is a type the user has to reason about for nothing, and this
// catalog is the only reason that half exists — so every relation in it that a
// tuple could be written to must be reachable from some recipe's requirements.
//
// Two kinds of relation are exempt by construction. A computed relation has no
// type restrictions, so no tuple is ever written to it; and one relation is the
// join the user wires up by hand, which is listed below with the reason.
func TestTheAuth0HalfOfTheModelHasNoUnusedRelations(t *testing.T) {
	required := map[string]bool{}
	for _, e := range Catalog() {
		for _, req := range e.Recipe.Requires {
			if req.Relation != "" {
				required[req.Relation] = true
			}
		}
	}
	// Auth0's event says a user was given a role; it never says what the role
	// permits. This is where the user says it, so no recipe can require it.
	required["admin"] = true

	auth0Part, _ := modelHalves(t)
	for _, line := range strings.Split(auth0Part, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "define ") {
			continue
		}
		relation, restrictions, _ := strings.Cut(strings.TrimPrefix(line, "define "), ":")
		if !strings.Contains(restrictions, "[") {
			continue // computed: nothing is ever written to it
		}
		if !required[relation] {
			t.Errorf("the model defines %q but no recipe requires it", relation)
		}
	}
}

// The example half exists to show what the Auth0 half is for, which it can only
// do by deriving a permission from it. A worked example with no computed
// relation is not an example of anything — it is another table of facts.
func TestTheExampleHalfDerivesAPermission(t *testing.T) {
	_, examplePart := modelHalves(t)
	var computed int
	for _, line := range strings.Split(examplePart, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "define ") {
			continue
		}
		if _, restrictions, _ := strings.Cut(strings.TrimPrefix(line, "define "), ":"); !strings.Contains(restrictions, "[") {
			computed++
		}
	}
	if computed == 0 {
		t.Error("the example half defines no computed relation, so it demonstrates nothing")
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
		// Maps(), not len(Rule.Tuples): a recipe can write from its iterator
		// alone, and the two that do are the only ones writing an identity — so
		// checking the rule's own tuples made user.deleted's sweep of them look
		// like a filter matching nothing.
		if !e.Recipe.Maps() {
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

// Three of the payloads in this catalog are tagged unions, and Auth0 ships one
// sample per event — so the sample shows one branch and the others go untested
// by everything above. Every bug these cases pin was live in a shipped recipe:
// a rule that read the wrong branch's field either failed the whole event or,
// worse, wrote a connection id into a user tuple and said nothing.
//
// The payloads are minimal on purpose. Only the fields a recipe reads are here,
// which is also the check that a recipe reads nothing Auth0 does not guarantee.
func TestRecipesSurviveTheBranchesTheirSamplesDoNotShow(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		payload string
		want    []string
		filters int
	}{{
		name: "a group scoped to an organization makes the group an org member",
		typ:  "group.created",
		payload: `{"type":"group.created","a0tenant":"acme","data":{"object":{
			"id":"grp_01949dad80fc7d56","name":"Engineering","created_at":"2025-02-01T12:34:56Z",
			"type":"organization","organization_id":"org_1234567890abcdef"}}}`,
		want: []string{"group:grp_01949dad80fc7d56#member member organization:org_1234567890abcdef write"},
	}, {
		name: "a group scoped to the tenant writes nothing and does not fail",
		typ:  "group.created",
		payload: `{"type":"group.created","a0tenant":"acme","data":{"object":{
			"id":"grp_01949dad80fc7d56","name":"Everyone","created_at":"2025-02-01T12:34:56Z",
			"type":"tenant"}}}`,
		want: nil,
	}, {
		name: "a connection member of a group becomes a userset, not a user",
		typ:  "group.member.added",
		payload: `{"type":"group.member.added","a0tenant":"acme","data":{"object":{
			"group":{"id":"grp_01949dad80fc7d56","type":"tenant"},
			"member":{"member_type":"connection","id":"con_kFOHQUeaCSC1Kjqz","type":"connection",
			"connection_id":"con_kFOHQUeaCSC1Kjqz"}}}}`,
		want: []string{"connection:con_kFOHQUeaCSC1Kjqz#identity member group:grp_01949dad80fc7d56 write"},
	}, {
		name: "a user with no email can still be swept",
		typ:  "user.deleted",
		payload: `{"type":"user.deleted","a0tenant":"acme","data":{"object":{
			"user_id":"auth0|507f1f77bcf86cd799439020","created_at":"2025-02-01T12:34:56Z",
			"updated_at":"2025-02-01T12:34:56Z","deleted_at":"2025-02-01T12:34:56Z","identities":[]}}}`,
		filters: 3,
	}, {
		name: "a role name with a space is an object id, never a relation",
		typ:  "organization.member.role.assigned",
		payload: `{"type":"organization.member.role.assigned","a0tenant":"acme","data":{"object":{
			"organization":{"id":"org_1234567890abcdef"},
			"user":{"user_id":"auth0|507f1f77bcf86cd799439020"},
			"role":{"id":"rol_1234567890abcdef","name":"Billing Manager"}}}}`,
		want: []string{"user:auth0|507f1f77bcf86cd799439020 assignee role:org_1234567890abcdef|rol_1234567890abcdef write"},
	}, {
		name: "an event with no data.context still finds its tenant",
		typ:  "organization.created",
		payload: `{"type":"organization.created","a0tenant":"acme","data":{"object":{
			"id":"org_1234567890abcdef","name":"acme"}}}`,
		want: []string{"tenant:acme tenant organization:org_1234567890abcdef write"},
	}}

	index := canonicalModel(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var event map[string]any
			if err := json.Unmarshal([]byte(tc.payload), &event); err != nil {
				t.Fatalf("payload: %v", err)
			}
			doc := &mapping.Document{Rules: []mapping.Rule{recipeFor(tc.typ).Rule}}
			for _, p := range mapping.Lint(doc, index) {
				t.Errorf("lint: [%s/%s] %s", p.Section, p.Field, p.Message)
			}

			p := mapping.Evaluate(context.Background(), doc, event)
			if p.EvalErr != nil {
				t.Fatalf("evaluate: %v", p.EvalErr)
			}
			for _, d := range p.Diagnostics {
				t.Fatalf("diagnostic: %s", d.Message)
			}

			var got []string
			for _, tup := range p.Tuples {
				got = append(got, fmt.Sprintf("%s %s %s %s", tup.User, tup.Relation, tup.Object, tup.Action))
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d tuples %q, want %d %q", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("tuple %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}

			var filters int
			for _, op := range p.Filters {
				filters += len(op.Filters)
			}
			if filters != tc.filters {
				t.Errorf("got %d filters, want %d", filters, tc.filters)
			}
		})
	}
}
