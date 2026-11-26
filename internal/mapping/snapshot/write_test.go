package snapshot

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/openfga/mapper/language"
)

type recWriter struct {
	mu     sync.Mutex
	reqs   [][2]int // writes, deletes per request
	failOn int      // 1-based request that fails; 0 never
}

func (r *recWriter) Write(_ context.Context, writes, deletes []language.Tuple) error {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	// Requests of one kind run side by side, so only their sizes are fixed.
	if got := sortedReqs(w.reqs); !reflect.DeepEqual(got, [][2]int{{50, 0}, {100, 0}, {100, 0}, {0, 20}, {0, 100}}) {
		t.Errorf("requests = %v", w.reqs)
	}
	if w.reqs[0][1] == 0 || w.reqs[1][1] == 0 || w.reqs[2][1] != 0 {
		t.Errorf("requests = %v, want the two deletes first", w.reqs)
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
	// The requests are side by side, so how many went before the failure
	// varies; the failed one is not counted.
	if sent.Deletes != 0 || sent.Writes < 100 || sent.Writes >= 250 {
		t.Errorf("sent = %+v, want the requests that did not fail", sent)
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
	if sent.Writes != 0 || sent.Deletes < 100 || sent.Deletes >= 250 {
		t.Errorf("sent = %+v, want the two delete requests that did not fail", sent)
	}
	for _, r := range w.reqs {
		if r[0] != 0 {
			t.Errorf("requests = %v, want no write after a failed delete", w.reqs)
		}
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

var errTooMany = errors.New("too many changes")

// limitWriter refuses any request over limit (or every request when limit is
// negative) and implements BatchLimiter. named is the limit it reports, 0 for
// none.
type limitWriter struct {
	mu           sync.Mutex
	limit, named int
	reqs         [][2]int // writes, deletes per request, refused ones too
	accepted     [][2]int
	refused      int
}

func (l *limitWriter) Write(_ context.Context, writes, deletes []language.Tuple) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := [2]int{len(writes), len(deletes)}
	l.reqs = append(l.reqs, r)
	tooBig := l.limit < 0 || len(writes)+len(deletes) > l.limit
	if tooBig {
		l.refused++
		return errTooMany
	}
	l.accepted = append(l.accepted, r)
	return nil
}

func (l *limitWriter) BatchLimit(err error) (int, bool) {
	if errors.Is(err, errTooMany) {
		return l.named, true
	}
	return 0, false
}

func TestWriteShrinksToTheStoresLimit(t *testing.T) {
	w := &limitWriter{limit: 40, named: 40}
	var calls [][2]int
	sent, err := Write(context.Background(), w, tuples(90), tuples(200), func(d, tot int) { calls = append(calls, [2]int{d, tot}) })
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if want := (Sent{Writes: 200, Deletes: 90}); sent != want {
		t.Errorf("sent = %+v, want %+v", sent, want)
	}
	for _, r := range w.accepted {
		if r[0]+r[1] > 40 {
			t.Errorf("accepted request of %v changes", r)
		}
	}
	if w.refused < 1 || w.refused > 2 {
		t.Errorf("refused = %d, want one or two (requests in flight at the first refusal)", w.refused)
	}
	if last := calls[len(calls)-1]; last != [2]int{290, 290} {
		t.Errorf("last progress = %v, want {290 290}", last)
	}
	for _, c := range calls {
		if c[0] > 290 {
			t.Errorf("progress %v looks like a refused request", c)
		}
	}
	if len(calls) != len(w.accepted) {
		t.Errorf("progress calls = %d, accepted requests = %d", len(calls), len(w.accepted))
	}
}

func TestWriteHalvesWhenTheStoreNamesNoLimit(t *testing.T) {
	w := &limitWriter{limit: 30}
	sent, err := Write(context.Background(), w, nil, tuples(100), nil)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if sent != (Sent{Writes: 100}) {
		t.Errorf("sent = %+v", sent)
	}
	if w.refused != 2 || len(w.accepted) != 4 {
		t.Errorf("refused = %d, accepted = %v, want 2 refusals (100, 50) then 4 requests of 25", w.refused, w.accepted)
	}
	for _, r := range w.accepted {
		if r != [2]int{25, 0} {
			t.Errorf("accepted = %v, want only 25s", w.accepted)
		}
	}
}

func TestWriteGivesUpAtOneChange(t *testing.T) {
	w := &limitWriter{limit: -1}
	sent, err := Write(context.Background(), w, nil, tuples(100), nil)
	if !errors.Is(err, errTooMany) {
		t.Fatalf("error = %v, want errTooMany", err)
	}
	if sent != (Sent{}) {
		t.Errorf("sent = %+v, want zero", sent)
	}
	if w.reqs[0] != [2]int{100, 0} || w.refused != len(w.reqs) || len(w.accepted) != 0 {
		t.Errorf("requests = %v, want every one refused", w.reqs)
	}
	if got := sortedReqs(w.reqs); got[0] != [2]int{1, 0} {
		t.Errorf("requests = %v, want a request of one change, the last resort", w.reqs)
	}
}

func TestWriteDoesNotShrinkForOtherErrors(t *testing.T) {
	// recWriter fails with boom, which the limiter does not recognise.
	rw := &struct {
		*recWriter
		BatchLimiter
	}{&recWriter{failOn: 1}, &limitWriter{limit: 40}}
	_, err := Write(context.Background(), rw, nil, tuples(100), nil)
	if err == nil || errors.Is(err, errTooMany) {
		t.Fatalf("error = %v, want boom", err)
	}
	if len(rw.reqs) != 1 {
		t.Errorf("requests = %v, want one", rw.reqs)
	}
}

func TestWriteDoesNotShrinkWithoutALimiter(t *testing.T) {
	w := &recWriter{failOn: 1}
	_, err := Write(context.Background(), w, nil, tuples(100), nil)
	if err == nil {
		t.Fatal("error = nil, want error")
	}
	if len(w.reqs) != 1 {
		t.Errorf("requests = %v, want one", w.reqs)
	}
}

func TestReplaceShrinksWhenTheDeleteIsRefusedForSize(t *testing.T) {
	was, now := storedAndDesired(100)
	w := &limitWriter{limit: 40, named: 40}
	sent, lost, err := Replace(context.Background(), w, was, now, nil)
	if err != nil || lost != nil {
		t.Fatalf("error = %v, lost = %v", err, lost)
	}
	if want := (Sent{Writes: 100, Deletes: 100}); sent != want {
		t.Errorf("sent = %+v, want %+v", sent, want)
	}
	if w.refused != 1 {
		t.Errorf("refused = %d, want 1 (no restore request after it)", w.refused)
	}
	if w.reqs[1] != [2]int{0, 40} {
		t.Errorf("request after the refusal = %v, want a delete of 40, not a restore", w.reqs[1])
	}
	for _, r := range w.accepted {
		if r[0]+r[1] > 40 {
			t.Errorf("accepted request of %v changes", r)
		}
	}
}

func TestReplaceSizeRefusalOfTheWriteStillRestores(t *testing.T) {
	was, now := storedAndDesired(10)
	w := &tupleWriter{fail: map[int]error{2: errTooMany}}
	lw := &limiterOf{tupleWriter: w}
	sent, lost, err := Replace(context.Background(), lw, was, now, nil)
	if !errors.Is(err, errTooMany) {
		t.Fatalf("error = %v, want errTooMany", err)
	}
	if sent != (Sent{}) || lost != nil {
		t.Errorf("sent = %+v, lost = %v, want zero and nil", sent, lost)
	}
	if want := [][2]int{{0, 10}, {10, 0}, {10, 0}}; !reflect.DeepEqual(w.shape(), want) {
		t.Fatalf("requests = %v, want %v", w.shape(), want)
	}
	if !reflect.DeepEqual(w.reqs[2].writes, was) {
		t.Errorf("restore did not write the stored versions")
	}
}

// limiterOf makes a tupleWriter a BatchLimiter that knows errTooMany.
type limiterOf struct{ *tupleWriter }

func (limiterOf) BatchLimit(err error) (int, bool) { return 0, errors.Is(err, errTooMany) }

func sortedReqs(r [][2]int) [][2]int {
	out := append([][2]int(nil), r...)
	sort.Slice(out, func(i, j int) bool {
		if out[i][1] != out[j][1] {
			return out[i][1] < out[j][1]
		}
		return out[i][0] < out[j][0]
	})
	return out
}

// gateWriter blocks every request until the test releases it and reports
// each arrival on arrived. The request numbered failNth (1-based, 0 for none)
// fails with errBoom once the test closes failGo instead, and closes release when its failure
// reaches BatchLimit, which Write calls before it records the failure. It is
// safe for concurrent use.
type gateWriter struct {
	mu       sync.Mutex
	inFlight int
	peak     int
	sizes    []int
	kinds    []string // "w" or "d" per request, in arrival order
	failNth  int
	failGo   chan struct{} // the failing request waits for the test to close it
	arrived  chan int      // request number on arrival
	release  chan struct{}
	once     sync.Once
}

var errBoom = errors.New("boom")

func newGate() *gateWriter {
	return &gateWriter{arrived: make(chan int, 1000), failGo: make(chan struct{}), release: make(chan struct{})}
}

func (g *gateWriter) BatchLimit(err error) (int, bool) {
	if errors.Is(err, errBoom) {
		g.once.Do(func() { close(g.release) })
	}
	return 0, false
}

func (g *gateWriter) Write(ctx context.Context, writes, deletes []language.Tuple) error {
	g.mu.Lock()
	g.inFlight++
	g.peak = max(g.peak, g.inFlight)
	g.sizes = append(g.sizes, len(writes)+len(deletes))
	if len(writes) > 0 {
		g.kinds = append(g.kinds, "w")
	} else {
		g.kinds = append(g.kinds, "d")
	}
	n := len(g.sizes)
	g.mu.Unlock()
	g.arrived <- n
	var err error
	if n == g.failNth {
		<-g.failGo
		err = errBoom
	} else {
		select {
		case <-g.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()
	return err
}

func waitArrivals(t *testing.T, g *gateWriter, n int) {
	t.Helper()
	for range n {
		select {
		case <-g.arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for requests")
		}
	}
}

func TestWriteKeepsFourRequestsInFlight(t *testing.T) {
	g := newGate()
	type result struct {
		sent Sent
		err  error
	}
	res := make(chan result)
	go func() {
		s, err := Write(context.Background(), g, nil, tuples(1000), nil)
		res <- result{s, err}
	}()
	waitArrivals(t, g, writeWorkers)
	time.Sleep(20 * time.Millisecond) // a fifth request would have arrived by now
	g.mu.Lock()
	inFlight := g.inFlight
	g.mu.Unlock()
	if inFlight != writeWorkers {
		t.Fatalf("in flight = %d, want %d", inFlight, writeWorkers)
	}
	close(g.release)
	r := <-res
	if r.err != nil || r.sent != (Sent{Writes: 1000}) {
		t.Fatalf("sent = %+v, err = %v", r.sent, r.err)
	}
	if g.peak != writeWorkers || len(g.sizes) != 10 {
		t.Errorf("peak = %d, requests = %d, want %d and 10", g.peak, len(g.sizes), writeWorkers)
	}
}

func TestWriteFinishesDeletesBeforeWrites(t *testing.T) {
	g := newGate()
	close(g.release)
	if _, err := Write(context.Background(), g, tuples(250), tuples(250), nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"d", "d", "d", "w", "w", "w"}
	if !reflect.DeepEqual(g.kinds, want) {
		t.Errorf("kinds = %v, want %v", g.kinds, want)
	}
}

func TestWriteDoesNotStartWritesWhileADeleteIsInFlight(t *testing.T) {
	g := newGate()
	res := make(chan error)
	go func() {
		_, err := Write(context.Background(), g, tuples(250), tuples(250), nil)
		res <- err
	}()
	waitArrivals(t, g, 3)
	time.Sleep(20 * time.Millisecond)
	g.mu.Lock()
	n := len(g.kinds)
	g.mu.Unlock()
	if n != 3 {
		t.Fatalf("requests while deletes are unfinished = %d, want 3", n)
	}
	close(g.release)
	if err := <-res; err != nil {
		t.Fatal(err)
	}
}

func TestWriteOneRequestForOneBatch(t *testing.T) {
	g := newGate()
	close(g.release)
	sent, err := Write(context.Background(), g, nil, tuples(100), nil)
	if err != nil || sent != (Sent{Writes: 100}) || len(g.sizes) != 1 {
		t.Errorf("sent = %+v, err = %v, requests = %v", sent, err, g.sizes)
	}
}

func TestWriteSharedSizeSendsEveryTupleOnce(t *testing.T) {
	w := &limitWriter{limit: 40, named: 40}
	sent, err := Write(context.Background(), w, nil, tuples(1000), nil)
	if err != nil || sent != (Sent{Writes: 1000}) {
		t.Fatalf("sent = %+v, err = %v", sent, err)
	}
	total := 0
	for _, r := range w.accepted {
		if r[0] > 40 {
			t.Errorf("accepted request of %d", r[0])
		}
		total += r[0]
	}
	if total != 1000 {
		t.Errorf("accepted changes = %d, want 1000", total)
	}
	if w.refused > writeWorkers {
		t.Errorf("refused = %d, want at most %d (one per request in flight)", w.refused, writeWorkers)
	}
}

func TestWriteLetsInFlightRequestsFinishAfterAnError(t *testing.T) {
	g := newGate()
	g.failNth = 2
	type result struct {
		sent Sent
		err  error
	}
	res := make(chan result)
	go func() {
		s, err := Write(context.Background(), g, nil, tuples(500), nil)
		res <- result{s, err}
	}()
	waitArrivals(t, g, writeWorkers)
	close(g.failGo)
	r := <-res
	if !errors.Is(r.err, errBoom) {
		t.Fatalf("err = %v, want boom", r.err)
	}
	if r.sent != (Sent{Writes: 300}) {
		t.Errorf("sent = %+v, want the 3 requests that succeeded", r.sent)
	}
	if len(g.sizes) != writeWorkers {
		t.Errorf("requests = %d, want %d (the fifth must not start after the failure)", len(g.sizes), writeWorkers)
	}
}

func TestWriteCanceledWhileRequestsAreBlocked(t *testing.T) {
	g := newGate()
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		sent Sent
		err  error
	}
	res := make(chan result)
	go func() {
		s, err := Write(ctx, g, nil, tuples(1000), nil)
		res <- result{s, err}
	}()
	waitArrivals(t, g, writeWorkers)
	cancel()
	r := <-res
	if !errors.Is(r.err, context.Canceled) || r.sent != (Sent{}) {
		t.Errorf("sent = %+v, err = %v", r.sent, r.err)
	}
	if len(g.sizes) != writeWorkers {
		t.Errorf("requests = %d, want %d", len(g.sizes), writeWorkers)
	}
}

func TestWriteProgressIsSerialAndIncreasing(t *testing.T) {
	g := newGate()
	close(g.release)
	var calls []int // not synchronized on purpose: -race proves one at a time
	count := 0
	_, err := Write(context.Background(), g, tuples(250), tuples(1000), func(done, total int) {
		count++
		calls = append(calls, done)
		if total != 1250 {
			t.Errorf("total = %d", total)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(calls); i++ {
		if calls[i] <= calls[i-1] {
			t.Errorf("progress = %v, want strictly increasing", calls)
			break
		}
	}
	if count != 13 || calls[len(calls)-1] != 1250 {
		t.Errorf("calls = %d, last = %d", count, calls[len(calls)-1])
	}
}
