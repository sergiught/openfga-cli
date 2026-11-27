package mapping

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/openfga/mapper"
	"github.com/openfga/mapper/apply"
	"github.com/openfga/mapper/language"
)

// maxChangesPerWrite is OpenFGA's limit on tuples in one Write request.
const maxChangesPerWrite = 100

// TupleReader reads tuples from a store.
type TupleReader interface {
	ReadTuples(ctx context.Context, filter language.TupleFilter) ([]language.Tuple, error)
}

// EventPlan is what one event changes.
type EventPlan struct {
	Index   int    // 1-based
	Type    string // event["type"] when it is a string
	Matched bool   // some rule's `when` matched
	// Changes are the event's deletes followed by its writes, each group in
	// mapper's order. Action is write or delete.
	Changes []language.Tuple
	// Atomic means the event goes out as one Write request: it fits in one
	// request and no tuple is both deleted and written (OpenFGA rejects that).
	Atomic bool
	// Removals are what the event asked to be gone from the store; nil when
	// it asked for nothing.
	Removals []Removal
}

// Removal is something an event asked to be gone from the store, whether or
// not it was there: a delete tuple, or a tuple filter with the tuples its
// own rule keeps.
type Removal struct {
	// Filter is the tuple filter; a delete tuple is one naming its user,
	// relation and object.
	Filter language.TupleFilter
	// Keep holds a patch filter's own tuples, which it does not remove.
	Keep []language.Tuple
}

// Removes reports whether r asks for t to be gone.
func (r Removal) Removes(t language.Tuple) bool {
	if !matchesFilter(t, r.Filter) {
		return false
	}
	return !slices.ContainsFunc(r.Keep, func(k language.Tuple) bool { return URO(k) == URO(t) })
}

// removalsOf lists what res asks to be gone.
func removalsOf(res *mapper.Result) []Removal {
	var out []Removal
	for _, t := range res.Tuples {
		if t.Action == language.ActionDelete {
			out = append(out, Removal{Filter: language.TupleFilter{User: t.User, Relation: t.Relation, Object: t.Object, Action: language.FilterActionDelete}})
		}
	}
	for _, op := range res.TupleFilterOperations {
		for _, f := range op.Filters {
			r := Removal{Filter: f}
			if f.Action == language.FilterActionPatch {
				r.Keep = op.Tuples
			}
			if f.Action == language.FilterActionDelete || f.Action == language.FilterActionPatch {
				out = append(out, r)
			}
		}
	}
	return out
}

// Requests is how many Write requests Execute sends for the event: one when
// it is atomic, otherwise its deletes and then its writes, each in chunks of
// maxChangesPerWrite. An event that changes nothing sends none.
func (e EventPlan) Requests() int {
	if len(e.Changes) == 0 {
		return 0
	}
	if e.Atomic {
		return 1
	}
	var writes, deletes int
	for _, c := range e.Changes {
		if c.Action == language.ActionDelete {
			deletes++
		} else {
			writes++
		}
	}
	chunks := func(n int) int { return (n + maxChangesPerWrite - 1) / maxChangesPerWrite }
	return chunks(deletes) + chunks(writes)
}

// Plan is the ordered changes a list of events makes to a store.
type Plan struct {
	Events          []EventPlan
	Writes, Deletes int
	Unmatched       int
}

// PlanEvents evaluates events in order with mapper and runs each result
// through mapper's own reconciler against a recording client. Reads see the
// store plus the changes planned for earlier events; writes are recorded,
// never sent. m must be compiled with tracing (CompileSource does), since
// Matched comes from the trace. Any event that fails fails the whole plan.
//
// from is the 1-based number of the first event to plan: the ones before it
// are skipped entirely (a resumed apply), and every event keeps its number in
// events.
func PlanEvents(ctx context.Context, m *mapper.Mapping, events []map[string]any, from int, store TupleReader, progress func(done, total int)) (*Plan, error) {
	planner := NewPlanner(m, store)
	p := &Plan{}
	from = max(from, 1)
	for i := from - 1; i < len(events); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ep, err := planner.Add(ctx, i+1, events[i])
		if err != nil {
			return nil, err
		}
		for _, c := range ep.Changes {
			if c.Action == language.ActionDelete {
				p.Deletes++
			} else {
				p.Writes++
			}
		}
		if !ep.Matched {
			p.Unmatched++
		}
		p.Events = append(p.Events, ep)
		if progress != nil {
			progress(i+2-from, len(events)+1-from)
		}
	}
	return p, nil
}

// Planner plans events one at a time. Each event's reads see the store plus
// the changes planned for the events before it.
type Planner struct {
	m     *mapper.Mapping
	rec   *recorder
	state PlanState
}

// NewPlanner returns a Planner for m against store. m must be compiled with
// tracing, as for PlanEvents.
func NewPlanner(m *mapper.Mapping, store TupleReader) *Planner {
	return NewPlannerWith(m, store, NewMapState())
}

// NewPlannerWith is NewPlanner with the planned changes kept in state.
func NewPlannerWith(m *mapper.Mapping, store TupleReader, state PlanState) *Planner {
	return &Planner{
		m:     m,
		rec:   &recorder{store: store, cache: map[language.TupleFilter][]language.Tuple{}, state: state},
		state: state,
	}
}

// Add plans the next event. index is the event's 1-based number, kept in the
// EventPlan and in errors. When the error is not nil the EventPlan holds only
// what was known when it failed: the Index and Type, Matched once the rules
// were evaluated and, for a write that conflicts with an earlier one, the
// Changes. Removals is nil and the Changes are not planned for later reads.
func (p *Planner) Add(ctx context.Context, index int, event map[string]any) (EventPlan, error) {
	ep := EventPlan{Index: index}
	ep.Type, _ = event["type"].(string)
	res, err := p.m.Evaluate(ctx, event)
	if err != nil {
		return ep, eventErr(ep, err)
	}
	if res.Trace != nil {
		for _, rt := range res.Trace.Rules {
			ep.Matched = ep.Matched || rt.Status == mapper.RuleMatched
		}
	}
	p.rec.got = nil
	if err := apply.New(p.rec).Execute(ctx, res); err != nil {
		return ep, eventErr(ep, err)
	}
	ep.Changes, ep.Atomic = deletesFirst(p.rec.got)
	if err := trackWrites(ctx, ep, p.state); err != nil {
		return ep, err
	}
	ep.Removals = removalsOf(res)
	return ep, nil
}

// deletesFirst stably partitions changes into deletes then writes, and
// reports whether they can go out as one Write request.
func deletesFirst(changes []language.Tuple) ([]language.Tuple, bool) {
	out := make([]language.Tuple, 0, len(changes))
	deleted := map[string]bool{}
	for _, c := range changes {
		if c.Action == language.ActionDelete {
			out = append(out, c)
			deleted[URO(c)] = true
		}
	}
	atomic := true
	for _, c := range changes {
		if c.Action != language.ActionDelete {
			out = append(out, c)
			if deleted[URO(c)] {
				atomic = false
			}
		}
	}
	return out, atomic && len(out) <= maxChangesPerWrite
}

// trackWrites records ep's changes in state, failing an event that writes a
// tuple an earlier event wrote with a different condition or context when no
// delete came in between. OpenFGA keeps one tuple per user, relation and
// object, so that second write would fail at apply on every run. Only a rule
// without a tuple_filter plans it: a filter reads the earlier write and
// replaces it. A planned delete counts as no write. Nothing is recorded when
// the event fails.
func trackWrites(ctx context.Context, ep EventPlan, state PlanState) error {
	own := map[string]PlannedChange{} // the event's own earlier changes
	planned := make([]PlannedChange, 0, len(ep.Changes))
	for _, c := range ep.Changes {
		k := URO(c)
		pc := PlannedChange{Tuple: c, Event: ep.Index}
		planned = append(planned, pc)
		if c.Action == language.ActionDelete {
			own[k] = pc
			continue
		}
		c.Action = ""
		prev, ok := own[k]
		if !ok {
			var err error
			if prev, ok, err = state.Last(ctx, k); err != nil {
				return err
			}
		}
		if ok && prev.Tuple.Action != language.ActionDelete && prev.Event != ep.Index && keyOf(prev.Tuple) != c.Key() {
			return eventErr(ep, fmt.Errorf("writes %s %s %s with a different condition or context than event %d; "+
				"OpenFGA keeps one tuple per user, relation and object and rejects the second write — "+
				"give the rule a tuple_filter so it replaces the tuple", c.User, c.Relation, c.Object, prev.Event))
		}
		own[k] = pc
	}
	return state.Plan(ctx, planned)
}

// keyOf is t.Key() without the action, the way a write is compared.
func keyOf(t language.Tuple) string {
	t.Action = ""
	return t.Key()
}

// recorder is the apply.TupleClient the plan runs mapper's reconciliation
// against: reads see the store as it will be after the events planned so far,
// writes are recorded instead of sent.
type recorder struct {
	store TupleReader
	cache map[language.TupleFilter][]language.Tuple // store reads; the store does not change while planning
	state PlanState                                 // the changes planned so far
	got   []language.Tuple
}

func (r *recorder) ReadTuples(ctx context.Context, f language.TupleFilter) ([]language.Tuple, error) {
	key := language.TupleFilter{User: f.User, Relation: f.Relation, Object: f.Object}
	base, ok := r.cache[key]
	if !ok {
		var err error
		if base, err = r.store.ReadTuples(ctx, key); err != nil {
			return nil, err
		}
		r.cache[key] = base
	}
	byURO := map[string]language.Tuple{}
	for _, t := range base {
		byURO[URO(t)] = t
	}
	planned, err := r.state.Matching(ctx, key)
	if err != nil {
		return nil, err
	}
	for _, pc := range planned {
		t := pc.Tuple
		if t.Action == language.ActionDelete {
			delete(byURO, URO(t))
		} else {
			t.Action = ""
			byURO[URO(t)] = t
		}
	}
	out := make([]language.Tuple, 0, len(byURO))
	for _, t := range byURO {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return URO(out[i]) < URO(out[j]) })
	return out, nil
}

func (r *recorder) WriteTuples(_ context.Context, ts []language.Tuple) error {
	r.got = append(r.got, ts...)
	return nil
}

// URO is the key of a tuple's user, relation and object.
func URO(t language.Tuple) string { return t.User + "\x00" + t.Relation + "\x00" + t.Object }

// EventError is a failure to plan one event: its 1-based number, its type when
// it has one, and the cause.
type EventError struct {
	Index int
	Type  string
	Err   error
}

func (e *EventError) Error() string {
	if e.Type == "" {
		return fmt.Sprintf("event %d: %v", e.Index, e.Err)
	}
	return fmt.Sprintf("event %d (%s): %v", e.Index, e.Type, e.Err)
}

func (e *EventError) Unwrap() error { return e.Err }

func eventErr(ep EventPlan, err error) error {
	return &EventError{Index: ep.Index, Type: ep.Type, Err: err}
}
