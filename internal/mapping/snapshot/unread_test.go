package snapshot

import (
	"context"
	"reflect"
	"testing"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

const unreadWindow = "snapshot.user"

// unreadTail is the rules every unread case shares: one that writes a
// membership, one that writes a role, and one that writes a tuple for the
// windowed type itself.
const unreadTail = `
  - name: member
    when: input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:{{ input.org }}"
  - name: grant
    when: input.type == "snapshot.grant"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: assignee
        object: "role:{{ input.role }}"
  - name: revoke
    when: input.type == "snapshot.revoke"
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "organization:"
        action: delete
  - name: home
    when: input.type == "snapshot.user"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: owner
        object: "home:{{ input.user_id }}"
`

// unreadOf replays events of a mapping made of head and unreadTail with
// snapshot.user read by window.
func unreadOf(t *testing.T, head string, events ...map[string]any) ([]language.Tuple, []Contested) {
	t.Helper()
	src := "version: \"1\"\nrules:" + head + unreadTail
	f, err := mapping.LoadBytes("m.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := mapping.CompileSource([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	r := NewReplay(m)
	w := []string{unreadWindow}
	r.Window(w, UnreadRemovals(f.Doc, w))
	if err := r.Add(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	return r.Done()
}

const unreadDeleteFilter = `
  - name: blocked
    when: input.type == "snapshot.user"
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "organization:"
        action: delete
`

func TestUnreadRemovalSetsAsideWhatItReaches(t *testing.T) {
	member := cev("snapshot.organization.member", "u1")
	grant := cev("snapshot.grant", "u1", "role", "r1")
	d, c := unreadOf(t, unreadDeleteFilter, member, grant)
	if want := []language.Tuple{tup("user:u1", "assignee", "role:r1")}; !reflect.DeepEqual(d, want) {
		t.Errorf("desired = %+v, want %+v", d, want)
	}
	want := []Contested{{Tuple: tup("user:u1", "member", "organization:o1"), By: unreadWindow, Unread: true}}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("contested = %+v, want %+v", c, want)
	}
}

func TestUnreadRemovalLeavesWhatALiteralRelationMisses(t *testing.T) {
	head := `
  - name: owners
    when: input.type == "snapshot.user"
    tuple_filters:
      - relation: owner
        object: "organization:"
        action: delete
`
	d, c := unreadOf(t, head, cev("snapshot.organization.member", "u1"))
	if len(d) != 1 || len(c) != 0 {
		t.Errorf("desired = %+v, contested = %+v, want one and none", d, c)
	}
}

func TestUnreadRemovalLeavesWhatTheWindowedTypeWrote(t *testing.T) {
	// The filter reaches home:u1's owner tuple, but the windowed type wrote it
	// itself, after every user outside the window.
	head := `
  - name: all
    when: input.type == "snapshot.user"
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "home:"
        action: delete
`
	d, c := unreadOf(t, head, cev("snapshot.user", "u1"))
	if want := []language.Tuple{tup("user:u1", "owner", "home:u1")}; !reflect.DeepEqual(d, want) || len(c) != 0 {
		t.Errorf("desired = %+v, contested = %+v, want the owner tuple and none", d, c)
	}
}

func TestUnreadPatchFilterAndDeleteTupleSetAside(t *testing.T) {
	patch := `
  - name: patch
    when: input.type == "snapshot.user"
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "organization:"
        action: patch
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:home"
`
	deleteTuple := `
  - name: removed
    when: input.type == "snapshot.user"
    action: delete
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:{{ input.org }}"
`
	templateDelete := `
  - name: mixed
    when: input.type == "snapshot.user"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:{{ input.org }}"
        action: delete
`
	for name, head := range map[string]string{"patch filter": patch, "delete rule": deleteTuple, "delete template": templateDelete} {
		d, c := unreadOf(t, head, cev("snapshot.organization.member", "u1"), cev("snapshot.grant", "u1", "role", "r1"))
		want := []Contested{{Tuple: tup("user:u1", "member", "organization:o1"), By: unreadWindow, Unread: true}}
		if !reflect.DeepEqual(c, want) || len(d) != 1 {
			t.Errorf("%s: desired = %+v, contested = %+v, want the membership set aside", name, d, c)
		}
	}
}

func TestUnreadLiteralFilterReachesOnlyItsOwnValue(t *testing.T) {
	head := `
  - name: one
    when: input.type == "snapshot.user"
    tuple_filters:
      - user: "user:u9"
        object: "organization:o9"
        action: delete
`
	d, c := unreadOf(t, head,
		cev("snapshot.organization.member", "u1"),
		cev("snapshot.organization.member", "u9", "org", "o9"),
		cev("snapshot.organization.member", "u9"))
	want := []Contested{{Tuple: tup("user:u9", "member", "organization:o9"), By: unreadWindow, Unread: true}}
	if !reflect.DeepEqual(c, want) || len(d) != 2 {
		t.Errorf("desired = %+v, contested = %+v, want only user:u9 in o9 set aside", d, c)
	}
}

func TestUnreadTemplateStartingWithBracesReachesEverything(t *testing.T) {
	head := `
  - name: all
    when: input.type == "snapshot.user"
    tuple_filters:
      - object: "{{ input.kind }}:"
        action: delete
`
	d, c := unreadOf(t, head, cev("snapshot.organization.member", "u1"), cev("snapshot.grant", "u1", "role", "r1"))
	if len(d) != 0 || len(c) != 2 {
		t.Errorf("desired = %+v, contested = %+v, want none and two", d, c)
	}
}

func TestUnreadRuleThatCannotFireOnTheWindowedTypeSetsNothingAside(t *testing.T) {
	head := `
  - name: elsewhere
    when: input.type == "snapshot.tick"
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "organization:"
        action: delete
`
	d, c := unreadOf(t, head, cev("snapshot.organization.member", "u1"))
	if len(d) != 1 || len(c) != 0 {
		t.Errorf("desired = %+v, contested = %+v, want one and none", d, c)
	}
}

func TestUnreadAlreadyContestedKeepsItsOwnReason(t *testing.T) {
	d, c := unreadOf(t, unreadDeleteFilter, cev("snapshot.revoke", "u1"), cev("snapshot.organization.member", "u1"))
	want := []Contested{{Tuple: tup("user:u1", "member", "organization:o1"), By: "snapshot.revoke"}}
	if len(d) != 0 || !reflect.DeepEqual(c, want) {
		t.Errorf("desired = %+v, contested = %+v, want %+v", d, c, want)
	}
}

func TestReplayWithoutAWindowIsUnchanged(t *testing.T) {
	src := "version: \"1\"\nrules:" + unreadDeleteFilter + unreadTail
	m, _, err := mapping.CompileSource([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	r := NewReplay(m)
	if err := r.Add(context.Background(), []map[string]any{cev("snapshot.organization.member", "u1")}); err != nil {
		t.Fatal(err)
	}
	if d, c := r.Done(); len(d) != 1 || len(c) != 0 {
		t.Errorf("desired = %+v, contested = %+v, want one and none", d, c)
	}
}

func TestRemoversWordsUnreadTuples(t *testing.T) {
	const (
		oldReason    = "removes tuples that other events write, and a snapshot does not say which happened last; not compared: "
		unreadReason = "read with --since: could have removed these for a snapshot.user that was not read; not compared: "
	)
	filter := []mapping.TupleFilter{{User: "user:{{ input.id }}", Object: "organization:", Action: "delete"}}
	doc := &mapping.Document{Rules: []mapping.Rule{
		{Name: "blocked", When: `input.type == "snapshot.user"`, Filters: filter},
	}}
	one := tup("user:u1", "member", "organization:o1")
	two := tup("user:u2", "member", "organization:o1")
	three := tup("user:u3", "member", "organization:o1")
	got := Removers(doc, []Contested{
		{Tuple: one, By: unreadWindow}, {Tuple: two, By: unreadWindow, Unread: true}, {Tuple: three, By: unreadWindow, Unread: true},
	})
	shape := []mapping.Shape{mapping.ShapeOf(one)}
	want := []NotChecked{
		{Rule: "blocked", Reason: oldReason + "1 tuple of user member organization", Shapes: shape, Tuples: []language.Tuple{one}},
		{Rule: "blocked", Reason: unreadReason + "2 tuples of user member organization", Shapes: shape, Tuples: []language.Tuple{two, three}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Removers() = %+v, want %+v", got, want)
	}
}
