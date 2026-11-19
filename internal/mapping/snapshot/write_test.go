package snapshot

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

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
