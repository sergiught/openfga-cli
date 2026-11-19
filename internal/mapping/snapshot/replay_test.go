package snapshot

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/openfga/mapper/language"

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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := Desired(context.Background(), m, []map[string]any{memberEvent(), roleEvent("A"), roleEvent("B")}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []language.Tuple{
		{User: "user:u1", Relation: "assignee", Object: "role:B"},
		{User: "user:u1", Relation: "member", Object: "organization:org_1"},
		{User: "user:u1", Relation: "viewer", Object: "organization:org_1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Desired() = %+v, want %+v", got, want)
	}
}

func TestDesiredDedupes(t *testing.T) {
	m, _, err := mapping.CompileSource([]byte(replayMapping))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := Desired(context.Background(), m, []map[string]any{memberEvent(), memberEvent()}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len(Desired()) = %d, want 2", len(got))
	}
}

func TestDesiredErrorNamesEntity(t *testing.T) {
	m, _, err := mapping.CompileSource([]byte(replayMapping))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ev := memberEvent()
	ev["subject"] = "organization org_1 member auth0|x"
	delete(ev, "user")
	_, err = Desired(context.Background(), m, []map[string]any{ev}, nil)
	if err == nil {
		t.Fatal("Desired() error = nil, want error")
	}
	if want := "snapshot.organization.member (organization org_1 member auth0|x): "; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err, want)
	}
}
