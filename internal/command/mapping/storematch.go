package mapping

import (
	"context"
	"sync"

	"github.com/openfga/mapper/language"
	"github.com/sergiught/go-openfga/openfga"
	"golang.org/x/sync/errgroup"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// storeWorkers is how many pair reads run at once when backfill reads the
// store object by object.
const storeWorkers = 8

// matchProgress is one report of ReadMatching. Step is "store" while the
// whole store is paged (Done is tuples read, Total 0), "objects" during the
// per-object reads and "keys" during the full-key reads (Done of Total reads).
// Objects is the number of distinct objects, set on "objects" reports.
type matchProgress struct {
	Step        string
	Done, Total int
	Objects     int
}

// storeRead is what ReadMatching found. Objects is zero when the whole store
// was read; otherwise it is how many distinct objects were read one by one.
type storeRead struct {
	Tuples  []language.Tuple
	Read    int
	Objects int
}

// ReadMatching returns the stored tuples that share an identity (user,
// relation, object) with a wanted one. It wraps ReadMatchingFrom with a
// slice-backed Wanted and a sink that keeps the tuples whose identity is
// wanted, once each; see ReadMatchingFrom for how the store is read.
func (s storeClient) ReadMatching(ctx context.Context, want []language.Tuple, progress func(matchProgress)) (storeRead, error) {
	ids := make(map[string]bool, len(want))
	for _, t := range want {
		ids[snapshot.Identity(t)] = false
	}
	var res storeRead
	read, objects, err := s.ReadMatchingFrom(ctx, snapshot.SliceWanted(want), func(ts []language.Tuple) error {
		for _, t := range ts {
			if id := snapshot.Identity(t); seenWanted(ids, id) {
				res.Tuples = append(res.Tuples, t)
			}
		}
		return nil
	}, progress)
	if err != nil {
		return storeRead{}, err
	}
	res.Read, res.Objects = read, objects
	return res, nil
}

// seenWanted reports whether id is wanted and not yet taken, and marks it
// taken: the same tuple can reach the sink twice.
func seenWanted(ids map[string]bool, id string) bool {
	taken, wanted := ids[id]
	if !wanted || taken {
		return false
	}
	ids[id] = true
	return true
}

// ReadMatchingFrom reads the stored tuples that may share an identity (user,
// relation, object) with a wanted one and hands what it reads to sink, which
// may be nil. It pages through the whole store, which costs one request when
// the store is small. When the store is still not exhausted after a budget of
// pages, it reads each distinct (object, relation) of the wanted tuples
// instead, storeWorkers at a time, so a store far larger than the mapping's
// data is not read whole. The budget keeps the pages wasted on the first
// attempt to about the cost of the pair reads. A pair read stops after a few
// pages too: when an object holds far more tuples than are wanted from it, each
// wanted tuple is read by its full key instead, walking want.Keys for that
// pair.
//
// The sink gets every tuple read, wanted or not, in batches, and is never
// called concurrently. A tuple can reach it twice (a page of the whole store,
// then the pair read that covers it), so it must accept repeats. read is how
// many tuples the final strategy read; objects is zero when the whole store
// was read, otherwise how many distinct objects were read one by one. progress
// may be nil; it is never called concurrently.
func (s storeClient) ReadMatchingFrom(ctx context.Context, want snapshot.Wanted, sink func([]language.Tuple) error, progress func(matchProgress)) (read, objects int, err error) {
	if sink == nil {
		sink = func([]language.Tuple) error { return nil }
	}
	pairs, err := want.Pairs(ctx)
	if err != nil {
		return 0, 0, err
	}
	distinct := map[string]bool{}
	for _, p := range pairs {
		distinct[p.Object] = true
	}
	nobjects := len(distinct)
	budget := max(1, (len(pairs)+storeWorkers-1)/storeWorkers)

	req := &openfga.ReadRequest{PageSize: readPageSize, Consistency: readConsistency}
	for page := 1; ; page++ {
		resp, err := s.cl.Tuples.Read(ctx, req)
		if err != nil {
			return 0, 0, err
		}
		read += len(resp.Tuples)
		if err := sink(toTuples(resp.Tuples)); err != nil {
			return 0, 0, err
		}
		if progress != nil {
			progress(matchProgress{Step: "store", Done: read})
		}
		if resp.ContinuationToken == "" {
			return read, 0, nil
		}
		if page >= budget {
			break
		}
		req.ContinuationToken = resp.ContinuationToken
	}

	if progress != nil {
		progress(matchProgress{Step: "objects", Total: len(pairs), Objects: nobjects})
	}
	read = 0
	var (
		mu        sync.Mutex // also orders the sink and the progress reports
		crowded   []snapshot.Pair
		pairsDone int
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(storeWorkers)
	for _, p := range pairs {
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			// A pair wanted by few tuples is read for few pages. When the
			// last of them still continues, the object holds far more than
			// is wanted and each wanted tuple is read by its full key later.
			pages := max(1, (p.N+storeWorkers-1)/storeWorkers)
			req := &openfga.ReadRequest{
				TupleKey: &openfga.ReadRequestTupleKey{Object: p.Object, Relation: p.Relation}, PageSize: readPageSize,
				Consistency: readConsistency,
			}
			n := 0
			for page := 1; ; page++ {
				resp, err := s.cl.Tuples.Read(gctx, req)
				if err != nil {
					return err
				}
				n += len(resp.Tuples)
				mu.Lock()
				err = sink(toTuples(resp.Tuples))
				mu.Unlock()
				if err != nil {
					return err
				}
				if resp.ContinuationToken == "" {
					break
				}
				if page >= pages {
					// The key reads fetch the wanted tuples again.
					mu.Lock()
					crowded = append(crowded, p)
					mu.Unlock()
					break
				}
				req.ContinuationToken = resp.ContinuationToken
			}
			mu.Lock()
			read += n
			pairsDone++
			if progress != nil {
				progress(matchProgress{Step: "objects", Done: pairsDone, Total: len(pairs), Objects: nobjects})
			}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	// A canceled context stops the loop above without any read failing, so
	// what was read so far is only part of the store.
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}

	// Started only after the pair phase, so no goroutine of one errgroup
	// starts work on the same group.
	keysDone, keysTotal := 0, 0
	for _, p := range crowded {
		keysTotal += p.N
	}
	if progress != nil && keysTotal > 0 {
		progress(matchProgress{Step: "keys", Total: keysTotal})
	}
	g, gctx = errgroup.WithContext(ctx)
	g.SetLimit(storeWorkers)
	var walkErr error
	for _, p := range crowded {
		walkErr = want.Keys(gctx, p.Object, p.Relation, func(t language.Tuple) error {
			if err := gctx.Err(); err != nil {
				return err
			}
			g.Go(func() error {
				resp, err := s.cl.Tuples.Read(gctx, &openfga.ReadRequest{
					TupleKey: &openfga.ReadRequestTupleKey{User: t.User, Relation: t.Relation, Object: t.Object}, PageSize: readPageSize,
					Consistency: readConsistency,
				})
				if err != nil {
					return err
				}
				mu.Lock()
				defer mu.Unlock()
				if err := sink(toTuples(resp.Tuples)); err != nil {
					return err
				}
				read += len(resp.Tuples)
				keysDone++
				if progress != nil {
					progress(matchProgress{Step: "keys", Done: keysDone, Total: keysTotal})
				}
				return nil
			})
			return nil
		})
		if walkErr != nil {
			break
		}
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if walkErr != nil {
		return 0, 0, walkErr
	}
	// A canceled context stops the loop above without any read failing, so
	// what was read so far is only part of the store.
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	return read, nobjects, nil
}

func toTuples(ts []openfga.Tuple) []language.Tuple {
	out := make([]language.Tuple, len(ts))
	for i, t := range ts {
		out[i] = toTuple(t)
	}
	return out
}
