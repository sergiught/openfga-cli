package mapping

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/openfga/mapper/language"
)

type recWriter struct {
	calls  [][2][]language.Tuple
	failAt int // 1-based call that fails; 0 = never
}

func (w *recWriter) Write(_ context.Context, writes, deletes []language.Tuple) error {
	w.calls = append(w.calls, [2][]language.Tuple{writes, deletes})
	if len(w.calls) == w.failAt {
		return errors.New("boom")
	}
	return nil
}

func change(n int, action language.TupleAction) language.Tuple {
	return language.Tuple{User: fmt.Sprintf("user:%d", n), Relation: "member", Object: "team:1", Action: action}
}

func TestExecuteOneRequestPerEvent(t *testing.T) {
	p := &Plan{Events: []EventPlan{
		{Index: 1, Atomic: true, Changes: []language.Tuple{change(1, language.ActionWrite), change(2, language.ActionDelete)}},
		{Index: 2},
		{Index: 3, Atomic: true, Changes: []language.Tuple{change(3, language.ActionWrite)}},
	}}
	w := &recWriter{}
	n, err := Execute(context.Background(), p, w, nil)
	if err != nil || n != 3 || len(w.calls) != 2 {
		t.Fatalf("n=%d err=%v calls=%d", n, err, len(w.calls))
	}
	if len(w.calls[0][0]) != 1 || len(w.calls[0][1]) != 1 {
		t.Fatalf("first call = %+v", w.calls[0])
	}
}

func TestExecuteChunksLargeEvents(t *testing.T) {
	var cs []language.Tuple
	for i := 0; i < 250; i++ {
		cs = append(cs, change(i, language.ActionDelete))
	}
	w := &recWriter{}
	if _, err := Execute(context.Background(), &Plan{Events: []EventPlan{{Index: 1, Changes: cs}}}, w, nil); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 3 || len(w.calls[2][1]) != 50 {
		t.Fatalf("calls = %d", len(w.calls))
	}
}

func TestExecuteSplitsANonAtomicEvent(t *testing.T) {
	// Same user/relation/object deleted and written: OpenFGA rejects one
	// request with both, so the event goes out as two.
	p := &Plan{Events: []EventPlan{{
		Index:   1,
		Changes: []language.Tuple{change(1, language.ActionDelete), change(1, language.ActionWrite)},
	}}}
	w := &recWriter{}
	n, err := Execute(context.Background(), p, w, nil)
	if err != nil || n != 1 || len(w.calls) != 2 {
		t.Fatalf("n=%d err=%v calls=%d", n, err, len(w.calls))
	}
	if len(w.calls[0][0]) != 0 || len(w.calls[0][1]) != 1 {
		t.Fatalf("first call = %+v, want deletes only", w.calls[0])
	}
	if len(w.calls[1][0]) != 1 || len(w.calls[1][1]) != 0 {
		t.Fatalf("second call = %+v, want writes only", w.calls[1])
	}
}

func TestExecuteStopsAtFirstError(t *testing.T) {
	p := &Plan{Events: []EventPlan{
		{Index: 1, Atomic: true, Changes: []language.Tuple{change(1, language.ActionWrite)}},
		{Index: 2, Atomic: true, Changes: []language.Tuple{change(2, language.ActionWrite)}},
		{Index: 3, Atomic: true, Changes: []language.Tuple{change(3, language.ActionWrite)}},
	}}
	n, err := Execute(context.Background(), p, &recWriter{failAt: 2}, nil)
	if n != 1 || err == nil || !strings.Contains(err.Error(), "event 2") {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestExecuteStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, err := Execute(ctx, &Plan{Events: []EventPlan{{Index: 1, Atomic: true, Changes: []language.Tuple{change(1, language.ActionWrite)}}}}, &recWriter{}, nil)
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestRequestsCountsWhatExecuteSends(t *testing.T) {
	var big []language.Tuple
	for i := 0; i < 250; i++ {
		big = append(big, change(i, language.ActionDelete))
	}
	big = append(big, change(1, language.ActionWrite))
	for _, ep := range []EventPlan{
		{Index: 1},
		{Index: 1, Atomic: true, Changes: []language.Tuple{change(1, language.ActionDelete), change(2, language.ActionWrite)}},
		{Index: 1, Changes: []language.Tuple{change(1, language.ActionDelete), change(1, language.ActionWrite)}},
		{Index: 1, Changes: big},
	} {
		w := &recWriter{}
		if _, err := Execute(context.Background(), &Plan{Events: []EventPlan{ep}}, w, nil); err != nil {
			t.Fatal(err)
		}
		if ep.Requests() != len(w.calls) {
			t.Errorf("Requests() = %d, Execute sent %d (%d changes, atomic %v)", ep.Requests(), len(w.calls), len(ep.Changes), ep.Atomic)
		}
	}
}
