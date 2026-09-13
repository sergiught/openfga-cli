package mapping_test

import (
	"reflect"
	"testing"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func testModel() *openfga.AuthorizationModel {
	return &openfga.AuthorizationModel{
		ID:            "01J0",
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{
				Type:      "organization",
				Relations: map[string]openfga.Userset{"member": {}, "admin": {}},
				Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
					"member": {DirectlyRelatedUserTypes: []openfga.RelationReference{
						{Type: "user"},
						{Type: "group", Relation: "member"},
					}},
					"admin": {DirectlyRelatedUserTypes: []openfga.RelationReference{
						{Type: "user", Condition: "in_business_hours"},
						{Type: "user", Wildcard: &openfga.Wildcard{}},
					}},
				}},
			},
			{
				Type:      "group",
				Relations: map[string]openfga.Userset{"member": {}},
				Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
					"member": {DirectlyRelatedUserTypes: []openfga.RelationReference{{Type: "user"}}},
				}},
			},
		},
		Conditions: map[string]openfga.Condition{
			"in_business_hours": {
				Name:       "in_business_hours",
				Expression: "current_time < end_time",
				Parameters: map[string]openfga.ConditionParamType{
					"timezone":  {},
					"end_time":  {},
					"grace_min": {},
				},
			},
		},
	}
}

func TestIndexModelTypesAreSorted(t *testing.T) {
	ix := mapping.IndexModel(testModel())
	want := []string{"group", "organization", "user"}
	if got := ix.TypeNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("types = %v, want %v", got, want)
	}
	if ix.Empty() {
		t.Fatal("a populated model reported Empty")
	}
}

func TestIndexModelRelationsAreSorted(t *testing.T) {
	ix := mapping.IndexModel(testModel())
	want := []string{"admin", "member"}
	if got := ix.RelationsFor("organization"); !reflect.DeepEqual(got, want) {
		t.Fatalf("relations = %v, want %v", got, want)
	}
	if got := ix.RelationsFor("user"); len(got) != 0 {
		t.Fatalf("user relations = %v, want none", got)
	}
	if got := ix.RelationsFor("nope"); len(got) != 0 {
		t.Fatalf("unknown type = %v, want none", got)
	}
}

func TestIndexModelUserTypesRenderReferences(t *testing.T) {
	ix := mapping.IndexModel(testModel())

	want := []string{"group#member", "user"}
	if got := ix.UserTypesFor("organization", "member"); !reflect.DeepEqual(got, want) {
		t.Fatalf("member user types = %v, want %v", got, want)
	}
	// A wildcard renders as `user:*`; a conditioned reference keeps the plain
	// type — the condition is chosen separately on the tuple.
	wantAdmin := []string{"user", "user:*"}
	if got := ix.UserTypesFor("organization", "admin"); !reflect.DeepEqual(got, wantAdmin) {
		t.Fatalf("admin user types = %v, want %v", got, wantAdmin)
	}
}

func TestIndexModelConditions(t *testing.T) {
	ix := mapping.IndexModel(testModel())
	if got := ix.ConditionNames(); !reflect.DeepEqual(got, []string{"in_business_hours"}) {
		t.Fatalf("conditions = %v", got)
	}
	want := []string{"end_time", "grace_min", "timezone"}
	if got := ix.ConditionParams("in_business_hours"); !reflect.DeepEqual(got, want) {
		t.Fatalf("params = %v, want %v", got, want)
	}
	if got := ix.ConditionParams("nope"); len(got) != 0 {
		t.Fatalf("unknown condition = %v", got)
	}
}

// TestNilIndexIsUsable is the contract that lets the wizard hold a nil index
// for the whole session when the user skips the model step.
func TestNilIndexIsUsable(t *testing.T) {
	var ix *mapping.ModelIndex
	if !ix.Empty() {
		t.Fatal("nil index should be Empty")
	}
	if got := ix.TypeNames(); len(got) != 0 {
		t.Fatalf("TypeNames = %v", got)
	}
	if got := ix.RelationsFor("organization"); len(got) != 0 {
		t.Fatalf("RelationsFor = %v", got)
	}
	if got := ix.UserTypesFor("organization", "member"); len(got) != 0 {
		t.Fatalf("UserTypesFor = %v", got)
	}
	if got := ix.ConditionNames(); len(got) != 0 {
		t.Fatalf("ConditionNames = %v", got)
	}
	if got := ix.ConditionParams("c"); len(got) != 0 {
		t.Fatalf("ConditionParams = %v", got)
	}
}

func TestIndexModelNilModel(t *testing.T) {
	if ix := mapping.IndexModel(nil); !ix.Empty() {
		t.Fatal("nil model should index to Empty")
	}
	if ix := mapping.IndexModel(&openfga.AuthorizationModel{}); !ix.Empty() {
		t.Fatal("a model with no types should be Empty")
	}
}
