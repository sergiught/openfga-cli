package snapshot

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/openfga/mapper/language"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	require.Equal(t, [][2]int{{0, 100}, {0, 20}, {100, 0}, {100, 0}, {50, 0}}, w.reqs)
	require.Equal(t, Sent{Writes: 250, Deletes: 120}, sent)
	require.Equal(t, [2]int{370, 370}, last)
}

func TestWriteStopsAndCounts(t *testing.T) {
	w := &recWriter{failOn: 2}
	sent, err := Write(context.Background(), w, nil, tuples(250), nil)
	require.Error(t, err)
	require.Equal(t, Sent{Writes: 100}, sent)
}

func TestWriteCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &recWriter{}
	sent, err := Write(ctx, w, tuples(1), tuples(1), nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, Sent{}, sent)
	require.Empty(t, w.reqs)
}
