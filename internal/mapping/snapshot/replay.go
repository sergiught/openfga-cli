package snapshot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/openfga/mapper"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// emptyStore is the store events are replayed against: nothing is in it, so
// only what earlier events wrote is visible to tuple_filter. A removal of
// something a later event writes is therefore never seen by the replay, and
// neither is a removal of what another event of the same type wrote, which is
// why Done sets such tuples aside.
type emptyStore struct{}

func (emptyStore) ReadTuples(context.Context, language.TupleFilter) ([]language.Tuple, error) {
	return nil, nil
}

// Contested is a tuple the replay cannot vouch for, because a snapshot has
// no order within an event type: an event wrote it after an earlier event
// asked for it to be gone, or an event of the type that last wrote it removed
// it.
type Contested struct {
	Tuple language.Tuple
	// By is the type of the event that removes it: the earliest one in the
	// first case, the type of both events in the second.
	By string
}

// stored is a tuple in the set, the index and type id of the event that last
// wrote it, and whether an event of that type removed it.
type stored struct {
	tuple  language.Tuple
	writer int
	typ    int32
	marked bool
}

// removal is a Removal with the index and type of the event that asked.
type removal struct {
	mapping.Removal
	index int
	by    string
}

// Replay turns snapshot events into tuples that should exist. Events are
// added in order and none is kept. The order between event types is
// documented, so a later type's removal removes what an earlier type wrote.
// The order within a type is only the order the provider listed events in,
// so Replay sets such tuples aside as contested: those written after a
// removal by an earlier event, and those removed by another event of the type
// that wrote them. It remembers each tuple in the set with the index and type
// id of the event that last wrote it, and each distinct removal with the
// first event that asked for it, so memory does not grow with the number of
// write-only events.
type Replay struct {
	planner *mapping.Planner
	set     map[string]stored
	events  int

	// typeIDs gives each event type a small id, so a tuple stores an int
	// rather than the type's name; types is the inverse.
	typeIDs map[string]int32
	types   []string

	// seen collapses removals with an identical filter and nothing kept to
	// the first, which has the lowest index.
	seen map[language.TupleFilter]bool
	// The removals, each list in index order, by what narrows a tuple down:
	// the filter's user, else its exact object, else its object type prefix,
	// else nothing.
	byUser, byObject, byPrefix map[string][]*removal
	everything                 []*removal
}

// NewReplay returns a Replay of events through m against an empty store.
func NewReplay(m *mapper.Mapping) *Replay {
	return &Replay{planner: mapping.NewPlanner(m, emptyStore{}), set: map[string]stored{}}
}

// Add replays events, in order, after earlier calls.
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
		typ := r.typeID(ep.Type)
		for _, c := range ep.Changes {
			k := Identity(c)
			prev, had := r.set[k]
			if c.Action == language.ActionDelete {
				// Deletes come before writes within an event, so prev is from
				// an earlier event. Another event of the writer's type
				// removes it: their order means nothing, so keep it,
				// marked. Conservative: a marked
				// tuple that a later type's rule would also have removed
				// stays contested, because the planner no longer sees it and
				// plans no delete. Listing it costs a line in a report;
				// dropping it could cost a tuple.
				if had && prev.typ == typ {
					prev.marked = true
					r.set[k] = prev
				} else {
					delete(r.set, k)
				}
				continue
			}
			c.Action = ""
			// A write by another type clears the mark: the tuple is judged by
			// the removal rule, which names the type that asked for it to go.
			r.set[k] = stored{tuple: c, writer: r.events, typ: typ, marked: prev.marked && prev.typ == typ}
		}
		for _, rm := range ep.Removals {
			r.record(rm, ep)
		}
	}
	return nil
}

// typeID returns the id of an event type, new when first seen.
func (r *Replay) typeID(name string) int32 {
	if id, ok := r.typeIDs[name]; ok {
		return id
	}
	if r.typeIDs == nil {
		r.typeIDs = map[string]int32{}
	}
	id := int32(len(r.types)) //nolint:gosec // event types are few
	r.typeIDs[name] = id
	r.types = append(r.types, name)
	return id
}

// record keeps rm for Done, unless an earlier removal is the same.
func (r *Replay) record(rm mapping.Removal, ep mapping.EventPlan) {
	if len(rm.Keep) == 0 {
		if r.seen == nil {
			r.seen = map[language.TupleFilter]bool{}
		}
		if r.seen[rm.Filter] {
			return
		}
		r.seen[rm.Filter] = true
	}
	if r.byUser == nil {
		r.byUser, r.byObject, r.byPrefix = map[string][]*removal{}, map[string][]*removal{}, map[string][]*removal{}
	}
	x := &removal{Removal: rm, index: ep.Index, by: ep.Type}
	f := rm.Filter
	switch {
	case f.User != "":
		r.byUser[f.User] = append(r.byUser[f.User], x)
	case f.Object != "" && strings.HasSuffix(f.Object, ":") && strings.Count(f.Object, ":") == 1 && len(f.Object) > 1:
		r.byPrefix[f.Object] = append(r.byPrefix[f.Object], x)
	case f.Object != "":
		r.byObject[f.Object] = append(r.byObject[f.Object], x)
	default:
		r.everything = append(r.everything, x)
	}
}

// Events is how many events have been replayed.
func (r *Replay) Events() int { return r.events }

// Done returns the tuples that should exist and, apart from them, the
// contested ones: those an event of their writer's type removed, and those an
// earlier event removes. Each tuple is in one list. Both are sorted by shape,
// then user, relation, object.
func (r *Replay) Done() (desired []language.Tuple, contested []Contested) {
	desired = make([]language.Tuple, 0, len(r.set))
	by := map[string]string{}
	var ts []language.Tuple
	for k, s := range r.set {
		if s.marked {
			by[k] = r.types[s.typ]
		} else if b, ok := r.contestedBy(s); ok {
			by[k] = b
		} else {
			desired = append(desired, s.tuple)
			continue
		}
		ts = append(ts, s.tuple)
	}
	sortTuples(desired)
	if len(ts) > 0 {
		sortTuples(ts)
		contested = make([]Contested, len(ts))
		for i, t := range ts {
			contested[i] = Contested{Tuple: t, By: by[Identity(t)]}
		}
	}
	return desired, contested
}

// contestedBy returns the type of the earliest event before s's writer that
// asked for s to be gone.
func (r *Replay) contestedBy(s stored) (string, bool) {
	if r.byUser == nil {
		return "", false
	}
	t := s.tuple
	var best *removal
	scan := func(list []*removal) {
		for _, x := range list {
			if x.index >= s.writer || (best != nil && x.index >= best.index) {
				return
			}
			if x.Removes(t) {
				best = x
				return
			}
		}
	}
	scan(r.byUser[t.User])
	scan(r.byObject[t.Object])
	if i := strings.IndexByte(t.Object, ':'); i > 0 {
		scan(r.byPrefix[t.Object[:i+1]])
	}
	scan(r.everything)
	if best == nil {
		return "", false
	}
	return best.by, true
}

// Desired replays events through m against an empty store and returns the
// tuples that should exist and the contested ones (see Replay), each sorted by
// shape, user, relation, object.
func Desired(ctx context.Context, m *mapper.Mapping, events []map[string]any, progress func(done, total int)) (desired []language.Tuple, contested []Contested, err error) {
	r := NewReplay(m)
	for i := range events {
		if err := r.Add(ctx, events[i:i+1]); err != nil {
			return nil, nil, err
		}
		if progress != nil {
			progress(i+1, len(events))
		}
	}
	desired, contested = r.Done()
	return desired, contested, nil
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
