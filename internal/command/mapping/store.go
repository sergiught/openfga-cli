package mapping

import (
	"context"
	"fmt"

	"github.com/openfga/mapper/language"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/fga"
)

// storeClient reads and writes the profile's store for apply. It is the only
// place apply touches go-openfga, so the engine stays testable with fakes.
type storeClient struct{ cl *openfga.Client }

// readPageSize is the most tuples OpenFGA returns for one read request. The
// server's default is 50, so asking for the maximum halves the requests.
const readPageSize = 100

func toTuple(t openfga.Tuple) language.Tuple {
	lt := language.Tuple{User: t.Key.User, Relation: t.Key.Relation, Object: t.Key.Object}
	if c := t.Key.Condition; c != nil {
		lt.Condition, lt.Context = c.Name, c.Context
	}
	return lt
}

func (s storeClient) ReadTuples(ctx context.Context, f language.TupleFilter) ([]language.Tuple, error) {
	rf := fga.ReadFilter{User: f.User, Relation: f.Relation, Object: f.Object}
	if err := rf.Validate(); err != nil {
		return nil, fmt.Errorf("tuple filter %s %s %s: %w", f.User, f.Relation, f.Object, err)
	}
	var out []language.Tuple
	for t, err := range s.cl.Tuples.ReadAll(ctx, &openfga.ReadRequest{TupleKey: rf.TupleKey(), PageSize: readPageSize}) {
		if err != nil {
			return nil, err
		}
		out = append(out, toTuple(t))
	}
	return out, nil
}

// ReadAll reads every tuple in the store.
func (s storeClient) ReadAll(ctx context.Context) ([]language.Tuple, error) {
	return s.ReadTuples(ctx, language.TupleFilter{})
}

// ReadKept reads the whole store and keeps only the tuples keep accepts, so a
// large store is not held in memory. It also returns how many tuples it read.
// progress may be nil; it is called after every readPageSize tuples read and
// once at the end.
func (s storeClient) ReadKept(ctx context.Context, keep func(language.Tuple) bool, progress func(read, kept int)) (kept []language.Tuple, total int, err error) {
	for t, err := range s.cl.Tuples.ReadAll(ctx, &openfga.ReadRequest{PageSize: readPageSize}) {
		if err != nil {
			return nil, 0, err
		}
		total++
		if lt := toTuple(t); keep(lt) {
			kept = append(kept, lt)
		}
		if progress != nil && total%readPageSize == 0 {
			progress(total, len(kept))
		}
	}
	if progress != nil {
		progress(total, len(kept))
	}
	return kept, total, nil
}

func (s storeClient) Write(ctx context.Context, writes, deletes []language.Tuple) error {
	req := &openfga.WriteRequest{}
	if len(writes) > 0 {
		req.Writes = &openfga.WriteRequestTuples{TupleKeys: tupleKeys(writes, true), OnDuplicate: openfga.OnDuplicateIgnore}
	}
	if len(deletes) > 0 {
		// A delete names the tuple; the server ignores any condition on it.
		req.Deletes = &openfga.WriteRequestTuples{TupleKeys: tupleKeys(deletes, false), OnMissing: openfga.OnMissingIgnore}
	}
	return s.cl.Tuples.Write(ctx, req)
}

func tupleKeys(ts []language.Tuple, withCondition bool) []openfga.TupleKey {
	keys := make([]openfga.TupleKey, 0, len(ts))
	for _, t := range ts {
		k := openfga.NewTupleKey(t.User, t.Relation, t.Object)
		if withCondition && t.Condition != "" {
			k.Condition = &openfga.RelationshipCondition{Name: t.Condition, Context: t.Context}
		}
		keys = append(keys, k)
	}
	return keys
}
