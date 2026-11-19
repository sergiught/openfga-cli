package snapshot

import (
	"context"
	"testing"

	"github.com/openfga/mapper/language"
	"github.com/stretchr/testify/require"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

const replayMapping = `version: "1"
rules:
  - name: member
    when: input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user.user_id }}"
        relation: member
        object: "organization:{{ input.organization.id }}"
      - user: "user:{{ input.user.user_id }}"
        relation: viewer
        object: "organization:{{ input.organization.id }}"
  - name: role
    when: input.type == "snapshot.organization.member.role"
    tuple_filters:
      - user: "user:{{ input.user.user_id }}"
        object: "role:"
        action: patch
    tuples:
      - user: "user:{{ input.user.user_id }}"
        relation: assignee
        object: "role:{{ input.role.id }}"
`

func memberEvent() map[string]any {
	return map[string]any{
		"type":         "snapshot.organization.member",
		"organization": map[string]any{"id": "org_1"},
		"user":         map[string]any{"user_id": "u1"},
	}
}

func roleEvent(role string) map[string]any {
	return map[string]any{
		"type":         "snapshot.organization.member.role",
		"organization": map[string]any{"id": "org_1"},
		"user":         map[string]any{"user_id": "u1"},
		"role":         map[string]any{"id": role},
	}
}

func TestDesiredFoldsWritesAndFilters(t *testing.T) {
	m, _, err := mapping.CompileSource([]byte(replayMapping))
	require.NoError(t, err)
	got, err := Desired(context.Background(), m, []map[string]any{memberEvent(), roleEvent("A"), roleEvent("B")}, nil)
	require.NoError(t, err)
	require.Equal(t, []language.Tuple{
		{User: "user:u1", Relation: "assignee", Object: "role:B"},
		{User: "user:u1", Relation: "member", Object: "organization:org_1"},
		{User: "user:u1", Relation: "viewer", Object: "organization:org_1"},
	}, got)
}

func TestDesiredDedupes(t *testing.T) {
	m, _, err := mapping.CompileSource([]byte(replayMapping))
	require.NoError(t, err)
	got, err := Desired(context.Background(), m, []map[string]any{memberEvent(), memberEvent()}, nil)
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestDesiredErrorNamesEntity(t *testing.T) {
	m, _, err := mapping.CompileSource([]byte(replayMapping))
	require.NoError(t, err)
	ev := memberEvent()
	ev["subject"] = "organization org_1 member auth0|x"
	delete(ev, "user")
	_, err = Desired(context.Background(), m, []map[string]any{ev}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "snapshot.organization.member (organization org_1 member auth0|x): ")
}
