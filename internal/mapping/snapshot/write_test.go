package snapshot

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/openfga/mapper/language"
)

type recWriter struct {
	reqs   [][2]int // writes, deletes per request
	failOn int      // 1-based request that fails; 0 never
}

func (r *recWriter) Write(_ context.Context, writes, deletes []language.Tuple) error {
	r.reqs = append(r.reqs, [2]int{len(writes), len(deletes)})
	if r.failOn == len(r.reqs) {
		return errors.New("boom")
	}
	return nil
}

func tuples(n int) []language.Tuple {
	out := make([]language.Tuple, n)
	for i := range out {
		out[i] = tup(fmt.Sprintf("user:%d", i), "member", "org:1")
	}
	return out
}

func TestWriteChunks(t *testing.T) {
	w := &recWriter{}
	var last [2]int
	sent, err := Write(context.Background(), w, tuples(120), tuples(250), func(d, tot int) { last = [2]int{d, tot} })
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if want := [][2]int{{0, 100}, {0, 20}, {100, 0}, {100, 0}, {50, 0}}; !reflect.DeepEqual(w.reqs, want) {
		t.Errorf("requests = %v, want %v", w.reqs, want)
	}
	if want := (Sent{Writes: 250, Deletes: 120}); sent != want {
		t.Errorf("sent = %+v, want %+v", sent, want)
	}
	if want := [2]int{370, 370}; last != want {
		t.Errorf("last progress = %v, want %v", last, want)
	}
}

func TestWriteStopsAndCounts(t *testing.T) {
	w := &recWriter{failOn: 2}
	sent, err := Write(context.Background(), w, nil, tuples(250), nil)
	if err == nil {
		t.Fatal("Write() error = nil, want error")
	}
	if want := (Sent{Writes: 100}); sent != want {
		t.Errorf("sent = %+v, want %+v", sent, want)
	}
}

func TestWriteCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &recWriter{}
	sent, err := Write(ctx, w, tuples(1), tuples(1), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Write() error = %v, want context.Canceled", err)
	}
	if sent != (Sent{}) {
		t.Errorf("sent = %+v, want zero", sent)
	}
	if len(w.reqs) != 0 {
		t.Errorf("requests = %v, want none", w.reqs)
	}
}

func TestWriteStopsInDeletePhase(t *testing.T) {
	w := &recWriter{failOn: 2}
	sent, err := Write(context.Background(), w, tuples(250), tuples(10), nil)
	if err == nil {
		t.Fatal("Write() error = nil, want error")
	}
	if want := (Sent{Deletes: 100}); sent != want {
		t.Errorf("sent = %+v, want %+v", sent, want)
	}
	if want := [][2]int{{0, 100}, {0, 100}}; !reflect.DeepEqual(w.reqs, want) {
		t.Errorf("requests = %v, want %v", w.reqs, want)
	}
}

// tupleWriter records every request in full; fail maps a 1-based request
// number to its error and onCall runs inside the request.
type tupleWriter struct {
	reqs      []req
	deadlines []time.Time
	fail      map[int]error
	onCall    func(n int)
}

type req struct{ writes, deletes []language.Tuple }

// Write fails like a real client when its context is done.
func (f *tupleWriter) Write(ctx context.Context, writes, deletes []language.Tuple) error {
	f.reqs = append(f.reqs, req{writes, deletes})
	deadline, _ := ctx.Deadline()
	f.deadlines = append(f.deadlines, deadline)
	n := len(f.reqs)
	if f.onCall != nil {
		f.onCall(n)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.fail[n]
}

func (f *tupleWriter) shape() [][2]int {
	var out [][2]int
	for _, r := range f.reqs {
		out = append(out, [2]int{len(r.writes), len(r.deletes)})
	}
	return out
}

// storedAndDesired returns n stored tuples carrying condition "old" and their
// desired versions carrying "new".
func storedAndDesired(n int) (was, now []language.Tuple) {
	for _, t := range tuples(n) {
		was = append(was, withCond(t, "old", nil))
		now = append(now, withCond(t, "new", nil))
	}
	return was, now
}

func TestReplaceBatches(t *testing.T) {
	was, now := storedAndDesired(250)
	w := &tupleWriter{}
	var last [2]int
	sent, lost, err := Replace(context.Background(), w, was, now, func(d, tot int) { last = [2]int{d, tot} })
	if err != nil || lost != nil {
		t.Fatalf("Replace() = %v, %v", lost, err)
	}
	// Each pair is {writes, deletes}: delete 100, write 100, and so on.
	if want := [][2]int{{0, 100}, {100, 0}, {0, 100}, {100, 0}, {0, 50}, {50, 0}}; !reflect.DeepEqual(w.shape(), want) {
		t.Errorf("requests = %v, want %v", w.shape(), want)
	}
	if want := (Sent{Writes: 250, Deletes: 250}); sent != want {
		t.Errorf("sent = %+v, want %+v", sent, want)
	}
	if want := [2]int{500, 500}; last != want {
		t.Errorf("last progress = %v, want %v", last, want)
	}
}

func TestReplaceRestoresOnWriteFailure(t *testing.T) {
	was, now := storedAndDesired(250)
	boom := errors.New("rejected")
	w := &tupleWriter{fail: map[int]error{2: boom}}
	sent, lost, err := Replace(context.Background(), w, was, now, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
	if sent != (Sent{}) || lost != nil {
		t.Errorf("sent = %+v, lost = %v, want zero and nil", sent, lost)
	}
	if want := [][2]int{{0, 100}, {100, 0}, {100, 0}}; !reflect.DeepEqual(w.shape(), want) {
		t.Fatalf("requests = %v, want %v", w.shape(), want)
	}
	if !reflect.DeepEqual(w.reqs[2].writes, was[:100]) {
		t.Errorf("restore wrote %+v, want the stored versions %+v", w.reqs[2].writes[:1], was[:1])
	}
	if w.reqs[2].writes[0].Condition != "old" {
		t.Errorf("restored condition = %q, want old", w.reqs[2].writes[0].Condition)
	}
}

func TestReplaceReportsLostTuples(t *testing.T) {
	was, now := storedAndDesired(250)
	boom, nope := errors.New("rejected"), errors.New("unreachable")
	w := &tupleWriter{fail: map[int]error{2: boom, 3: nope}}
	sent, lost, err := Replace(context.Background(), w, was, now, nil)
	if !errors.Is(err, boom) || !errors.Is(err, nope) {
		t.Fatalf("error = %v, want both failures", err)
	}
	if !reflect.DeepEqual(lost, was[:100]) {
		t.Errorf("lost = %d tuples, want the first batch's stored versions", len(lost))
	}
	if want := (Sent{Deletes: 100}); sent != want {
		t.Errorf("sent = %+v, want %+v", sent, want)
	}
}

func TestReplaceRestoresOnDeleteFailure(t *testing.T) {
	was, now := storedAndDesired(250)
	boom := errors.New("timeout")
	w := &tupleWriter{fail: map[int]error{1: boom}}
	sent, lost, err := Replace(context.Background(), w, was, now, nil)
	if !errors.Is(err, boom) || sent != (Sent{}) || lost != nil {
		t.Fatalf("Replace() = %+v, %v, %v, want nothing sent or lost and the delete's error", sent, lost, err)
	}
	// The delete may have reached the store, so the stored versions go back.
	if want := [][2]int{{0, 100}, {100, 0}}; !reflect.DeepEqual(w.shape(), want) {
		t.Fatalf("requests = %v, want %v", w.shape(), want)
	}
	if !reflect.DeepEqual(w.reqs[1].writes, was[:100]) {
		t.Errorf("restore wrote %+v, want the stored versions", w.reqs[1].writes[:1])
	}
}

func TestReplaceDeleteAndRestoreFail(t *testing.T) {
	was, now := storedAndDesired(250)
	boom, nope := errors.New("timeout"), errors.New("unreachable")
	w := &tupleWriter{fail: map[int]error{1: boom, 2: nope}}
	sent, lost, err := Replace(context.Background(), w, was, now, nil)
	if !errors.Is(err, boom) || !errors.Is(err, nope) {
		t.Fatalf("error = %v, want both failures", err)
	}
	if !reflect.DeepEqual(lost, was[:100]) {
		t.Errorf("lost = %d tuples, want the first batch's stored versions", len(lost))
	}
	// The delete was never confirmed, so it is not counted.
	if sent != (Sent{}) {
		t.Errorf("sent = %+v, want zero", sent)
	}
}

func TestReplaceLaterBatchRestoresItsOwnTuples(t *testing.T) {
	was, now := storedAndDesired(250)
	boom := errors.New("rejected")
	w := &tupleWriter{fail: map[int]error{4: boom}}
	sent, lost, err := Replace(context.Background(), w, was, now, nil)
	if !errors.Is(err, boom) || lost != nil {
		t.Fatalf("Replace() = %v, %v, want the write's error and nothing lost", lost, err)
	}
	if want := (Sent{Writes: 100, Deletes: 100}); sent != want {
		t.Errorf("sent = %+v, want %+v", sent, want)
	}
	if want := [][2]int{{0, 100}, {100, 0}, {0, 100}, {100, 0}, {100, 0}}; !reflect.DeepEqual(w.shape(), want) {
		t.Fatalf("requests = %v, want %v", w.shape(), want)
	}
	if !reflect.DeepEqual(w.reqs[4].writes, was[100:200]) {
		t.Errorf("restore wrote %+v, want the second batch's stored versions", w.reqs[4].writes[:1])
	}
}

func TestReplaceRestoreHasItsOwnDeadline(t *testing.T) {
	was, now := storedAndDesired(1)
	w := &tupleWriter{fail: map[int]error{2: errors.New("timed out")}}
	w.onCall = func(n int) {
		if n == 2 {
			time.Sleep(20 * time.Millisecond) // the rewrite uses up time
		}
	}
	if _, lost, _ := Replace(context.Background(), w, was, now, nil); lost != nil || len(w.deadlines) != 3 {
		t.Fatalf("lost = %v after %d requests, want a restore that went through", lost, len(w.deadlines))
	}
	if !w.deadlines[2].After(w.deadlines[1]) {
		t.Errorf("restore deadline %v is not after the rewrite's %v", w.deadlines[2], w.deadlines[1])
	}
}

// A request the client abandons may still be applied, so once a batch has
// started its requests ignore Ctrl-C and the run stops before the next batch.
func TestReplaceFinishesBatchWhenCanceledMidBatch(t *testing.T) {
	for _, during := range []int{1, 2} { // the delete, the write
		was, now := storedAndDesired(250)
		ctx, cancel := context.WithCancel(context.Background())
		w := &tupleWriter{onCall: func(n int) {
			if n == during {
				cancel()
			}
		}}
		sent, lost, err := Replace(ctx, w, was, now, nil)
		cancel()
		if !errors.Is(err, context.Canceled) || lost != nil {
			t.Fatalf("request %d: Replace() = %v, %v, want context.Canceled and nil", during, lost, err)
		}
		if want := (Sent{Writes: 100, Deletes: 100}); sent != want {
			t.Errorf("request %d: sent = %+v, want %+v", during, sent, want)
		}
		if want := [][2]int{{0, 100}, {100, 0}}; !reflect.DeepEqual(w.shape(), want) {
			t.Errorf("request %d: requests = %v, want %v", during, w.shape(), want)
		}
	}
}

func TestReplaceCanceledSendsNothing(t *testing.T) {
	was, now := storedAndDesired(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &tupleWriter{}
	sent, _, err := Replace(ctx, w, was, now, nil)
	if !errors.Is(err, context.Canceled) || sent != (Sent{}) || len(w.reqs) != 0 {
		t.Errorf("Replace() = %+v, %v with %d requests, want canceled and nothing sent", sent, err, len(w.reqs))
	}
}
