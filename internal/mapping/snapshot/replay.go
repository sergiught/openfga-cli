package snapshot

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/openfga/mapper"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// emptyStore is the store events are replayed against: nothing is in it, so
// only what earlier events wrote is visible to a tuple_filter.
type emptyStore struct{}

func (emptyStore) ReadTuples(context.Context, language.TupleFilter) ([]language.Tuple, error) {
	return nil, nil
}

// Replay turns snapshot events into the tuples that should exist. Events are
// added in order and none is kept.
type Replay struct {
	planner *mapping.Planner
	set     map[string]language.Tuple
	events  int
}

// NewReplay returns a Replay of events through m against an empty store.
func NewReplay(m *mapper.Mapping) *Replay {
	return &Replay{planner: mapping.NewPlanner(m, emptyStore{}), set: map[string]language.Tuple{}}
}

// Add replays events, in order, after those of earlier calls.
func (r *Replay) Add(ctx context.Context, events []map[string]any) error {
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.events++
		ep, err := r.planner.Add(ctx, r.events, event)
		if err != nil {
			var ee *mapping.EventError
			if errors.As(err, &ee) {
				if subject, _ := event["subject"].(string); subject != "" {
					return fmt.Errorf("%s (%s): %w", ee.Type, subject, ee.Err)
				}
				return fmt.Errorf("%s: %w", ee.Type, ee.Err)
			}
			return err
		}
		for _, c := range ep.Changes {
			k := Identity(c)
			if c.Action == language.ActionDelete {
				delete(r.set, k)
				continue
			}
			c.Action = ""
			r.set[k] = c
		}
	}
	return nil
}

// Events is how many events have been replayed.
func (r *Replay) Events() int { return r.events }

// Done returns the tuples that should exist, sorted by shape, then user,
// relation, object.
func (r *Replay) Done() []language.Tuple {
	out := make([]language.Tuple, 0, len(r.set))
	for _, t := range r.set {
		out = append(out, t)
	}
	sortTuples(out)
	return out
}

// Desired replays events through m against an empty store and returns the
// tuples that should exist, sorted by shape, then user, relation, object.
func Desired(ctx context.Context, m *mapper.Mapping, events []map[string]any, progress func(done, total int)) ([]language.Tuple, error) {
	r := NewReplay(m)
	for i := range events {
		if err := r.Add(ctx, events[i:i+1]); err != nil {
			return nil, err
		}
		if progress != nil {
			progress(i+1, len(events))
		}
	}
	return r.Done(), nil
}

// Identity is what OpenFGA keys a tuple by.
func Identity(t language.Tuple) string {
	return t.User + "\x00" + t.Relation + "\x00" + t.Object
}

// sortTuples orders by shape, then user, relation, object. Each tuple's shape
// is rendered once, not on every comparison.
func sortTuples(ts []language.Tuple) {
	type keyed struct {
		shape string
		t     language.Tuple
	}
	ks := make([]keyed, len(ts))
	for i, t := range ts {
		ks[i] = keyed{mapping.ShapeOf(t).String(), t}
	}
	sort.SliceStable(ks, func(i, j int) bool {
		a, b := ks[i], ks[j]
		if a.shape != b.shape {
			return a.shape < b.shape
		}
		if a.t.User != b.t.User {
			return a.t.User < b.t.User
		}
		if a.t.Relation != b.t.Relation {
			return a.t.Relation < b.t.Relation
		}
		return a.t.Object < b.t.Object
	})
	for i, k := range ks {
		ts[i] = k.t
	}
}
