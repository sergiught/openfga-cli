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

func (s storeClient) ReadTuples(ctx context.Context, f language.TupleFilter) ([]language.Tuple, error) {
	rf := fga.ReadFilter{User: f.User, Relation: f.Relation, Object: f.Object}
	if err := rf.Validate(); err != nil {
		return nil, fmt.Errorf("tuple filter %s %s %s: %w", f.User, f.Relation, f.Object, err)
	}
	var out []language.Tuple
	for t, err := range s.cl.Tuples.ReadAll(ctx, &openfga.ReadRequest{TupleKey: rf.TupleKey()}) {
		if err != nil {
			return nil, err
		}
		lt := language.Tuple{User: t.Key.User, Relation: t.Key.Relation, Object: t.Key.Object}
		if c := t.Key.Condition; c != nil {
			lt.Condition, lt.Context = c.Name, c.Context
		}
		out = append(out, lt)
	}
	return out, nil
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
