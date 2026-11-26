package snapshot

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

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
	got, contested, err := Desired(context.Background(), m, []map[string]any{memberEvent(), roleEvent("A"), roleEvent("B")}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The role events are of one type, so their order means nothing: event 3's
	// patch removed role:A and event 2's patch asked for role:B to be gone.
	const roleType = "snapshot.organization.member.role"
	wantContested := []Contested{
		{Tuple: tup("user:u1", "assignee", "role:A"), By: roleType},
		{Tuple: tup("user:u1", "assignee", "role:B"), By: roleType},
	}
	if !reflect.DeepEqual(contested, wantContested) {
		t.Errorf("contested = %+v, want %+v", contested, wantContested)
	}
	want := []language.Tuple{
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
	got, _, err := Desired(context.Background(), m, []map[string]any{memberEvent(), memberEvent()}, nil)
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
	_, _, err = Desired(context.Background(), m, []map[string]any{ev}, nil)
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

func replayEvents() []map[string]any {
	return []map[string]any{memberEvent(), roleEvent("A"), roleEvent("B")}
}

func newReplay(t *testing.T) *Replay {
	t.Helper()
	m, _, err := mapping.CompileSource([]byte(replayMapping))
	if err != nil {
		t.Fatal(err)
	}
	return NewReplay(m)
}

func TestReplayAddInPartsMatchesOneAdd(t *testing.T) {
	ctx := context.Background()
	events := replayEvents()
	one := newReplay(t)
	if err := one.Add(ctx, events); err != nil {
		t.Fatal(err)
	}
	parts := newReplay(t)
	for _, e := range events {
		if err := parts.Add(ctx, []map[string]any{e}); err != nil {
			t.Fatal(err)
		}
	}
	oneDone, _ := one.Done()
	partsDone, _ := parts.Done()
	if !reflect.DeepEqual(oneDone, partsDone) {
		t.Errorf("parts = %+v, one = %+v", partsDone, oneDone)
	}
	if one.Events() != 3 || parts.Events() != 3 {
		t.Errorf("Events() = %d and %d, want 3", one.Events(), parts.Events())
	}
}

func TestReplayLaterCallFilterRemovesEarlierWrite(t *testing.T) {
	ctx := context.Background()
	r := newReplay(t)
	if err := r.Add(ctx, []map[string]any{roleEvent("A")}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(ctx, []map[string]any{roleEvent("B")}); err != nil {
		t.Fatal(err)
	}
	got, contested := r.Done()
	if len(got) != 0 || len(contested) != 2 || contested[0].Tuple.Object != "role:A" || contested[1].Tuple.Object != "role:B" {
		t.Errorf("Done() = %+v and %+v, want role:A and role:B, contested", got, contested)
	}
}

func TestReplayErrorInLaterCallNamesEntity(t *testing.T) {
	ctx := context.Background()
	r := newReplay(t)
	if err := r.Add(ctx, []map[string]any{memberEvent()}); err != nil {
		t.Fatal(err)
	}
	bad := memberEvent()
	bad["subject"] = "organization org_1 member auth0|x"
	delete(bad, "user")
	err := r.Add(ctx, []map[string]any{bad})
	if err == nil {
		t.Fatal("Add() error = nil, want error")
	}
	if want := "snapshot.organization.member (organization org_1 member auth0|x): "; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err, want)
	}
}

func TestReplayCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newReplay(t).Add(ctx, replayEvents()); !errors.Is(err, context.Canceled) {
		t.Errorf("Add() error = %v, want context.Canceled", err)
	}
}

const contestMapping = `version: "1"
rules:
  - name: blocked
    when: input.type in ["user.blocked", "snapshot.user"] && input.blocked == true
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "organization:"
        action: delete
  - name: member
    when: input.type == "snapshot.organization.member" && input.drop != true
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:{{ input.org }}"
  - name: drop
    when: input.type == "snapshot.organization.member" && input.drop == true
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "organization:"
        action: delete
  - name: removed
    when: input.type == "snapshot.removed"
    action: delete
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:{{ input.org }}"
  - name: role
    when: input.type == "snapshot.role"
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "role:"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: assignee
        object: "role:{{ input.role }}"
  - name: grant
    when: input.type == "snapshot.grant"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: assignee
        object: "role:{{ input.role }}"
  - name: swap-out
    when: input.type == "snapshot.swap"
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "organization:"
        action: delete
  - name: swap-in
    when: input.type == "snapshot.swap"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:{{ input.org }}"
  - name: tick
    when: input.type == "snapshot.tick"
    tuple_filters:
      - relation: owner
        object: "organization:"
        action: delete
  - name: owner
    when: input.type == "snapshot.owner"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: owner
        object: "organization:{{ input.org }}"
`

func cev(typ, user string, kv ...any) map[string]any {
	e := map[string]any{"type": typ, "user_id": user, "org": "o1", "blocked": false}
	for i := 0; i+1 < len(kv); i += 2 {
		e[kv[i].(string)] = kv[i+1]
	}
	return e
}

func desiredOf(t *testing.T, events ...map[string]any) ([]language.Tuple, []Contested) {
	t.Helper()
	m, _, err := mapping.CompileSource([]byte(contestMapping))
	if err != nil {
		t.Fatal(err)
	}
	d, c, err := Desired(context.Background(), m, events, nil)
	if err != nil {
		t.Fatal(err)
	}
	return d, c
}

func TestDesiredContestsAWriteAfterAnEarlierDeleteFilter(t *testing.T) {
	blocked := cev("snapshot.user", "u1", "blocked", true)
	member := cev("snapshot.organization.member", "u1")
	other := cev("snapshot.organization.member", "u2")

	d, c := desiredOf(t, blocked, member, other)
	if want := []language.Tuple{tup("user:u2", "member", "organization:o1")}; !reflect.DeepEqual(d, want) {
		t.Errorf("desired = %+v, want %+v", d, want)
	}
	if want := []Contested{{Tuple: tup("user:u1", "member", "organization:o1"), By: "snapshot.user"}}; !reflect.DeepEqual(c, want) {
		t.Errorf("contested = %+v, want %+v", c, want)
	}

	d, c = desiredOf(t, blocked, member)
	if len(d) != 0 || len(c) != 1 {
		t.Errorf("desired = %+v, contested = %+v, want none and one", d, c)
	}

	d, c = desiredOf(t, member, blocked)
	if len(d) != 0 || len(c) != 0 {
		t.Errorf("later removal: desired = %+v, contested = %+v, want both empty", d, c)
	}

	d, c = desiredOf(t, cev("snapshot.user", "u1"), member)
	if len(d) != 1 || len(c) != 0 {
		t.Errorf("not blocked: desired = %+v, contested = %+v, want one and none", d, c)
	}
}

func TestDesiredContestsAWriteAfterAnEarlierDeleteTuple(t *testing.T) {
	d, c := desiredOf(t, cev("snapshot.removed", "u1"), cev("snapshot.organization.member", "u1"))
	if len(d) != 0 || len(c) != 1 || c[0].By != "snapshot.removed" {
		t.Errorf("desired = %+v, contested = %+v", d, c)
	}
}

func TestDesiredPatchFilterKeepsItsOwnTuples(t *testing.T) {
	d, c := desiredOf(t,
		cev("snapshot.role", "u1", "role", "r1"),
		cev("snapshot.grant", "u1", "role", "r1"),
		cev("snapshot.grant", "u1", "role", "r2"),
	)
	if want := []language.Tuple{tup("user:u1", "assignee", "role:r1")}; !reflect.DeepEqual(d, want) {
		t.Errorf("desired = %+v, want %+v", d, want)
	}
	if want := []Contested{{Tuple: tup("user:u1", "assignee", "role:r2"), By: "snapshot.role"}}; !reflect.DeepEqual(c, want) {
		t.Errorf("contested = %+v, want %+v", c, want)
	}
}

func TestDesiredSameEventRemovalAndWriteIsNotContested(t *testing.T) {
	d, c := desiredOf(t, cev("snapshot.swap", "u1"))
	if len(d) != 1 || len(c) != 0 {
		t.Errorf("desired = %+v, contested = %+v, want one and none", d, c)
	}
}

func TestDesiredManyEventsWithTheSameFilter(t *testing.T) {
	const n = 20000
	events := make([]map[string]any, 0, 2*n+5)
	for range n {
		events = append(events, cev("snapshot.tick", "x"))
	}
	for i := range n {
		events = append(events, cev("snapshot.organization.member", fmt.Sprintf("u%d", i)))
	}
	for i := range 5 {
		events = append(events, cev("snapshot.owner", fmt.Sprintf("o%d", i)))
	}
	start := time.Now()
	d, c := desiredOf(t, events...)
	t.Logf("%d events in %s", len(events), time.Since(start))
	if len(d) != n || len(c) != 5 || c[0].By != "snapshot.tick" {
		t.Errorf("desired = %d, contested = %d", len(d), len(c))
	}
}

func TestDesiredEventsOfOneTypeCannotVouchForEachOther(t *testing.T) {
	const roleType = "snapshot.role"
	d, c := desiredOf(t,
		cev(roleType, "u1", "role", "A"), cev(roleType, "u1", "role", "B"), cev(roleType, "u1", "role", "C"),
		cev(roleType, "u2", "role", "A"),
	)
	if want := []language.Tuple{tup("user:u2", "assignee", "role:A")}; !reflect.DeepEqual(d, want) {
		t.Errorf("desired = %+v, want %+v", d, want)
	}
	if len(c) != 3 {
		t.Fatalf("contested = %+v, want the three roles of u1", c)
	}
	for i, role := range []string{"A", "B", "C"} {
		if want := (Contested{Tuple: tup("user:u1", "assignee", "role:"+role), By: roleType}); !reflect.DeepEqual(c[i], want) {
			t.Errorf("contested[%d] = %+v, want %+v", i, c[i], want)
		}
	}
}

func TestDesiredLaterEventOfTheSameTypeRemovesWhatAnEarlierOneWrote(t *testing.T) {
	d, c := desiredOf(t,
		cev("snapshot.organization.member", "u1"),
		cev("snapshot.organization.member", "u1", "drop", true),
	)
	if len(d) != 0 || len(c) != 1 || c[0].By != "snapshot.organization.member" {
		t.Errorf("desired = %+v, contested = %+v, want none and one by the member type", d, c)
	}
}
