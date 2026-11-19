package snapshot

import (
	"reflect"
	"slices"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

var testTypes = []Type{
	{Name: "snapshot.organization.member", Mirrors: "organization.member.added"},
	{Name: "snapshot.organization.member.role", Mirrors: "organization.member.role.assigned"},
}

func whenDoc(whens ...string) *mapping.Document {
	doc := &mapping.Document{}
	for _, w := range whens {
		doc.Rules = append(doc.Rules, mapping.Rule{Name: w, When: w})
	}
	return doc
}

func TestRequested(t *testing.T) {
	doc := whenDoc(
		`input.type == "a" || input.type == "snapshot.organization.member"`,
		`input.type in ['x', 'snapshot.organization.member.role']`,
	)
	if got, want := Requested(doc, testTypes), []string{"snapshot.organization.member", "snapshot.organization.member.role"}; !slices.Equal(got, want) {
		t.Errorf("Requested() = %q, want %q", got, want)
	}

	onlyRole := whenDoc("input.type == `snapshot.organization.member.role`")
	if got, want := Requested(onlyRole, testTypes), []string{"snapshot.organization.member.role"}; !slices.Equal(got, want) {
		t.Errorf("Requested(onlyRole) = %q, want %q", got, want)
	}
}

func TestUnknownSnapshotTypes(t *testing.T) {
	doc := whenDoc(`input.type == "snapshot.organisation.member"`)
	if got, want := Unknown(doc, testTypes), []string{"snapshot.organisation.member"}; !slices.Equal(got, want) {
		t.Errorf("Unknown() = %q, want %q", got, want)
	}
	if got := Requested(doc, testTypes); len(got) != 0 {
		t.Errorf("Requested() = %q, want none", got)
	}
}

func TestUnknownIgnoresPrefixLiteral(t *testing.T) {
	doc := whenDoc(`input.type.startsWith("snapshot.") || input.type.startsWith("snapshot.organization.")`)
	if got := Unknown(doc, testTypes); len(got) != 0 {
		t.Errorf("Unknown() = %q, want none", got)
	}
	if IsSnapshotRule(doc.Rules[0], testTypes) {
		t.Error("IsSnapshotRule() = true, want false")
	}

	typo := whenDoc(`input.type.startsWith("snapshot.") || input.type == "snapshot.organisation."`)
	if got, want := Unknown(typo, testTypes), []string{"snapshot.organisation."}; !slices.Equal(got, want) {
		t.Errorf("Unknown(typo) = %q, want %q", got, want)
	}
}

func TestRuleShapesMixedDeleteAndWrite(t *testing.T) {
	r := mapping.Rule{Tuples: []mapping.Tuple{
		{User: "user:{{ a }}", Relation: "member", Object: "organization:{{ b }}", Action: "delete"},
		{User: "user:{{ a }}", Relation: "owner", Object: "organization:{{ b }}"},
	}}
	shapes, unshaped := mapping.RuleShapes(r)
	if want := []mapping.Shape{{UserType: "user", Relation: "owner", ObjectType: "organization"}}; !reflect.DeepEqual(shapes, want) {
		t.Errorf("shapes = %+v, want %+v", shapes, want)
	}
	if unshaped != 0 {
		t.Errorf("unshaped = %d, want 0", unshaped)
	}
}

func TestSuggestions(t *testing.T) {
	doc := whenDoc(`input.type == "organization.member.added"`)
	wantSuggestions := []Suggestion{{
		Rule:   `input.type == "organization.member.added"`,
		Clause: `|| input.type == "snapshot.organization.member"`,
	}}
	if got := Suggestions(doc, testTypes); !reflect.DeepEqual(got, wantSuggestions) {
		t.Errorf("Suggestions() = %+v, want %+v", got, wantSuggestions)
	}

	done := whenDoc(`input.type == "organization.member.added" || input.type == "snapshot.organization.member"`)
	if got := Suggestions(done, testTypes); len(got) != 0 {
		t.Errorf("Suggestions(done) = %+v, want none", got)
	}
}

func TestCoverage(t *testing.T) {
	member := mapping.Tuple{User: "user:{{ input.data.user_id }}", Relation: "member", Object: "organization:{{ input.data.org }}"}
	owner := mapping.Tuple{User: "user:{{ input.data.user_id }}", Relation: "owner", Object: "organization:{{ input.data.org }}"}
	identity := mapping.Tuple{User: "user:{{ input.data.user_id }}", Relation: "identity", Object: "connection:{{ input.data.conn }}"}
	templated := mapping.Tuple{User: "user:{{ input.data.user_id }}", Relation: "{{ input.data.role }}", Object: "organization:{{ input.data.org }}"}

	doc := &mapping.Document{Rules: []mapping.Rule{
		{Name: "members", When: `input.type == "snapshot.organization.member"`, Tuples: []mapping.Tuple{member}},
		{Name: "gone", When: `input.type == "user.deleted"`, Action: "delete", Tuples: []mapping.Tuple{owner}},
		{Name: "identities", When: `input.type == "user.updated"`, Tuples: []mapping.Tuple{identity}},
		{Name: "roles", When: `input.type == "snapshot.organization.member.role"`, Tuples: []mapping.Tuple{templated}},
	}}

	owned, notChecked := Coverage(doc, testTypes)
	if want := []mapping.Shape{{UserType: "user", Relation: "member", ObjectType: "organization"}}; !reflect.DeepEqual(owned, want) {
		t.Errorf("owned = %+v, want %+v", owned, want)
	}
	wantNotChecked := []NotChecked{
		{
			Rule:   "identities",
			Reason: "no snapshot rule writes user identity connection",
			Shapes: []mapping.Shape{{UserType: "user", Relation: "identity", ObjectType: "connection"}},
		},
		{
			Rule:   "roles",
			Reason: "the type or relation is templated, so stale tuples cannot be told apart",
		},
	}
	if !reflect.DeepEqual(notChecked, wantNotChecked) {
		t.Errorf("notChecked = %+v, want %+v", notChecked, wantNotChecked)
	}
}
