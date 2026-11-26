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

// Desired replays events through m against an empty store and returns the
// tuples that should exist, sorted by shape, then user, relation, object.
func Desired(ctx context.Context, m *mapper.Mapping, events []map[string]any, progress func(done, total int)) ([]language.Tuple, error) {
	plan, err := mapping.PlanEvents(ctx, m, events, 1, emptyStore{}, progress)
	if err != nil {
		var ee *mapping.EventError
		if errors.As(err, &ee) && ee.Index >= 1 && ee.Index <= len(events) {
			if subject, _ := events[ee.Index-1]["subject"].(string); subject != "" {
				return nil, fmt.Errorf("%s (%s): %w", ee.Type, subject, ee.Err)
			}
			return nil, fmt.Errorf("%s: %w", ee.Type, ee.Err)
		}
		return nil, err
	}
	set := map[string]language.Tuple{}
	for _, ep := range plan.Events {
		for _, c := range ep.Changes {
			k := Identity(c)
			if c.Action == language.ActionDelete {
				delete(set, k)
				continue
			}
			c.Action = ""
			set[k] = c
		}
	}
	out := make([]language.Tuple, 0, len(set))
	for _, t := range set {
		out = append(out, t)
	}
	sortTuples(out)
	return out, nil
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
