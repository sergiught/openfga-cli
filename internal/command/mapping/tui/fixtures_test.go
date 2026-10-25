package tui

import (
	"testing"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

// memberAddedEvent is the catalog sample, inlined so the tests do not depend
// on the embedded copy.
func memberAddedEvent() map[string]any {
	return map[string]any{
		"type": "organization.member.added",
		"data": map[string]any{
			"object": map[string]any{
				"organization": map[string]any{"id": "org_1234567890abcdef"},
				"user":         map[string]any{"user_id": "auth0|507f1f77bcf86cd799439020"},
			},
		},
	}
}

// userCreatedEvent carries an array, for iterator-scope tests.
func userCreatedEvent() map[string]any {
	return map[string]any{
		"type": "user.created",
		"data": map[string]any{
			"object": map[string]any{
				"user_id": "auth0|507f1f77bcf86cd799439020",
				"identities": []any{
					map[string]any{"connection": "Username-Password-Authentication", "provider": "auth0"},
				},
			},
		},
	}
}

// recipeDoc is a one-rule document holding typ's ready-made rule and its
// catalog sample: what using the recipe in the workspace produces.
func recipeDoc(t *testing.T, typ string) *mapping.Document {
	t.Helper()
	e, ok := auth0.Lookup(typ)
	if !ok {
		t.Fatalf("no catalog event %q", typ)
	}
	r := cloneRule(e.Recipe.Rule)
	r.Sample = &mapping.Sample{Label: typ, Event: cloneEvent(e.Sample)}
	return &mapping.Document{Rules: []mapping.Rule{r}}
}

// sampleDoc is one rule with event as its sample and nothing else.
func sampleDoc(event map[string]any) *mapping.Document {
	return &mapping.Document{Rules: []mapping.Rule{{
		Name: "r", When: "true",
		Sample: &mapping.Sample{Label: "sample", Event: event},
	}}}
}

// testModel is a user type plus an organization with member and admin, both
// directly assignable to users.
func testModel() *openfga.AuthorizationModel {
	return &openfga.AuthorizationModel{
		ID: "01J0",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{
				Type:      "organization",
				Relations: map[string]openfga.Userset{"member": {}, "admin": {}},
				Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
					"member": {DirectlyRelatedUserTypes: []openfga.RelationReference{{Type: "user"}}},
					"admin":  {DirectlyRelatedUserTypes: []openfga.RelationReference{{Type: "user"}}},
				}},
			},
		},
	}
}

// conditionModel is testModel plus a condition, for the context-key tests.
func conditionModel() *openfga.AuthorizationModel {
	m := testModel()
	m.Conditions = map[string]openfga.Condition{
		"in_region": {
			Name:       "in_region",
			Expression: "params.region == \"eu\"",
			Parameters: map[string]openfga.ConditionParamType{"region": {TypeName: "TYPE_NAME_STRING"}},
		},
	}
	return m
}

// tupleValues turns a sparse field map into the dense slice the form wants.
func tupleValues(set map[tupleField]string) []string {
	v := make([]string, fieldCount)
	for f, s := range set {
		v[f] = s
	}
	return v
}

func pickerTitles(p *picker.Picker) []string {
	var out []string
	for i := 0; i < p.Len(); i++ {
		p.SetCursor(i)
		out = append(out, p.Selected().Title)
	}
	p.SetCursor(0)
	return out
}

func selectTitle(p *picker.Picker, title string) bool {
	for i := 0; i < p.Len(); i++ {
		p.SetCursor(i)
		if p.Selected().Title == title {
			return true
		}
	}
	p.SetCursor(0)
	return false
}

// tuplesDoc is sampleDoc with the member-added sample and ts as the rule's tuples.
func tuplesDoc(ts ...mapping.Tuple) *mapping.Document {
	d := sampleDoc(memberAddedEvent())
	d.Rules[0].Tuples = ts
	return d
}

// recipeDoc feeds the editor harness, which edits in place; the catalog is
// shared across every test in the process and must come out untouched.
func TestFixturesDoNotShareTheCatalog(t *testing.T) {
	const typ = "organization.member.added"
	before := recipeFor(t, typ)
	wantUser := before.Recipe.Rule.Tuples[0].User

	d := recipeDoc(t, typ)
	d.Rules[0].Tuples[0].User = "edited"
	d.Rules[0].Sample.Event["type"] = "edited"

	after := recipeFor(t, typ)
	if got := after.Recipe.Rule.Tuples[0].User; got != wantUser {
		t.Fatalf("catalog tuple user = %q, want %q", got, wantUser)
	}
	if got := after.Sample["type"]; got != typ {
		t.Fatalf("catalog sample type = %v, want %q", got, typ)
	}
}
