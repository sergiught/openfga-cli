package workfile

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

type emptyStore struct{}

func (emptyStore) ReadTuples(context.Context, language.TupleFilter) ([]language.Tuple, error) {
	return nil, nil
}

const planMapping = `version: "1"
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

func planEv(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func TestWorkfilePlanStateParity(t *testing.T) {
	m, _, err := mapping.CompileSource([]byte(planMapping))
	require.NoError(t, err)
	f, _ := newFile(t)
	sequences := [][]map[string]any{
		{
			planEv("type", "added", "user", "u1", "team", "t1"),
			planEv("type", "roster", "team", "t1", "users", []any{"u2", "u3"}),
			planEv("type", "removed", "user", "u2", "team", "t1"),
			planEv("type", "roster", "team", "t1", "users", []any{"u3", "u4"}),
			planEv("type", "other"),
		},
		{
			planEv("type", "grant", "doc", "d", "user", "u", "limit", "1"),
			planEv("type", "revoke", "doc", "d", "user", "u"),
			planEv("type", "grant", "doc", "d", "user", "u", "limit", "2"),
			planEv("type", "grant", "doc", "d", "user", "u", "limit", "3"),
		},
	}
	for i, events := range sequences {
		t.Run(fmt.Sprint("sequence", i), func(t *testing.T) {
			// A fresh table per sequence.
			if _, err := f.db.Exec(`DELETE FROM plan`); err != nil {
				t.Fatal(err)
			}
			a := mapping.NewPlannerWith(m, emptyStore{}, mapping.NewMapState())
			b := mapping.NewPlannerWith(m, emptyStore{}, f.PlanState())
			sawErr := false
			for j, e := range events {
				wantEP, wantErr := a.Add(context.Background(), j+1, e)
				gotEP, gotErr := b.Add(context.Background(), j+1, e)
				if !reflect.DeepEqual(gotEP, wantEP) {
					t.Errorf("event %d plan = %+v, want %+v", j+1, gotEP, wantEP)
				}
				if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && gotErr.Error() != wantErr.Error()) {
					t.Errorf("event %d err = %v, want %v", j+1, gotErr, wantErr)
				}
				if wantErr != nil {
					sawErr = true
					break
				}
			}
			if i == 1 && !sawErr {
				t.Error("the conditional sequence should end in a conflict")
			}
		})
	}
}

func TestWorkfileMatchingFilters(t *testing.T) {
	f, _ := newFile(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic test data
	users := []string{"user:a", "user:b", "group:x#member"}
	rels := []string{"member", "viewer"}
	objs := []string{"doc:1", "doc:2", "docs:1", "team:1", "doc_x:1", "d%c:1", "plain", "doc"}
	state := mapping.NewMapState()
	var changes []mapping.PlannedChange
	for i := 0; i < 200; i++ {
		tup := language.Tuple{User: users[rng.Intn(len(users))], Relation: rels[rng.Intn(len(rels))], Object: objs[rng.Intn(len(objs))]}
		if rng.Intn(3) == 0 {
			tup.Action = language.ActionDelete
		} else if rng.Intn(2) == 0 {
			tup.Condition = "before"
			tup.Context = map[string]any{"limit": "1"}
		}
		changes = append(changes, mapping.PlannedChange{Tuple: tup, Event: i + 1})
	}
	require.NoError(t, state.Plan(ctx, changes))
	require.NoError(t, f.PlanState().Plan(ctx, changes))

	filters := []language.TupleFilter{
		{User: "user:a"},
		{Object: "doc:1"},
		{Object: "doc:"},
		{Object: "d%c:"},
		{Object: "doc_x:"},
		{Object: "plain"},
		{Object: "nosuch:"},
		{Relation: "viewer", Object: "doc:2"},
		{Relation: "member", Object: "team:"},
		{User: "group:x#member", Relation: "viewer"},
		{},
	}
	canon := func(pcs []mapping.PlannedChange) []string {
		var out []string
		for _, pc := range pcs {
			out = append(out, fmt.Sprintf("%d|%s|%s", pc.Event, pc.Tuple.Action, pc.Tuple.Key()))
		}
		sort.Strings(out)
		return out
	}
	for _, flt := range filters {
		want, err := state.Matching(ctx, flt)
		require.NoError(t, err)
		got, err := f.PlanState().Matching(ctx, flt)
		require.NoError(t, err)
		if !reflect.DeepEqual(canon(got), canon(want)) {
			t.Errorf("Matching(%+v): %d changes, want %d\n got %v\nwant %v", flt, len(got), len(want), canon(got), canon(want))
		}
	}
	// Last agrees too.
	for _, c := range changes {
		want, wok, _ := state.Last(ctx, mapping.URO(c.Tuple))
		got, gok, err := f.PlanState().Last(ctx, mapping.URO(c.Tuple))
		require.NoError(t, err)
		if wok != gok || !reflect.DeepEqual(canon([]mapping.PlannedChange{got}), canon([]mapping.PlannedChange{want})) {
			t.Fatalf("Last(%s) = %+v, %v; want %+v, %v", mapping.URO(c.Tuple), got, gok, want, wok)
		}
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.PlanState().Matching(cctx, language.TupleFilter{}); err == nil {
		t.Error("Matching ignored a cancelled context")
	}
}
