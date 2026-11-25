package status

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/client"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// writes records each Write call separately.
type writes struct{ calls []string }

func (w *writes) Write(p []byte) (int, error) {
	w.calls = append(w.calls, string(p))
	return len(p), nil
}

func newTest(rows int) (*Live, *writes, *fakeClock) {
	w, clk := &writes{}, &fakeClock{t: time.Unix(1000, 0)}
	l := newLive(w, func() (int, int) { return 100, rows }, clk.now, func() uint64 { return 1 << 30 }, "ofga mappings drift", "Store", "Auth0", "Replay")
	return l, w, clk
}

func TestFrameMovesUpByPreviousLines(t *testing.T) {
	l, w, _ := newTest(40)
	l.Phase("Store")
	l.frame()
	l.frame()
	if len(w.calls) != 2 {
		t.Fatalf("%d writes for 2 frames, want one each", len(w.calls))
	}
	n := strings.Count(w.calls[0], "\x1b[K\n")
	if n < 10 || strings.HasPrefix(w.calls[0], "\x1b[") {
		t.Errorf("first frame has %d lines or moves up: %q", n, w.calls[0][:10])
	}
	if want := "\x1b[" + strconv.Itoa(n) + "A\r"; !strings.HasPrefix(w.calls[1], want) {
		t.Errorf("second frame starts %q, want %q", w.calls[1][:10], want)
	}
	if !strings.HasSuffix(w.calls[1], "\x1b[J") {
		t.Error("frame does not erase below")
	}
	if strings.Contains(w.calls[0], "\x1b[?25l") || strings.Contains(w.calls[0], "\x1b[?1049h") {
		t.Error("frame hides the cursor or uses the alternate screen")
	}
}

func TestFollowsResize(t *testing.T) {
	cols := 100
	w, clk := &writes{}, &fakeClock{t: time.Unix(1000, 0)}
	l := newLive(w, func() (int, int) { return cols, 40 }, clk.now, func() uint64 { return 0 }, "t", "Store")
	l.Phase("Store")
	l.frame()
	cols = 40
	l.frame()
	if w.calls[0] == w.calls[1] {
		t.Error("frame ignored the new size")
	}
}

func TestSuspendErasesAndSummarises(t *testing.T) {
	l, w, clk := newTest(40)
	var reqs, limited = 0, 0
	l.Provider("Auth0", func() snapshot.Stats {
		return snapshot.Stats{Requests: reqs, RateLimited: limited, Counts: []snapshot.Count{{Type: "snapshot.organization", N: 3}}}
	})
	var meter client.Meter
	l.Store(&meter)

	l.Phase("Store")
	l.frame()
	lines := strings.Count(w.calls[0], "\x1b[K\n")
	reqs, limited = 3274, 12
	clk.add(196 * time.Second)
	l.Suspend("read")

	last := w.calls[len(w.calls)-1]
	if !strings.HasPrefix(last, "\x1b["+strconv.Itoa(lines)+"A\r\x1b[J") {
		t.Errorf("suspend does not erase the card: %q", last)
	}
	want := "read in 3m 16s · Auth0 3,274 requests, 12 rate limited · peak memory 1.0 GB\n"
	if got := ansi.Strip(last[strings.Index(last, "J")+1:]); got != "● "+want {
		t.Errorf("summary = %q, want %q", got, "● "+want)
	}

	// The next stretch counts from the previous summary line.
	reqs, limited = 3300, 12
	l.Phase("Auth0")
	clk.add(2 * time.Second)
	reqs = 3304
	l.Suspend("wrote")
	last = w.calls[len(w.calls)-1]
	if got := ansi.Strip(last[strings.Index(last, "J")+1:]); !strings.Contains(got, "wrote in 2s · Auth0 30 requests ·") || strings.Contains(got, "rate limited") {
		t.Errorf("second summary = %q, want deltas and no rate limited", got)
	}
}

func TestSuspendStopsDrawing(t *testing.T) {
	l, w, _ := newTest(40)
	l.Phase("Store")
	l.Suspend("read")
	n := len(w.calls)
	l.frame()
	if len(w.calls) != n {
		t.Error("frame wrote after Suspend")
	}
	l.Phase("Auth0")
	l.frame()
	if len(w.calls) == n {
		t.Error("Phase did not resume drawing")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	l, w, clk := newTest(40)
	l.Phase("Store")
	clk.add(5 * time.Second)
	l.Close()
	l.Close()
	var summaries int
	for _, c := range w.calls {
		if strings.Contains(c, "stopped after 5s") {
			summaries++
		}
	}
	if summaries != 1 || len(w.calls) != 1 {
		t.Errorf("%d summaries in %d writes, want 1 in 1: %q", summaries, len(w.calls), w.calls)
	}
}

func TestCloseWithoutPhaseWritesNothing(t *testing.T) {
	l, w, _ := newTest(40)
	l.Close()
	l.Suspend("read")
	if len(w.calls) != 0 {
		t.Errorf("wrote %q", w.calls)
	}
}

func TestNilLiveDoesNothing(t *testing.T) {
	var l *Live
	l.Provider("Auth0", nil)
	l.Store(nil)
	l.Phase("Store")
	l.Set("tuples", 1, 2)
	l.Note("x")
	l.Suspend("read")
	l.Close()
}

func TestNewNeedsTerminal(t *testing.T) {
	if l := New(&bytes.Buffer{}, "t"); l != nil {
		t.Error("New on a buffer returned a status")
	}
}

func TestSetAndNoteShowInCard(t *testing.T) {
	l, w, clk := newTest(40)
	l.Phase("Store")
	l.Set("tuples", 100, 0)
	clk.add(time.Second)
	l.frame()
	clk.add(time.Second)
	l.Set("tuples", 2100, 0)
	l.frame()
	l.Note("2,100 tuples read")
	l.Phase("Auth0")
	l.Set("members of organizations", 25, 100)
	clk.add(10 * time.Second)
	l.frame()
	out := ansi.Strip(w.calls[len(w.calls)-1])
	for _, want := range []string{"✓ Store", "2,100 tuples read", "members of organizations", "25 / 100", "25%", "30s left"} {
		if !strings.Contains(out, want) {
			t.Errorf("card lacks %q:\n%s", want, out)
		}
	}
	if mid := ansi.Strip(w.calls[1]); !strings.Contains(mid, "2,100 · 2,000/s") {
		t.Errorf("no rate for an unknown total:\n%s", mid)
	}
}

func TestRealTickerStops(t *testing.T) {
	w := &writes{}
	l := newLive(w, func() (int, int) { return 100, 40 }, time.Now, func() uint64 { return 0 }, "t", "Store")
	l.async = true
	l.Phase("Store")
	l.Close()
	n := len(w.calls)
	time.Sleep(2 * redrawEvery)
	if len(w.calls) != n {
		t.Error("the redraw goroutine kept writing after Close")
	}
}
