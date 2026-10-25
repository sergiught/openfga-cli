package mapping

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openfga/mapper"
	"github.com/openfga/mapper/language"
)

type memStore struct{ tuples []language.Tuple }

func (s *memStore) ReadTuples(_ context.Context, f language.TupleFilter) ([]language.Tuple, error) {
	var out []language.Tuple
	for _, t := range s.tuples {
		if matchesFilter(t, f) {
			out = append(out, t)
		}
	}
	return out, nil
}

const membership = `version: "1"
rules:
  - name: added
    when: input.type == "added"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "team:{{ input.team }}"
  - name: removed
    when: input.type == "removed"
    action: delete
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "team:{{ input.team }}"
  - name: roster
    when: input.type == "roster"
    tuple_filters:
      - relation: member
        object: "team:{{ input.team }}"
    iterator:
      source: input.users
      as: u
      tuples:
        - user: "user:{{ u }}"
          relation: member
          object: "team:{{ input.team }}"
`

func compile(t *testing.T, src string) *mapper.Mapping {
	t.Helper()
	m, _, err := CompileSource([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func ev(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func TestPlanWritesAndCountsUnmatched(t *testing.T) {
	p, err := PlanEvents(context.Background(), compile(t, membership),
		[]map[string]any{ev("type", "added", "user", "u1", "team", "t1"), ev("type", "other")}, 1, &memStore{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Writes != 1 || p.Deletes != 0 || p.Unmatched != 1 || len(p.Events) != 2 || !p.Events[0].Matched {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPlanSeesEarlierEventsChanges(t *testing.T) {
	// The roster patch after an add must keep u1 (planned) and delete u9 (stored).
	store := &memStore{tuples: []language.Tuple{{User: "user:u9", Relation: "member", Object: "team:t1"}}}
	p, err := PlanEvents(context.Background(), compile(t, membership), []map[string]any{
		ev("type", "added", "user", "u1", "team", "t1"),
		ev("type", "roster", "team", "t1", "users", []any{"u1"}),
	}, 1, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	second := p.Events[1].Changes
	if len(second) != 1 || second[0].Action != language.ActionDelete || second[0].User != "user:u9" {
		t.Fatalf("event 2 changes = %+v", second)
	}
}

func TestPlanLaterDeleteUndoesEarlierWrite(t *testing.T) {
	p, err := PlanEvents(context.Background(), compile(t, membership), []map[string]any{
		ev("type", "added", "user", "u1", "team", "t1"),
		ev("type", "removed", "user", "u1", "team", "t1"),
	}, 1, &memStore{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Writes != 1 || p.Deletes != 1 {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPlanTypePrefixScope(t *testing.T) {
	src := strings.Replace(membership, `object: "team:{{ input.team }}"
    iterator:`, `object: "team:"
        user: "user:{{ input.owner }}"
    iterator:`, 1)
	store := &memStore{tuples: []language.Tuple{
		{User: "user:o", Relation: "member", Object: "team:a"},
		{User: "user:o", Relation: "member", Object: "team:b"},
	}}
	p, err := PlanEvents(context.Background(), compile(t, src), []map[string]any{
		ev("type", "roster", "team", "a", "owner", "o", "users", []any{"o"}),
	}, 1, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	// desired: user:o member team:a; the prefix scope covers team:b too, so it is deleted.
	if p.Writes != 0 || p.Deletes != 1 || p.Events[0].Changes[0].Object != "team:b" {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPlanFailsWholeOnABadEvent(t *testing.T) {
	_, err := PlanEvents(context.Background(), compile(t, membership), []map[string]any{
		ev("type", "added", "user", "u1", "team", "t1"),
		ev("type", "added"), // missing fields
	}, 1, &memStore{}, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "event 2") {
		t.Fatalf("err = %v", err)
	}
}

const conditioned = `version: "1"
rules:
  - name: grant
    when: input.type == "grant"
    tuple_filters:
      - relation: viewer
        object: "doc:{{ input.doc }}"
    tuples:
      - user: "user:{{ input.user }}"
        relation: viewer
        object: "doc:{{ input.doc }}"
        condition: before
        context:
          limit: "{{ input.limit }}"
  - name: roster
    when: input.type == "roster"
    tuple_filters:
      - relation: viewer
        object: "doc:{{ input.doc }}"
    iterator:
      source: input.users
      as: u
      tuples:
        - user: "user:{{ u }}"
          relation: viewer
          object: "doc:{{ input.doc }}"
          condition: before
          context:
            limit: "{{ input.limit }}"
`

func TestPlanNoChurnOnConditionContext(t *testing.T) {
	// Mapper renders every context value as a string, and a tuple ofga wrote
	// comes back from the store with that string.
	stored := language.Tuple{User: "user:u", Relation: "viewer", Object: "doc:d", Condition: "before",
		Context: map[string]any{"limit": "5"}}
	p, err := PlanEvents(context.Background(), compile(t, conditioned),
		[]map[string]any{ev("type", "grant", "doc", "d", "user", "u", "limit", float64(5))}, 1, &memStore{tuples: []language.Tuple{stored}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Writes+p.Deletes != 0 {
		t.Fatalf("unchanged tuple churned: %+v", p.Events[0].Changes)
	}
}

func TestPlanConditionContextChangeIsNotAtomic(t *testing.T) {
	stored := language.Tuple{User: "user:u", Relation: "viewer", Object: "doc:d", Condition: "before",
		Context: map[string]any{"limit": float64(5)}}
	p, err := PlanEvents(context.Background(), compile(t, conditioned),
		[]map[string]any{ev("type", "grant", "doc", "d", "user", "u", "limit", float64(5))}, 1, &memStore{tuples: []language.Tuple{stored}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch := p.Events[0].Changes
	if len(ch) != 2 || ch[0].Action != language.ActionDelete || ch[1].Action != language.ActionWrite ||
		ch[0].User != "user:u" || ch[1].User != "user:u" || p.Events[0].Atomic {
		t.Fatalf("plan = %+v", p.Events[0])
	}
}

func TestPlanOverlayKeepsAConditionChange(t *testing.T) {
	stored := language.Tuple{User: "user:u", Relation: "viewer", Object: "doc:d", Condition: "before",
		Context: map[string]any{"limit": float64(5)}}
	p, err := PlanEvents(context.Background(), compile(t, conditioned), []map[string]any{
		ev("type", "grant", "doc", "d", "user", "u", "limit", float64(5)),
		ev("type", "roster", "doc", "d", "users", []any{"u"}, "limit", float64(5)),
	}, 1, &memStore{tuples: []language.Tuple{stored}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Events[0].Changes) != 2 || len(p.Events[1].Changes) != 0 {
		t.Fatalf("plan = %+v", p.Events)
	}
}

func TestPlanOverlayHidesEarlierDeletes(t *testing.T) {
	store := &memStore{tuples: []language.Tuple{{User: "user:u1", Relation: "member", Object: "team:t1"}}}
	p, err := PlanEvents(context.Background(), compile(t, membership), []map[string]any{
		ev("type", "removed", "user", "u1", "team", "t1"),
		ev("type", "roster", "team", "t1", "users", []any{"u1"}),
	}, 1, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := p.Events[1].Changes
	if len(c) != 1 || c[0].Action != language.ActionWrite || c[0].User != "user:u1" {
		t.Fatalf("event 2 = %+v", c)
	}
}

func TestPlanOverlayKeepsReadsInScope(t *testing.T) {
	p, err := PlanEvents(context.Background(), compile(t, membership), []map[string]any{
		ev("type", "added", "user", "u1", "team", "t1"),
		ev("type", "roster", "team", "t2", "users", []any{"u2"}),
	}, 1, &memStore{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := p.Events[1].Changes
	if len(c) != 1 || c[0].Action != language.ActionWrite || c[0].User != "user:u2" {
		t.Fatalf("event 2 = %+v", c)
	}
}

func TestPlanLargeEventIsNotAtomic(t *testing.T) {
	// Mapper caps the tuples an event may produce, but a patch can delete any
	// number of stored ones: 101 deletes and 1 write.
	store := &memStore{}
	for i := 0; i <= maxChangesPerWrite; i++ {
		store.tuples = append(store.tuples, language.Tuple{User: fmt.Sprintf("user:s%d", i), Relation: "member", Object: "team:t1"})
	}
	p, err := PlanEvents(context.Background(), compile(t, membership),
		[]map[string]any{ev("type", "roster", "team", "t1", "users", []any{"u"})}, 1, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Events[0].Changes) != maxChangesPerWrite+2 || p.Events[0].Atomic {
		t.Fatalf("changes = %d, atomic = %v", len(p.Events[0].Changes), p.Events[0].Atomic)
	}
}

func TestPlanErrorOmitsEmptyType(t *testing.T) {
	_, err := PlanEvents(context.Background(), compile(t, membership),
		[]map[string]any{ev("type", "added"), ev("user", "u")}, 1, &memStore{}, nil)
	if err == nil || strings.Contains(err.Error(), "()") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanFromSkipsEarlierEventsAndKeepsTheirNumbers(t *testing.T) {
	var progress []int
	p, err := PlanEvents(context.Background(), compile(t, membership), []map[string]any{
		ev("type", "added"), // missing fields: planning it would fail
		ev("type", "added", "user", "u2", "team", "t1"),
		ev("type", "other"),
	}, 2, &memStore{}, func(done, total int) { progress = append(progress, done, total) })
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Events) != 2 || p.Events[0].Index != 2 || p.Events[1].Index != 3 || p.Writes != 1 || p.Unmatched != 1 {
		t.Fatalf("plan = %+v", p)
	}
	if fmt.Sprint(progress) != "[1 2 2 2]" {
		t.Fatalf("progress = %v", progress)
	}
}

// direct writes a conditioned tuple without a tuple_filter, so it never reads
// the store and cannot replace a tuple whose context changed.
const direct = `version: "1"
rules:
  - name: grant
    when: input.type == "grant"
    tuples:
      - user: "user:{{ input.user }}"
        relation: viewer
        object: "doc:{{ input.doc }}"
        condition: before
        context:
          limit: "{{ input.limit }}"
  - name: revoke
    when: input.type == "revoke"
    action: delete
    tuples:
      - user: "user:{{ input.user }}"
        relation: viewer
        object: "doc:{{ input.doc }}"
`

func TestPlanRejectsADirectWriteThatConflictsWithAnEarlierOne(t *testing.T) {
	_, err := PlanEvents(context.Background(), compile(t, direct), []map[string]any{
		ev("type", "grant", "doc", "d", "user", "u", "limit", "1"),
		ev("type", "other"),
		ev("type", "grant", "doc", "d", "user", "u", "limit", "2"),
	}, 1, &memStore{}, nil)
	want := "event 3 (grant): writes user:u viewer doc:d with a different condition or context than event 1; " +
		"OpenFGA keeps one tuple per user, relation and object and rejects the second write — " +
		"give the rule a tuple_filter so it replaces the tuple"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant  %s", err, want)
	}
}

func TestPlanAllowsWritesThatDoNotConflict(t *testing.T) {
	for name, tc := range map[string]struct {
		src    string
		events []map[string]any
	}{
		"same tuple twice": {direct, []map[string]any{
			ev("type", "grant", "doc", "d", "user", "u", "limit", "1"),
			ev("type", "grant", "doc", "d", "user", "u", "limit", "1"),
		}},
		"deleted in between": {direct, []map[string]any{
			ev("type", "grant", "doc", "d", "user", "u", "limit", "1"),
			ev("type", "revoke", "doc", "d", "user", "u"),
			ev("type", "grant", "doc", "d", "user", "u", "limit", "2"),
		}},
		"a filter replaces it": {conditioned, []map[string]any{
			ev("type", "grant", "doc", "d", "user", "u", "limit", "1"),
			ev("type", "grant", "doc", "d", "user", "u", "limit", "2"),
		}},
	} {
		if _, err := PlanEvents(context.Background(), compile(t, tc.src), tc.events, 1, &memStore{}, nil); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPlanDeleteFilterDeletesWhatItMatches(t *testing.T) {
	src := `version: "1"
rules:
  - name: left
    when: input.type == "left"
    tuple_filters:
      - user: "user:{{ input.user }}"
        relation: member
        object: "team:"
        action: delete
`
	store := &memStore{tuples: []language.Tuple{
		{User: "user:u1", Relation: "member", Object: "team:a"},
		{User: "user:u1", Relation: "member", Object: "team:b"},
		{User: "user:u2", Relation: "member", Object: "team:a"},
	}}
	p, err := PlanEvents(context.Background(), compile(t, src), []map[string]any{ev("type", "left", "user", "u1")}, 1, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := p.Events[0].Changes
	if p.Writes != 0 || p.Deletes != 2 || len(c) != 2 || c[0].Object != "team:a" || c[1].Object != "team:b" ||
		c[0].User != "user:u1" || c[1].User != "user:u1" || c[0].Action != language.ActionDelete {
		t.Fatalf("plan = %+v", p.Events[0])
	}
}
