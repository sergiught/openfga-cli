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
// it. Under a window it is also one that a rule on a windowed type could have
// removed for an event that was not read.
type Contested struct {
	Tuple language.Tuple
	// By is the type of the event that removes it: the earliest one in the
	// first case, the type of both events in the second, the windowed type in
	// the third.
	By string
	// Unread is true in the third case: no event removed it, but one that
	// the window left out might have.
	Unread bool
}

// Stored is a tuple in the set, the index and type id of the event that last
// wrote it, and whether an event of that type removed it.
type Stored struct {
	Tuple  language.Tuple
	Writer int   // index of the event that last wrote it
	Type   int32 // id of that event's type
	Marked bool  // an event of that type removed it
}

// TupleSet holds the tuples a Replay has written and not yet removed, by
// Identity.
type TupleSet interface {
	Get(ctx context.Context, id string) (Stored, bool, error)
	Put(ctx context.Context, id string, s Stored) error
	Delete(ctx context.Context, id string) error
	// Sorted calls fn for each tuple, ordered by shape, user, relation, object.
	Sorted(ctx context.Context, fn func(Stored) error) error
}

type mapSet map[string]Stored

// NewMapSet returns a TupleSet held in memory.
func NewMapSet() TupleSet { return mapSet{} }

func (m mapSet) Get(_ context.Context, id string) (Stored, bool, error) {
	s, ok := m[id]
	return s, ok, nil
}

func (m mapSet) Put(_ context.Context, id string, s Stored) error {
	m[id] = s
	return nil
}

func (m mapSet) Delete(_ context.Context, id string) error {
	delete(m, id)
	return nil
}

func (m mapSet) Sorted(ctx context.Context, fn func(Stored) error) error {
	type keyed struct {
		shape string
		s     Stored
	}
	ks := make([]keyed, 0, len(m))
	for _, s := range m {
		ks = append(ks, keyed{ShapeKey(s.Tuple), s})
	}
	sort.Slice(ks, func(i, j int) bool { return less(ks[i].shape, ks[i].s.Tuple, ks[j].shape, ks[j].s.Tuple) })
	for _, k := range ks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(k.s); err != nil {
			return err
		}
	}
	return nil
}

// ShapeKey is the shape a tuple sorts by first.
func ShapeKey(t language.Tuple) string { return mapping.ShapeOf(t).String() }

// less orders by shape, then user, relation, object.
func less(sa string, a language.Tuple, sb string, b language.Tuple) bool {
	if sa != sb {
		return sa < sb
	}
	if a.User != b.User {
		return a.User < b.User
	}
	if a.Relation != b.Relation {
		return a.Relation < b.Relation
	}
	return a.Object < b.Object
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
	set     TupleSet
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

	// windowed and unread are set by Window.
	windowed map[string]bool
	unread   func(language.Tuple) (string, bool)
}

// NewReplay returns a Replay of events through m against an empty store.
func NewReplay(m *mapper.Mapping) *Replay {
	return NewReplayWith(m, NewMapSet(), mapping.NewMapState())
}

// NewReplayWith is NewReplay with the tuples kept in set and the planner's
// changes in state.
func NewReplayWith(m *mapper.Mapping, set TupleSet, state mapping.PlanState) *Replay {
	return &Replay{planner: mapping.NewPlannerWith(m, emptyStore{}, state), set: set}
}

// Window tells r that events of the windowed types were read by window, so
// users outside it sent none. unread says, for a tuple, the windowed type whose
// unread events could have removed it. Done then sets aside a tuple that an
// event of another type last wrote and unread reaches. A nil unread, or no call,
// leaves r as it was.
func (r *Replay) Window(windowed []string, unread func(language.Tuple) (string, bool)) {
	r.windowed = make(map[string]bool, len(windowed))
	for _, n := range windowed {
		r.windowed[n] = true
	}
	r.unread = unread
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
			prev, had, err := r.set.Get(ctx, k)
			if err != nil {
				return err
			}
			if c.Action == language.ActionDelete {
				// Deletes come before writes within an event, so prev is from
				// an earlier event. Another event of the writer's type
				// removes it: their order means nothing, so keep it,
				// marked. Conservative: a marked
				// tuple that a later type's rule would also have removed
				// stays contested, because the planner no longer sees it and
				// plans no delete. Listing it costs a line in a report;
				// dropping it could cost a tuple.
				if had && prev.Type == typ {
					prev.Marked = true
					err = r.set.Put(ctx, k, prev)
				} else {
					err = r.set.Delete(ctx, k)
				}
				if err != nil {
					return err
				}
				continue
			}
			c.Action = ""
			// A write by another type clears the mark: the tuple is judged by
			// the removal rule, which names the type that asked for it to go.
			if err := r.set.Put(ctx, k, Stored{Tuple: c, Writer: r.events, Type: typ, Marked: prev.Marked && prev.Type == typ}); err != nil {
				return err
			}
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
// then user, relation, object. It is for a Replay on a NewMapSet, which cannot
// fail.
func (r *Replay) Done() (desired []language.Tuple, contested []Contested) {
	contested, _ = r.DoneEach(context.Background(), func(t language.Tuple) error {
		desired = append(desired, t)
		return nil
	})
	return desired, contested
}

// DoneEach gives each desired tuple to desired, in sorted order, and returns
// the contested ones, sorted.
func (r *Replay) DoneEach(ctx context.Context, desired func(language.Tuple) error) ([]Contested, error) {
	var contested []Contested
	err := r.set.Sorted(ctx, func(s Stored) error {
		if s.Marked {
			contested = append(contested, Contested{Tuple: s.Tuple, By: r.types[s.Type]})
		} else if b, ok := r.contestedBy(s); ok {
			contested = append(contested, Contested{Tuple: s.Tuple, By: b})
		} else if b, ok := r.unreadBy(s); ok {
			contested = append(contested, Contested{Tuple: s.Tuple, By: b, Unread: true})
		} else {
			return desired(s.Tuple)
		}
		return nil
	})
	return contested, err
}

// contestedBy returns the type of the earliest event before s's writer that
// asked for s to be gone.
func (r *Replay) contestedBy(s Stored) (string, bool) {
	if r.byUser == nil {
		return "", false
	}
	t := s.Tuple
	var best *removal
	scan := func(list []*removal) {
		for _, x := range list {
			if x.index >= s.Writer || (best != nil && x.index >= best.index) {
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

// unreadBy returns the windowed type whose unread events could have removed s,
// when the event type that last wrote s was not read by window. A tuple a
// windowed type wrote is later than every user outside the window.
func (r *Replay) unreadBy(s Stored) (string, bool) {
	if r.unread == nil || r.windowed[r.types[s.Type]] {
		return "", false
	}
	return r.unread(s.Tuple)
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
		ks[i] = keyed{ShapeKey(t), t}
	}
	sort.SliceStable(ks, func(i, j int) bool { return less(ks[i].shape, ks[i].t, ks[j].shape, ks[j].t) })
	for i, k := range ks {
		ts[i] = k.t
	}
}
