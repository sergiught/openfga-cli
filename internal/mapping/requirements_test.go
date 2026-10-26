package mapping

import (
	"testing"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func idx(t *testing.T, dsl string) *ModelIndex {
	t.Helper()
	// Built from DSL via the same path the workspace uses for --file.
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
    define public: [user:*]
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
			name: "a wildcard-only relation does not accept a direct user",
			req:  Requirement{Type: "organization", Relation: "public", UserTypes: []string{"user"}},
			want: RequirementStatus{TypeOK: true, RelationOK: true, UserTypesOK: false, Checked: true},
		},
		{
			name: "a wildcard-only relation accepts the wildcard",
			req:  Requirement{Type: "organization", Relation: "public", UserTypes: []string{"user:*"}},
			want: RequirementStatus{TypeOK: true, RelationOK: true, UserTypesOK: true, Checked: true},
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

// [user:*] accepts only the wildcard user:*, so a concrete user written to such
// a relation is rejected by the server; [user] likewise rejects user:*.
func TestLintMatchesWildcardReferencesExactly(t *testing.T) {
	ix := idx(t, `model
  schema 1.1

type user

type organization
  relations
    define member: [user]
    define public: [user:*]
`)
	tests := []struct {
		user, relation string
		wantWarning    bool
	}{
		{"user:1", "public", true},
		{"user:*", "public", false},
		{"user:1", "member", false},
		{"user:*", "member", true},
	}
	for _, tc := range tests {
		t.Run(tc.user+" "+tc.relation, func(t *testing.T) {
			ps := lintAgainstModel(0, Tuple{User: tc.user, Relation: tc.relation, Object: "organization:acme"}, ix)
			if got := len(ps) > 0; got != tc.wantWarning {
				t.Fatalf("warned = %v, want %v: %+v", got, tc.wantWarning, ps)
			}
		})
	}
}

// A reference's condition is part of what the server accepts: [user with c]
// refuses user:1 without c, and [user] refuses it with c.
func TestModelChecksPairTheUserTypeWithItsCondition(t *testing.T) {
	ix := idx(t, `model
  schema 1.1

type user

type organization
  relations
    define member: [user]
    define gated: [user with in_hours]
    define either: [user, user with in_hours]

condition in_hours(hour: int) {
  hour >= 9
}
`)
	t.Run("lint", func(t *testing.T) {
		tests := []struct {
			relation, condition string
			wantWarning         bool
		}{
			{"gated", "", true},
			{"gated", "in_hours", false},
			{"member", "in_hours", true},
			{"member", "", false},
			{"either", "", false},
			{"either", "in_hours", false},
		}
		for _, tc := range tests {
			t.Run(tc.relation+" "+tc.condition, func(t *testing.T) {
				ps := lintAgainstModel(0, Tuple{User: "user:1", Relation: tc.relation, Object: "organization:acme",
					Condition: tc.condition}, ix)
				if got := len(ps) > 0; got != tc.wantWarning {
					t.Fatalf("warned = %v, want %v: %+v", got, tc.wantWarning, ps)
				}
			})
		}
	})
	t.Run("requirements", func(t *testing.T) {
		for relation, want := range map[string]bool{"gated": false, "either": true, "member": true} {
			got := CheckRequirements(ix, []Requirement{{Type: "organization", Relation: relation, UserTypes: []string{"user"}}})
			if got[0].UserTypesOK != want {
				t.Errorf("%s: UserTypesOK = %v, want %v", relation, got[0].UserTypesOK, want)
			}
		}
	})
}
