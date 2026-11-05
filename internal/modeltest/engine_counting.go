package modeltest

import (
	"context"
	"sync/atomic"

	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/openfga/pkg/storage"
)

// Counters tallies datastore tuple reads. The caller resets it between
// checks; Reads reports the total since the last Reset. Only the four
// Check-path tuple reads are counted: Read, ReadUserTuple,
// ReadUsersetTuples and ReadStartingWithUser.
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

// count tallies one read on the engine-wide counter and on the counter of
// the check that issued it, when its context carries one.
func (d countingDatastore) count(ctx context.Context) {
	d.c.reads.Add(1)
	if n, ok := ctx.Value(readCountKey{}).(*atomic.Int64); ok {
		n.Add(1)
	}
}

func (d countingDatastore) Read(ctx context.Context, store string, filter storage.ReadFilter, opts storage.ReadOptions) (storage.TupleIterator, error) {
	d.count(ctx)
	return d.OpenFGADatastore.Read(ctx, store, filter, opts)
}

func (d countingDatastore) ReadUserTuple(ctx context.Context, store string, filter storage.ReadUserTupleFilter, opts storage.ReadUserTupleOptions) (*openfgav1.Tuple, error) {
	d.count(ctx)
	return d.OpenFGADatastore.ReadUserTuple(ctx, store, filter, opts)
}

func (d countingDatastore) ReadUsersetTuples(ctx context.Context, store string, filter storage.ReadUsersetTuplesFilter, opts storage.ReadUsersetTuplesOptions) (storage.TupleIterator, error) {
	d.count(ctx)
	return d.OpenFGADatastore.ReadUsersetTuples(ctx, store, filter, opts)
}

func (d countingDatastore) ReadStartingWithUser(ctx context.Context, store string, filter storage.ReadStartingWithUserFilter, opts storage.ReadStartingWithUserOptions) (storage.TupleIterator, error) {
	d.count(ctx)
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

type readCountKey struct{}

// WithReadCount returns a ctx that charges the datastore reads a Check issues
// under it to a counter of its own, plus a func that reads that counter.
// Unlike Counters, it never counts a read from another check, such as one
// an earlier check's abandoned branch issues late.
func WithReadCount(ctx context.Context) (context.Context, func() int64) {
	n := &atomic.Int64{}
	return context.WithValue(ctx, readCountKey{}, n), n.Load
}
