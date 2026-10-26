package mapping

import (
	"context"
	"fmt"

	"github.com/openfga/mapper/language"
)

// TupleWriter writes tuples to a store in one request.
type TupleWriter interface {
	Write(ctx context.Context, writes, deletes []language.Tuple) error
}

// Execute applies a plan event by event, stopping at the first error or
// cancellation. An atomic event goes out as one Write request. A non-atomic
// event (a tuple is both deleted and written) sends its deletes, then its
// writes, each in chunks of maxChangesPerWrite. It returns how many events
// were fully applied. Cancellation is checked before each request, so one
// that lands after the last change still returns success: the plan is fully
// applied, and a resume would have nothing left to send.
func Execute(ctx context.Context, p *Plan, w TupleWriter, progress func(done, total int)) (int, error) {
	for i, ep := range p.Events {
		if err := applyEvent(ctx, w, ep); err != nil {
			return i, err
		}
		if progress != nil {
			progress(i+1, len(p.Events))
		}
	}
	return len(p.Events), nil
}

func applyEvent(ctx context.Context, w TupleWriter, ep EventPlan) error {
	if len(ep.Changes) == 0 {
		return nil
	}
	if ep.Atomic {
		return send(ctx, w, ep.Index, ep.Changes)
	}
	var deletes, writes []language.Tuple
	for _, c := range ep.Changes {
		if c.Action == language.ActionDelete {
			deletes = append(deletes, c)
		} else {
			writes = append(writes, c)
		}
	}
	for _, group := range [][]language.Tuple{deletes, writes} {
		for start := 0; start < len(group); start += maxChangesPerWrite {
			chunk := group[start:min(start+maxChangesPerWrite, len(group))]
			if err := send(ctx, w, ep.Index, chunk); err != nil {
				return err
			}
		}
	}
	return nil
}

// send makes one Write request carrying changes, split into writes and deletes.
func send(ctx context.Context, w TupleWriter, index int, changes []language.Tuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var writes, deletes []language.Tuple
	for _, c := range changes {
		if c.Action == language.ActionDelete {
			deletes = append(deletes, c)
		} else {
			writes = append(writes, c)
		}
	}
	if err := w.Write(ctx, writes, deletes); err != nil {
		return fmt.Errorf("event %d: %w", index, err)
	}
	return nil
}
