package snapshot

import (
	"context"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// maxChangesPerWrite is OpenFGA's limit on tuples in one Write request.
const maxChangesPerWrite = 100

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
