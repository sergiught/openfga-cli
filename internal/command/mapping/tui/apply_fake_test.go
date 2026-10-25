package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// fakeStore is an in-memory store for the Apply pane: it reads its tuples and
// records every write.
type fakeStore struct {
	mu     sync.Mutex
	tuples []language.Tuple
	// writes is every Write call, in order, the failing one included.
	writes []fakeWrite
	// failAt makes the Nth write (1-based) fail and change nothing; 0 never.
	failAt int
	// block, when set, makes each write announce itself with a send on it,
	// then wait for a second send before it does anything, so a test knows a
	// request is in flight and can act before it finishes. Set it before the
	// writes start, and answer every write it holds, or its goroutine leaks.
	block chan struct{}
	// reads is how many ReadTuples calls the store has had.
	reads int
	// readErr, when set, fails every read.
	readErr error
}

// fakeWrite is one Write request.
type fakeWrite struct {
	writes, deletes []language.Tuple
	// ctxErr is the request's context error as the write finished: nil unless
	// something cancelled the request itself.
	ctxErr error
}

var (
	_ mapping.TupleReader = (*fakeStore)(nil)
	_ mapping.TupleWriter = (*fakeStore)(nil)
)

// errFakeWrite is the error a write failAt names returns.
var errFakeWrite = errors.New("the store refused the write")

func (s *fakeStore) ReadTuples(_ context.Context, f language.TupleFilter) ([]language.Tuple, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.readErr != nil {
		return nil, s.readErr
	}
	var out []language.Tuple
	for _, t := range s.tuples {
		if fakeMatches(t, f) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *fakeStore) Write(ctx context.Context, writes, deletes []language.Tuple) error {
	if s.block != nil {
		s.block <- struct{}{}
		<-s.block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, fakeWrite{writes: slices.Clone(writes), deletes: slices.Clone(deletes), ctxErr: ctx.Err()})
	if len(s.writes) == s.failAt {
		return errFakeWrite
	}
	for _, d := range deletes {
		s.tuples = slices.DeleteFunc(s.tuples, func(t language.Tuple) bool { return sameStored(t, d) })
	}
	for _, w := range writes {
		if !slices.ContainsFunc(s.tuples, func(t language.Tuple) bool { return sameStored(t, w) }) {
			s.tuples = append(s.tuples, w)
		}
	}
	return nil
}

// written is how many Write calls the store has had.
func (s *fakeStore) written() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.writes)
}

// sent is every Write call so far.
func (s *fakeStore) sent() []fakeWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.writes)
}

// readCount is how many ReadTuples calls the store has had.
func (s *fakeStore) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// sameStored is whether two tuples are the same relationship in the store.
func sameStored(a, b language.Tuple) bool {
	return a.User == b.User && a.Relation == b.Relation && a.Object == b.Object
}

// fakeMatches is a read filter the way the store applies one: each field set
// must match, and an object of "type:" matches every object of that type.
func fakeMatches(t language.Tuple, f language.TupleFilter) bool {
	if f.User != "" && f.User != t.User || f.Relation != "" && f.Relation != t.Relation {
		return false
	}
	if i := strings.IndexByte(f.Object, ':'); i > 0 && i == len(f.Object)-1 {
		return strings.HasPrefix(t.Object, f.Object)
	}
	return f.Object == "" || f.Object == t.Object
}

func TestTheFakeStoreReadsWritesFailsAndBlocks(t *testing.T) {
	ctx := context.Background()
	member := func(user, team string) language.Tuple {
		return language.Tuple{User: "user:" + user, Relation: "member", Object: "team:" + team}
	}
	s := &fakeStore{tuples: []language.Tuple{member("1", "a"), member("2", "b")}, failAt: 2}
	if got, _ := s.ReadTuples(ctx, language.TupleFilter{Object: "team:"}); len(got) != 2 {
		t.Fatalf("a type filter read %v", got)
	}
	if got, _ := s.ReadTuples(ctx, language.TupleFilter{Object: "team:a"}); len(got) != 1 {
		t.Fatalf("an object filter read %v", got)
	}

	if err := s.Write(ctx, []language.Tuple{member("3", "a")}, []language.Tuple{member("1", "a")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, []language.Tuple{member("4", "a")}, nil); !errors.Is(err, errFakeWrite) {
		t.Fatalf("write 2 = %v, want the failAt error", err)
	}
	got, _ := s.ReadTuples(ctx, language.TupleFilter{Object: "team:a"})
	if len(got) != 1 || !sameStored(got[0], member("3", "a")) || s.written() != 2 {
		t.Fatalf("after a write and a failed one: tuples %v, %d writes recorded", got, s.written())
	}

	s.block = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- s.Write(ctx, []language.Tuple{member("5", "a")}, nil) }()
	<-s.block // the write is in flight
	select {
	case err := <-done:
		t.Fatalf("a blocked write returned before its release: %v", err)
	default:
	}
	if s.written() != 2 {
		t.Fatalf("a held write was recorded: %d writes", s.written())
	}
	s.block <- struct{}{}
	if err := <-done; err != nil || s.written() != 3 || s.sent()[2].ctxErr != nil {
		t.Fatalf("released write = %v, %d writes recorded, %+v", err, s.written(), s.sent())
	}
}
