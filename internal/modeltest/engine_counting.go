package modeltest

import (
	"context"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"sync/atomic"

	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/openfga/openfga/pkg/storage"
)

// Counters tallies datastore tuple reads. The caller resets it between
// checks; Reads reports the total since the last Reset.
type Counters struct{ reads atomic.Int64 }

// Reset zeroes the counters.
func (c *Counters) Reset() { c.reads.Store(0) }

// Reads returns the number of tuple reads since the last Reset.
func (c *Counters) Reads() int64 { return c.reads.Load() }

// countingDatastore counts the tuple-read calls a Check issues.
type countingDatastore struct {
	storage.OpenFGADatastore
	c *Counters
}

func (d countingDatastore) Read(ctx context.Context, store string, filter storage.ReadFilter, opts storage.ReadOptions) (storage.TupleIterator, error) {
	d.c.reads.Add(1)
	return d.OpenFGADatastore.Read(ctx, store, filter, opts)
}

func (d countingDatastore) ReadUserTuple(ctx context.Context, store string, filter storage.ReadUserTupleFilter, opts storage.ReadUserTupleOptions) (*openfgav1.Tuple, error) {
	d.c.reads.Add(1)
	return d.OpenFGADatastore.ReadUserTuple(ctx, store, filter, opts)
}

func (d countingDatastore) ReadUsersetTuples(ctx context.Context, store string, filter storage.ReadUsersetTuplesFilter, opts storage.ReadUsersetTuplesOptions) (storage.TupleIterator, error) {
	d.c.reads.Add(1)
	return d.OpenFGADatastore.ReadUsersetTuples(ctx, store, filter, opts)
}

func (d countingDatastore) ReadStartingWithUser(ctx context.Context, store string, filter storage.ReadStartingWithUserFilter, opts storage.ReadStartingWithUserOptions) (storage.TupleIterator, error) {
	d.c.reads.Add(1)
	return d.OpenFGADatastore.ReadStartingWithUser(ctx, store, filter, opts)
}

// NewCountingEmbeddedEngine is NewEmbeddedEngine with a datastore wrapper
// that counts tuple reads into the returned Counters.
func NewCountingEmbeddedEngine(serverOpts map[string]any) (Engine, *Counters, error) {
	c := &Counters{}
	eng, err := newEmbeddedEngine(serverOpts, func(ds storage.OpenFGADatastore) storage.OpenFGADatastore {
		return countingDatastore{OpenFGADatastore: ds, c: c}
	})
	if err != nil {
		return nil, nil, err
	}
	return eng, c, nil
}

// WithDispatchTags returns a ctx carrying fresh grpc_ctxtags, plus a func
// that reads the "dispatch_count" the server recorded after a Check (0 if
// absent).
func WithDispatchTags(ctx context.Context) (context.Context, func() float64) {
	tags := grpc_ctxtags.NewTags()
	ctx = grpc_ctxtags.SetInContext(ctx, tags)
	return ctx, func() float64 {
		v, _ := tags.Values()["dispatch_count"].(float64)
		return v
	}
}
