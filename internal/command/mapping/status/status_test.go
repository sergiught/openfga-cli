package status

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
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
type writes struct {
	mu    sync.Mutex
	calls []string
}

func (w *writes) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, string(p))
	return len(p), nil
}

func (w *writes) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
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
	for _, line := range strings.Split(ansi.Strip(w.calls[1]), "\x1b[K\n") {
		line = strings.TrimLeft(line, "\x1b[0123456789A\r")
		if n := lipgloss.Width(strings.TrimSuffix(line, "\x1b[J")); n > 39 {
			t.Errorf("line of the frame after the resize is %d cells, max 39: %q", n, line)
		}
	}
}

// wrapped is the rows the lines of a card drawn at 100 columns take at cols.
func wrapped(t *testing.T, first string, cols int) int {
	t.Helper()
	rows := 0
	for _, line := range strings.Split(strings.TrimSuffix(strings.TrimSuffix(first, "\x1b[J"), "\x1b[K\n"), "\x1b[K\n") {
		line = strings.TrimPrefix(line, "\r")
		rows += max(1, (lipgloss.Width(line)+cols-1)/cols)
	}
	return rows
}

func TestNarrowingMovesUpOverWrappedLines(t *testing.T) {
	cols := 100
	w, clk := &writes{}, &fakeClock{t: time.Unix(1000, 0)}
	l := newLive(w, func() (int, int) { return cols, 60 }, clk.now, func() uint64 { return 0 }, "t", "Store")
	l.Phase("Store")
	l.frame()
	cols = 40
	want := wrapped(t, w.calls[0], 40)
	if want <= strings.Count(w.calls[0], "\x1b[K\n") {
		t.Fatalf("test setup: the card does not wrap at 40 columns")
	}
	l.frame()
	if prefix := "\x1b[" + strconv.Itoa(want) + "A\r"; !strings.HasPrefix(w.calls[1], prefix) {
		t.Errorf("frame after narrowing starts %q, want %q", w.calls[1][:8], prefix)
	}
	// The same for the erase of Suspend: the second frame's lines fit 40 columns.
	cols = 30
	want = wrapped(t, w.calls[1][strings.Index(w.calls[1], "\r")+1:], 30)
	l.Suspend("read")
	if prefix := "\x1b[" + strconv.Itoa(want) + "A\r\x1b[J"; w.calls[len(w.calls)-2] != prefix {
		t.Errorf("suspend erase = %q, want %q", w.calls[len(w.calls)-2], prefix)
	}
}

func TestTinyTerminalDrawsNothing(t *testing.T) {
	cols := 100
	w, clk := &writes{}, &fakeClock{t: time.Unix(1000, 0)}
	l := newLive(w, func() (int, int) { return cols, 40 }, clk.now, func() uint64 { return 0 }, "t", "Store")
	l.Phase("Store")
	l.frame()
	cols = 5
	l.frame()
	if got := w.calls[1]; strings.Contains(ansi.Strip(got), "Store") || !strings.HasSuffix(got, "\r\x1b[J") {
		t.Errorf("frame at 5 columns = %q, want only an erase", got)
	}
}

func TestSummaryCountsAfterCounterReset(t *testing.T) {
	l, w, _ := newTest(40)
	reqs := 0
	l.Provider("Auth0", func() snapshot.Stats { return snapshot.Stats{Requests: reqs} })
	l.Phase("Store")
	reqs = 100
	l.Suspend("read")
	reqs = 7 // a new read reset the provider's counters
	l.Phase("Auth0")
	l.Suspend("read")
	if got := ansi.Strip(w.calls[len(w.calls)-1]); !strings.Contains(got, "Auth0 7 requests") {
		t.Errorf("summary = %q, want the 7 requests of the new read", got)
	}
}

func TestResumeResetsRates(t *testing.T) {
	l, _, clk := newTest(40)
	reqs := 0
	l.Provider("Auth0", func() snapshot.Stats { return snapshot.Stats{Requests: reqs} })
	l.Phase("Store")
	l.frame()
	clk.add(2 * time.Second)
	reqs = 20
	l.frame()
	l.Suspend("read")
	clk.add(time.Minute)
	l.Phase("Auth0")
	l.frame()
	if r := l.prov.rate.rate(); r != 0 {
		t.Errorf("rate right after resuming = %v, want 0", r)
	}
}

func TestTarget(t *testing.T) {
	if _, _, _, ok := target(&bytes.Buffer{}); ok {
		t.Error("a buffer is a target")
	}
	if _, _, _, ok := target(&colorprofile.Writer{Forward: &bytes.Buffer{}, Profile: colorprofile.TrueColor}); ok {
		t.Error("a colorprofile.Writer over a buffer is a target")
	}
	if New(&colorprofile.Writer{Forward: &bytes.Buffer{}, Profile: colorprofile.TrueColor}, "t") != nil {
		t.Error("New over a colorprofile.Writer over a buffer returned a status")
	}

	f := os.Stderr
	got, frames, plain, ok := target(f)
	if !ok || got != f || frames != io.Writer(f) || plain {
		t.Errorf("bare file: %v %v %v %v", got, frames, plain, ok)
	}
	cw := &colorprofile.Writer{Forward: f, Profile: colorprofile.ANSI256}
	got, frames, plain, ok = target(cw)
	if !ok || got != f || frames != io.Writer(cw) || plain {
		t.Errorf("wrapped file: frames must go through the writer, got %v %v %v %v", got, frames, plain, ok)
	}

	t.Setenv("TERM", "xterm")
	nt := &colorprofile.Writer{Forward: f, Profile: colorprofile.NoTTY}
	got, frames, plain, ok = target(nt)
	if !ok || got != f || frames != io.Writer(f) || !plain {
		t.Errorf("NoTTY: frames must go to the file, colorless; got %v %v %v %v", got, frames, plain, ok)
	}
	t.Setenv("TERM", "dumb")
	if _, _, _, ok := target(nt); ok {
		t.Error("a NoTTY writer on a dumb terminal is a target")
	}
}

func TestPlainFramesHaveNoColor(t *testing.T) {
	l, w, _ := newTest(40)
	l.plain = true
	l.Phase("Store")
	l.frame()
	if strings.Contains(w.calls[0], "\x1b[3") || strings.Contains(w.calls[0], "\x1b[1m") {
		t.Errorf("plain frame has SGR: %q", w.calls[0])
	}
	if !strings.Contains(w.calls[0], "\x1b[K\n") {
		t.Error("plain frame lost its cursor sequences")
	}
}

func TestSetAndNoteFromOtherGoroutines(t *testing.T) {
	l, _, clk := newTest(40)
	l.Phase("Store")
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				l.Set("tuples", i+g, 1000)
				l.Note("n")
			}
		}()
	}
	for range 50 {
		clk.add(100 * time.Millisecond)
		l.frame()
	}
	wg.Wait()
	l.Close()
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

	erase := w.calls[len(w.calls)-2]
	if erase != "\x1b["+strconv.Itoa(lines)+"A\r\x1b[J" {
		t.Errorf("suspend does not erase the card: %q", erase)
	}
	want := "read in 3m 16s · Auth0 3,274 requests, 12 rate limited · peak memory 1.0 GB\n"
	if got := ansi.Strip(w.calls[len(w.calls)-1]); got != "● "+want {
		t.Errorf("summary = %q, want %q", got, "● "+want)
	}

	// The next stretch counts from the previous summary line.
	reqs, limited = 3300, 12
	l.Phase("Auth0")
	clk.add(2 * time.Second)
	reqs = 3304
	l.Suspend("wrote")
	last := w.calls[len(w.calls)-1]
	if got := ansi.Strip(last); !strings.Contains(got, "wrote in 2s · Auth0 30 requests ·") || strings.Contains(got, "rate limited") {
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
	if summaries != 1 || len(w.calls) != 2 {
		t.Errorf("%d summaries in %d writes, want 1 in 2 (erase, summary): %q", summaries, len(w.calls), w.calls)
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
	deadline := time.Now().Add(5 * time.Second)
	for w.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if w.count() == 0 {
		t.Fatal("the redraw goroutine wrote no frame")
	}
	l.Close()
	n := w.count()
	time.Sleep(2 * redrawEvery)
	if w.count() != n {
		t.Error("the redraw goroutine kept writing after Close")
	}
}
