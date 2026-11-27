package mapping

import (
	"context"
	"reflect"
	"testing"

	"github.com/openfga/mapper/language"
)

// lastOnlyState wraps a state so only the PlanState methods are reachable, as
// with a state kept on disk: Last sees only what was Planned before the event.
type lastOnlyState struct{ PlanState }

func TestPlannerWithMapStateMatchesPlanEvents(t *testing.T) {
	cases := []struct {
		name, src string
		events    []map[string]any
	}{
		{"membership", membership, []map[string]any{
			ev("type", "added", "user", "u1", "team", "t1"),
			ev("type", "other"),
			ev("type", "roster", "team", "t1", "users", []any{"u2", "u3"}),
			ev("type", "removed", "user", "u2", "team", "t1"),
			ev("type", "roster", "team", "t1", "users", []any{"u3"}),
		}},
		{"conditioned", conditioned, []map[string]any{
			ev("type", "grant", "doc", "d", "user", "u", "limit", "1"),
			ev("type", "grant", "doc", "d", "user", "u", "limit", "2"),
			ev("type", "roster", "doc", "d", "users", []any{"a", "b"}, "limit", "3"),
		}},
		{"direct conflict", direct, []map[string]any{
			ev("type", "grant", "doc", "d", "user", "u", "limit", "1"),
			ev("type", "grant", "doc", "d", "user", "u", "limit", "2"),
		}},
		{"direct delete between", direct, []map[string]any{
			ev("type", "grant", "doc", "d", "user", "u", "limit", "1"),
			ev("type", "revoke", "doc", "d", "user", "u"),
			ev("type", "grant", "doc", "d", "user", "u", "limit", "2"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := compile(t, tc.src)
			a := NewPlanner(m, &memStore{})
			b := NewPlannerWith(m, &memStore{}, NewMapState())
			for i, e := range tc.events {
				wantEP, wantErr := a.Add(context.Background(), i+1, e)
				gotEP, gotErr := b.Add(context.Background(), i+1, e)
				if !reflect.DeepEqual(gotEP, wantEP) {
					t.Errorf("event %d plan = %+v, want %+v", i+1, gotEP, wantEP)
				}
				if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && gotErr.Error() != wantErr.Error()) {
					t.Errorf("event %d err = %v, want %v", i+1, gotErr, wantErr)
				}
				if wantErr != nil {
					break
				}
			}
		})
	}
}

func TestConflictCheckSeesTheEventsOwnChanges(t *testing.T) {
	u := language.Tuple{User: "user:u", Relation: "viewer", Object: "doc:d", Condition: "before"}
	del := u
	del.Action = language.ActionDelete
	withCtx := func(n string) language.Tuple {
		c := u
		c.Context = map[string]any{"limit": n}
		return c
	}
	for name, state := range map[string]PlanState{"map": NewMapState(), "last-only": lastOnlyState{NewMapState()}} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ep1 := EventPlan{Index: 1, Changes: []language.Tuple{withCtx("1")}}
			if err := trackWrites(ctx, ep1, state); err != nil {
				t.Fatal(err)
			}
			// Delete then rewrite with another condition in one event: fine.
			ep2 := EventPlan{Index: 2, Changes: []language.Tuple{del, withCtx("2")}}
			if err := trackWrites(ctx, ep2, state); err != nil {
				t.Fatalf("delete then write in one event: %v", err)
			}
			ep3 := EventPlan{Index: 3, Changes: []language.Tuple{withCtx("3")}}
			err := trackWrites(ctx, ep3, state)
			if err == nil {
				t.Fatal("want a conflict with event 2")
			}
			want := "event 3: writes user:u viewer doc:d with a different condition or context than event 2; " +
				"OpenFGA keeps one tuple per user, relation and object and rejects the second write — " +
				"give the rule a tuple_filter so it replaces the tuple"
			if err.Error() != want {
				t.Errorf("err = %v\nwant  %s", err, want)
			}
			// The failed event recorded nothing.
			last, ok, _ := state.Last(ctx, URO(u))
			if !ok || last.Event != 2 {
				t.Errorf("Last = %+v, %v; want event 2", last, ok)
			}
		})
	}
}
