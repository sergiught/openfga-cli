package snapshot

import (
	"testing"

	"github.com/stretchr/testify/require"

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
	require.Equal(t, []string{"snapshot.organization.member", "snapshot.organization.member.role"}, Requested(doc, testTypes))

	onlyRole := whenDoc("input.type == `snapshot.organization.member.role`")
	require.Equal(t, []string{"snapshot.organization.member.role"}, Requested(onlyRole, testTypes))
}

func TestUnknownSnapshotTypes(t *testing.T) {
	doc := whenDoc(`input.type == "snapshot.organisation.member"`)
	require.Equal(t, []string{"snapshot.organisation.member"}, Unknown(doc, testTypes))
	require.Empty(t, Requested(doc, testTypes))
}

func TestSuggestions(t *testing.T) {
	doc := whenDoc(`input.type == "organization.member.added"`)
	require.Equal(t, []Suggestion{{
		Rule:   `input.type == "organization.member.added"`,
		Clause: `|| input.type == "snapshot.organization.member"`,
	}}, Suggestions(doc, testTypes))

	done := whenDoc(`input.type == "organization.member.added" || input.type == "snapshot.organization.member"`)
	require.Empty(t, Suggestions(done, testTypes))
}

func TestCoverage(t *testing.T) {
	member := mapping.Tuple{User: "user:{{ input.data.user_id }}", Relation: "member", Object: "organization:{{ input.data.org }}"}
	identity := mapping.Tuple{User: "user:{{ input.data.user_id }}", Relation: "identity", Object: "connection:{{ input.data.conn }}"}
	templated := mapping.Tuple{User: "user:{{ input.data.user_id }}", Relation: "{{ input.data.role }}", Object: "organization:{{ input.data.org }}"}

	doc := &mapping.Document{Rules: []mapping.Rule{
		{Name: "members", When: `input.type == "snapshot.organization.member"`, Tuples: []mapping.Tuple{member}},
		{Name: "gone", When: `input.type == "user.deleted"`, Action: "delete", Tuples: []mapping.Tuple{member}},
		{Name: "identities", When: `input.type == "user.updated"`, Tuples: []mapping.Tuple{identity}},
		{Name: "roles", When: `input.type == "snapshot.organization.member.role"`, Tuples: []mapping.Tuple{templated}},
	}}

	owned, notChecked := Coverage(doc, testTypes)
	require.Equal(t, []mapping.Shape{{UserType: "user", Relation: "member", ObjectType: "organization"}}, owned)
	require.Equal(t, []NotChecked{
		{
			Rule:   "identities",
			Reason: "no snapshot rule writes user identity connection",
			Shapes: []mapping.Shape{{UserType: "user", Relation: "identity", ObjectType: "connection"}},
		},
		{
			Rule:   "roles",
			Reason: "the type or relation is templated, so stale tuples cannot be told apart",
		},
	}, notChecked)
}
