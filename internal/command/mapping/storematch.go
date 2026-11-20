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
// on the first attempt to about the cost of the pair reads. onPairs is called
// with the number of distinct objects just before the pair reads start.
func (s storeClient) ReadMatching(ctx context.Context, want []language.Tuple, onPairs func(objects int)) (storeRead, error) {
	ids := make(map[string]bool, len(want))
	type pair struct{ object, relation string }
	var pairs []pair
	seenPair := map[pair]bool{}
	objects := map[string]bool{}
	for _, t := range want {
		ids[snapshot.Identity(t)] = true
		p := pair{t.Object, t.Relation}
		if !seenPair[p] {
			seenPair[p] = true
			pairs = append(pairs, p)
		}
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
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(storeWorkers)
	for _, p := range pairs {
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			var got []language.Tuple
			n := 0
			for t, err := range s.cl.Tuples.ReadAll(gctx, &openfga.ReadRequest{
				TupleKey: &openfga.ReadRequestTupleKey{Object: p.object, Relation: p.relation}, PageSize: readPageSize,
			}) {
				if err != nil {
					return err
				}
				n++
				if lt := toTuple(t); ids[snapshot.Identity(lt)] {
					got = append(got, lt)
				}
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
	return res, nil
}
