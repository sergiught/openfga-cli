package snapshot

import (
	"context"
	"errors"
	"time"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// maxChangesPerWrite is OpenFGA's default limit on tuples in one Write
// request. A store configured with a lower limit makes Write and Replace
// use fewer.
const maxChangesPerWrite = 100

// BatchLimiter is a TupleWriter that can tell when the store refused a
// request for carrying too many changes.
type BatchLimiter interface {
	// BatchLimit reports whether err is that refusal, and the limit the
	// store named, or 0 when it named none.
	BatchLimit(err error) (limit int, ok bool)
}

// smallerBatch returns the size to retry with after w failed a request of n
// changes with err: the limit the store named when that is smaller than n,
// else half of n. ok is false when err is not a size refusal, w cannot tell,
// or n is already 1.
func smallerBatch(w mapping.TupleWriter, err error, n int) (size int, ok bool) {
	bl, isLimiter := w.(BatchLimiter)
	if !isLimiter || n < 2 {
		return 0, false
	}
	limit, ok := bl.BatchLimit(err)
	if !ok {
		return 0, false
	}
	if 0 < limit && limit < n {
		return limit, true
	}
	return n / 2, true
}

// replaceTimeout bounds each request of a batch Replace has started. Ctrl-C
// does not cancel them, so no batch is left half done.
const replaceTimeout = 30 * time.Second

// Sent is how many tuples reached the store.
type Sent struct{ Writes, Deletes int }

// Write sends deletes, then writes, in requests of at most 100 changes, each
// request all deletes or all writes. When w is a BatchLimiter and the store
// refuses a request as too large, the request is sent again in smaller ones,
// and every later request keeps the smaller size. Otherwise it stops at the
// first error or at cancellation (checked before each request) and returns
// what was sent.
// progress counts changes sent so far out of all of them; it may be nil.
func Write(ctx context.Context, w mapping.TupleWriter, deletes, writes []language.Tuple, progress func(done, total int)) (Sent, error) {
	var sent Sent
	total := len(deletes) + len(writes)
	done := 0
	size := maxChangesPerWrite
	send := func(ts []language.Tuple, isDelete bool) error {
		for len(ts) > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := min(len(ts), size)
			var err error
			if isDelete {
				err = w.Write(ctx, nil, ts[:n])
			} else {
				err = w.Write(ctx, ts[:n], nil)
			}
			if err != nil {
				if smaller, ok := smallerBatch(w, err, n); ok {
					size = smaller
					continue
				}
				return err
			}
			if isDelete {
				sent.Deletes += n
			} else {
				sent.Writes += n
			}
			done += n
			ts = ts[n:]
			if progress != nil {
				progress(done, total)
			}
		}
		return nil
	}
	if err := send(deletes, true); err != nil {
		return sent, err
	}
	return sent, send(writes, false)
}

// Replace swaps each stored tuple for its desired version: was[i] for now[i].
// OpenFGA cannot overwrite a condition and rejects a request that deletes and
// writes the same tuple, so each batch is a delete request then a write
// request. When w is a BatchLimiter and the store refuses a delete request as
// too large, nothing was applied, so the batch is sent again in a smaller
// size, kept for the later batches. If any other request fails, or the write
// request is refused, the stored versions are written back; w must
// ignore a written tuple that is already there, because a delete the client
// saw fail may not have reached the store. It returns what was sent and,
// only when that restore failed too, the stored versions of the tuples the
// store may now be without: Sent counts their delete when it went through.
// progress counts changes sent so far out of all of them; it may be nil.
func Replace(ctx context.Context, w mapping.TupleWriter, was, now []language.Tuple, progress func(done, total int)) (Sent, []language.Tuple, error) {
	// A request the client gives up on can still be applied by the store, so
	// once a batch has started the context no longer cancels its requests.
	step := func(writes, deletes []language.Tuple) error {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), replaceTimeout)
		defer cancel()
		return w.Write(c, writes, deletes)
	}
	var sent Sent
	total, done := 2*len(now), 0
	size := maxChangesPerWrite
	for len(now) > 0 {
		if err := ctx.Err(); err != nil {
			return sent, nil, err
		}
		n := min(len(now), size)
		err := step(nil, was[:n])
		if err != nil {
			if smaller, ok := smallerBatch(w, err, n); ok {
				size = smaller
				continue
			}
		}
		deleted := err == nil
		if deleted {
			err = step(now[:n], nil)
		}
		if err != nil {
			rerr := step(was[:n], nil)
			if rerr == nil {
				return sent, nil, err
			}
			if deleted {
				sent.Deletes += n
			}
			return sent, was[:n], errors.Join(err, rerr)
		}
		sent.Deletes += n
		sent.Writes += n
		done += 2 * n
		was, now = was[n:], now[n:]
		if progress != nil {
			progress(done, total)
		}
	}
	return sent, nil, nil
}
