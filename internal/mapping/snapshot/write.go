package snapshot

import (
	"context"
	"errors"
	"time"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// maxChangesPerWrite is OpenFGA's limit on tuples in one Write request.
const maxChangesPerWrite = 100

// replaceTimeout bounds the write and restore of a batch Replace has already
// started deleting; Ctrl-C does not cancel them, so no batch is left half done.
const replaceTimeout = 30 * time.Second

// Sent is how many tuples reached the store.
type Sent struct{ Writes, Deletes int }

// Write sends deletes, then writes, in requests of at most 100 changes, each
// request all deletes or all writes. It stops at the first error or at
// cancellation (checked before each request) and returns what was sent.
// progress counts changes sent so far out of all of them; it may be nil.
func Write(ctx context.Context, w mapping.TupleWriter, deletes, writes []language.Tuple, progress func(done, total int)) (Sent, error) {
	var sent Sent
	total := len(deletes) + len(writes)
	done := 0
	send := func(ts []language.Tuple, isDelete bool) error {
		for len(ts) > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := min(len(ts), maxChangesPerWrite)
			var err error
			if isDelete {
				err = w.Write(ctx, nil, ts[:n])
			} else {
				err = w.Write(ctx, ts[:n], nil)
			}
			if err != nil {
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
// request. If the write fails the stored versions are written back. It
// returns what was sent and, only when that restore failed too, the stored
// versions of the tuples now missing from the store. progress counts changes
// sent so far out of all of them; it may be nil.
func Replace(ctx context.Context, w mapping.TupleWriter, was, now []language.Tuple, progress func(done, total int)) (Sent, []language.Tuple, error) {
	var sent Sent
	total, done := 2*len(now), 0
	for len(now) > 0 {
		if err := ctx.Err(); err != nil {
			return sent, nil, err
		}
		n := min(len(now), maxChangesPerWrite)
		if err := w.Write(ctx, nil, was[:n]); err != nil {
			return sent, nil, err
		}
		// Past the delete the batch must finish whatever the context says.
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), replaceTimeout)
		err := w.Write(bctx, now[:n], nil)
		if err != nil {
			rerr := w.Write(bctx, was[:n], nil)
			cancel()
			if rerr != nil {
				sent.Deletes += n
				return sent, was[:n], errors.Join(err, rerr)
			}
			return sent, nil, err
		}
		cancel()
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
