package snapshot

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
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

func TestSortTuplesOrder(t *testing.T) {
	in := []language.Tuple{
		tup("user:b", "member", "team:1"),
		tup("team:x#member", "viewer", "doc:1"),
		tup("user:a", "viewer", "doc:2"),
		withCond(tup("user:a", "member", "team:1"), "second", nil),
		tup("user:a", "viewer", "doc:1"),
		tup("user:a", "member", "team:2"),
		tup("team:x#member", "viewer", "doc:0"),
		withCond(tup("user:a", "member", "team:1"), "first", nil),
		tup("user:a", "member", "team:1"),
		tup("user:b", "viewer", "doc:1"),
		tup("user:a", "admin", "team:1"),
		tup("user:b", "member", "team:0"),
	}
	// Shapes sort as strings: team#member viewer doc, then user admin team,
	// user member team, user viewer doc. The three tuples equal in
	// user, relation and object keep their input order.
	want := []language.Tuple{
		tup("team:x#member", "viewer", "doc:0"),
		tup("team:x#member", "viewer", "doc:1"),
		tup("user:a", "admin", "team:1"),
		withCond(tup("user:a", "member", "team:1"), "second", nil),
		withCond(tup("user:a", "member", "team:1"), "first", nil),
		tup("user:a", "member", "team:1"),
		tup("user:a", "member", "team:2"),
		tup("user:b", "member", "team:0"),
		tup("user:b", "member", "team:1"),
		tup("user:a", "viewer", "doc:1"),
		tup("user:a", "viewer", "doc:2"),
		tup("user:b", "viewer", "doc:1"),
	}
	sortTuples(in)
	if !reflect.DeepEqual(in, want) {
		t.Errorf("order:\n got %+v\nwant %+v", in, want)
	}
}

func TestSortTuplesMatchesTheNaiveOrder(t *testing.T) {
	r := rand.New(rand.NewSource(7)) //nolint:gosec // a fixed seed makes the test repeatable
	users := []string{"user:a", "user:b", "user:c", "team:x#member", "org:1#admin"}
	rels := []string{"member", "viewer", "admin"}
	objs := []string{"doc:1", "doc:2", "team:1", "team:2", "org:1"}
	var in []language.Tuple
	for range 2000 {
		in = append(in, withCond(tup(users[r.Intn(len(users))], rels[r.Intn(len(rels))], objs[r.Intn(len(objs))]), fmt.Sprint(r.Intn(4)), nil))
	}
	want := append([]language.Tuple(nil), in...)
	sort.SliceStable(want, func(i, j int) bool {
		a, b := want[i], want[j]
		if sa, sb := mapping.ShapeOf(a).String(), mapping.ShapeOf(b).String(); sa != sb {
			return sa < sb
		}
		if a.User != b.User {
			return a.User < b.User
		}
		if a.Relation != b.Relation {
			return a.Relation < b.Relation
		}
		return a.Object < b.Object
	})
	sortTuples(in)
	if !reflect.DeepEqual(in, want) {
		t.Error("sortTuples order differs from the naive comparison")
	}
}
