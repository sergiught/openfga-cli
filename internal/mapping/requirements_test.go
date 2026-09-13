package mapping

import (
	"testing"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func idx(t *testing.T, dsl string) *ModelIndex {
	t.Helper()
	// Built from DSL via the same path the wizard uses for --file.
	return indexFromDSL(t, dsl)
}

func indexFromDSL(t *testing.T, dsl string) *ModelIndex {
	t.Helper()
	loaded, err := modeltest.LoadModelBytes([]byte(dsl))
	if err != nil {
		t.Fatalf("load model: %v", err)
	}
	return IndexModel(loaded.SDK)
}

func TestCheckRequirementsReportsWhatTheModelLacks(t *testing.T) {
	const dsl = `model
  schema 1.1

type user

type organization
  relations
    define member: [user]
`
	ix := idx(t, dsl)

	tests := []struct {
		name string
		req  Requirement
		want RequirementStatus
	}{
		{
			name: "type and relation present, user type accepted",
			req:  Requirement{Type: "organization", Relation: "member", UserTypes: []string{"user"}},
			want: RequirementStatus{TypeOK: true, RelationOK: true, UserTypesOK: true, Checked: true},
		},
		{
			name: "type absent",
			req:  Requirement{Type: "team", Relation: "member", UserTypes: []string{"user"}},
			want: RequirementStatus{TypeOK: false, RelationOK: false, UserTypesOK: false, Checked: true},
		},
		{
			name: "relation absent on a present type",
			req:  Requirement{Type: "organization", Relation: "owner", UserTypes: []string{"user"}},
			want: RequirementStatus{TypeOK: true, RelationOK: false, UserTypesOK: false, Checked: true},
		},
		{
			name: "user type not accepted by the relation",
			req:  Requirement{Type: "organization", Relation: "member", UserTypes: []string{"group"}},
			want: RequirementStatus{TypeOK: true, RelationOK: true, UserTypesOK: false, Checked: true},
		},
		{
			name: "type only, no relation asked about",
			req:  Requirement{Type: "organization"},
			want: RequirementStatus{TypeOK: true, RelationOK: true, UserTypesOK: true, Checked: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckRequirements(ix, []Requirement{tc.req})
			if len(got) != 1 {
				t.Fatalf("got %d statuses, want 1", len(got))
			}
			g := got[0]
			if g.TypeOK != tc.want.TypeOK || g.RelationOK != tc.want.RelationOK ||
				g.UserTypesOK != tc.want.UserTypesOK || g.Checked != tc.want.Checked {
				t.Fatalf("got %+v, want %+v", g, tc.want)
			}
		})
	}
}

// A nil index means the user skipped loading a model. Requirements are then
// reported without pass/fail marks rather than as failures.
func TestCheckRequirementsWithNoModelReportsUnchecked(t *testing.T) {
	got := CheckRequirements(nil, []Requirement{{Type: "organization", Relation: "member"}})
	if len(got) != 1 {
		t.Fatalf("got %d statuses, want 1", len(got))
	}
	if got[0].Checked {
		t.Fatalf("a nil index must not report a checked status: %+v", got[0])
	}
	if got[0].Satisfied() {
		t.Fatal("an unchecked status must not claim to be satisfied")
	}
}
