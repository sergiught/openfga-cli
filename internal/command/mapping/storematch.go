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

// storeRead is what ReadMatching found. Objects is zero when the whole store
// was read; otherwise it is how many distinct objects were read one by one.
type storeRead struct {
	Tuples  []language.Tuple
	Read    int
	Objects int
}

// ReadMatching returns the stored tuples that share an identity (user,
// relation, object) with a wanted one. It pages through the whole store, which
// costs one request when the store is small. When the store is still not
// exhausted after a budget of pages, it reads each distinct (object, relation)
// of the wanted tuples instead, storeWorkers at a time, so a store far larger
// than the mapping's data is not read whole. The budget keeps the pages wasted
// on the first attempt to about the cost of the pair reads. A pair read stops
// after a few pages too: when an object holds far more tuples than are wanted
// from it, each wanted tuple is read by its full key instead. onPairs is called
// with the number of distinct objects just before the pair reads start.
func (s storeClient) ReadMatching(ctx context.Context, want []language.Tuple, onPairs func(objects int)) (storeRead, error) {
	ids := make(map[string]bool, len(want))
	type pair struct{ object, relation string }
	var pairs []pair
	wanted := map[pair][]language.Tuple{}
	objects := map[string]bool{}
	for _, t := range want {
		id := snapshot.Identity(t)
		if ids[id] {
			continue
		}
		ids[id] = true
		p := pair{t.Object, t.Relation}
		if _, ok := wanted[p]; !ok {
			pairs = append(pairs, p)
		}
		wanted[p] = append(wanted[p], t)
		objects[t.Object] = true
	}
	budget := max(1, (len(pairs)+storeWorkers-1)/storeWorkers)

	var res storeRead
	req := &openfga.ReadRequest{PageSize: readPageSize}
	for page := 1; ; page++ {
		resp, err := s.cl.Tuples.Read(ctx, req)
		if err != nil {
			return storeRead{}, err
		}
		res.Read += len(resp.Tuples)
		for _, t := range resp.Tuples {
			if lt := toTuple(t); ids[snapshot.Identity(lt)] {
				res.Tuples = append(res.Tuples, lt)
			}
		}
		if resp.ContinuationToken == "" {
			return res, nil
		}
		if page >= budget {
			break
		}
		req.ContinuationToken = resp.ContinuationToken
	}

	if onPairs != nil {
		onPairs(len(objects))
	}
	res = storeRead{Objects: len(objects)}
	var (
		mu      sync.Mutex
		crowded []language.Tuple
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
			pages := max(1, (len(wanted[p])+storeWorkers-1)/storeWorkers)
			req := &openfga.ReadRequest{
				TupleKey: &openfga.ReadRequestTupleKey{Object: p.object, Relation: p.relation}, PageSize: readPageSize,
			}
			var got []language.Tuple
			n := 0
			for page := 1; ; page++ {
				resp, err := s.cl.Tuples.Read(gctx, req)
				if err != nil {
					return err
				}
				n += len(resp.Tuples)
				for _, t := range resp.Tuples {
					if lt := toTuple(t); ids[snapshot.Identity(lt)] {
						got = append(got, lt)
					}
				}
				if resp.ContinuationToken == "" {
					break
				}
				if page >= pages {
					// The key reads fetch the wanted tuples again, so the
					// pages read here are not kept.
					got = nil
					mu.Lock()
					crowded = append(crowded, wanted[p]...)
					mu.Unlock()
					break
				}
				req.ContinuationToken = resp.ContinuationToken
			}
			mu.Lock()
			res.Tuples = append(res.Tuples, got...)
			res.Read += n
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return storeRead{}, err
	}

	// Started only after the pair phase, so no goroutine of one errgroup
	// starts work on the same group.
	g, gctx = errgroup.WithContext(ctx)
	g.SetLimit(storeWorkers)
	for _, t := range crowded {
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			resp, err := s.cl.Tuples.Read(gctx, &openfga.ReadRequest{
				TupleKey: &openfga.ReadRequestTupleKey{User: t.User, Relation: t.Relation, Object: t.Object}, PageSize: readPageSize,
			})
			if err != nil {
				return err
			}
			var got []language.Tuple
			for _, st := range resp.Tuples {
				if lt := toTuple(st); ids[snapshot.Identity(lt)] {
					got = append(got, lt)
				}
			}
			mu.Lock()
			res.Tuples = append(res.Tuples, got...)
			res.Read += len(resp.Tuples)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return storeRead{}, err
	}
	return res, nil
}
