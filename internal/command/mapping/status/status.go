// Package status draws the live status card of a long read or write on a
// terminal: the steps with a progress bar, requests sent, times rate limited,
// items read and memory.
package status

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"

	"github.com/sergiught/openfga-cli/internal/client"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/output"
)

const (
	redrawEvery  = 100 * time.Millisecond
	fallbackCols = 80
	fallbackRows = 24
)

// Live is a status card drawn in place on a terminal. Every method is a no-op
// on a nil *Live.
//
// Phase, Suspend and Close must be called from one goroutine; Set and Note may
// be called from any goroutine.
type Live struct {
	size  func() (cols, rows int)
	now   func() time.Time
	mem   func() uint64
	async bool // Phase starts a goroutine that redraws every 100 ms

	// omu serialises everything written to the terminal. It is taken before
	// mu, and mu is never held while writing, so a stalled terminal cannot
	// block Set.
	omu    sync.Mutex
	w      io.Writer // the summary line goes here
	frameW io.Writer // frames and erasures go here
	plain  bool      // strip colors from frames
	widths []int     // cell widths of the previous frame's lines

	mu      sync.Mutex
	title   string
	phases  []*livePhase
	prov    *liveSource
	provFn  func() snapshot.Stats
	stats   snapshot.Stats // the provider's last reading
	store   *liveSource
	meter   *client.Meter
	drawing bool
	started time.Time // when drawing last started or resumed
	frames  int
	cur     *livePhase
	memNow  uint64
	memTop  uint64
	memAt   time.Time
	stop    chan struct{}
	stopped chan struct{}
}

type livePhase struct {
	phase
	began     time.Time
	stepBegan time.Time
	items     window
}

// liveSource is a pair of counters read once a frame, with the values at the
// last summary line.
type liveSource struct {
	name          string
	req, lim      int
	rate          window
	base, baseLim int
}

// target finds where a status for w would draw: the terminal file behind w
// (unwrapping a colorprofile.Writer), the writer frames go to, and whether
// frames must be colorless. A NoTTY writer strips every escape sequence,
// cursor movement included, so frames then go straight to the file; on a dumb
// terminal there is no card at all.
func target(w io.Writer) (f *os.File, frames io.Writer, plain, ok bool) {
	inner, frames := w, w
	cw, wrapped := w.(*colorprofile.Writer)
	if wrapped {
		inner = cw.Forward
	}
	f, isFile := inner.(*os.File)
	if !isFile {
		return nil, nil, false, false
	}
	if wrapped && cw.Profile <= colorprofile.NoTTY {
		if os.Getenv("TERM") == "dumb" {
			return nil, nil, false, false
		}
		return f, f, true, true
	}
	return f, frames, false, true
}

// NewAt returns a live status drawing on w at a fixed size of cols by rows,
// with the real clock and memory reading and the same redraw loop. It does not
// check that w is a terminal and is never nil: for tests and for callers that
// know the size.
func NewAt(w io.Writer, cols, rows int, title string, phases ...string) *Live {
	l := newLive(w, func() (int, int) { return cols, rows }, time.Now, heapInUse, title, phases...)
	l.async = true
	return l
}

// New returns a live status drawing on w, or nil when w is not a terminal or
// output is quiet, plain, terse or not interactive. w may be the file itself
// or a *colorprofile.Writer over it.
func New(w io.Writer, title string, phases ...string) *Live {
	f, frames, plain, ok := target(w)
	if !ok || !term.IsTerminal(f.Fd()) || output.Quiet || output.Plain || output.Terse || !output.Interactive {
		return nil
	}
	l := newLive(w, func() (int, int) {
		cols, rows, err := term.GetSize(f.Fd())
		if err != nil || cols <= 0 || rows <= 0 {
			return fallbackCols, fallbackRows
		}
		return cols, rows
	}, time.Now, heapInUse, title, phases...)
	l.frameW, l.plain, l.async = frames, plain, true
	return l
}

func newLive(w io.Writer, size func() (int, int), now func() time.Time, mem func() uint64, title string, phases ...string) *Live {
	l := &Live{w: w, frameW: w, size: size, now: now, mem: mem, title: title}
	for _, name := range phases {
		l.phases = append(l.phases, &livePhase{phase: phase{name: name}})
	}
	return l
}

func heapInUse() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Sys - m.HeapReleased
}

// set records a reading of the counters. A reading below the previous one
// means the counters were reset, so what was counted before no longer applies
// to the summary, even when they have grown past the old base since.
func (s *liveSource) set(req, lim int) {
	if req < s.req || lim < s.lim {
		s.base, s.baseLim = 0, 0
	}
	s.req, s.lim = req, lim
}

// Provider sets the identity provider whose requests and items are shown. The
// stats function is polled each frame.
func (l *Live) Provider(label string, stats func() snapshot.Stats) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prov = &liveSource{name: label}
	l.provFn = stats
}

// Store sets the meter of the store's requests, polled each frame.
func (l *Live) Store(m *client.Meter) {
	if l == nil || m == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store = &liveSource{name: "store"}
	l.meter = m
}

// Phase starts the named phase running; the phase that was running is done.
// It starts or resumes drawing.
func (l *Live) Phase(name string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	now := l.now()
	l.finish(now)
	var ph *livePhase
	for _, p := range l.phases {
		if p.name == name {
			ph = p
			break
		}
	}
	if ph == nil {
		ph = &livePhase{phase: phase{name: name}}
		l.phases = append(l.phases, ph)
	}
	ph.state, ph.began, ph.stepBegan = running, now, now
	ph.step, ph.done, ph.total, ph.items = "", 0, 0, nil
	l.cur = ph
	begin := !l.drawing
	if begin {
		l.drawing, l.started = true, now
		for _, s := range []*liveSource{l.prov, l.store} {
			if s != nil {
				s.rate = nil
			}
		}
		if l.async {
			l.stop, l.stopped = make(chan struct{}), make(chan struct{})
			go l.loop(l.stop, l.stopped)
		}
	}
	l.mu.Unlock()
	if begin && l.async {
		l.frame() // the card is there at once, not after the first tick
	}
}

// finish marks the running phase done.
func (l *Live) finish(now time.Time) {
	if l.cur == nil {
		return
	}
	l.cur.state, l.cur.took = done, now.Sub(l.cur.began)
	l.cur = nil
}

// Set sets the detail of the running phase. A total of 0 means unknown.
func (l *Live) Set(step string, done, total int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cur == nil {
		return
	}
	if step != l.cur.step {
		l.cur.stepBegan, l.cur.items = l.now(), nil
	}
	l.cur.step, l.cur.done, l.cur.total = step, done, total
}

// Note sets what the running phase shows once it is done.
func (l *Live) Note(text string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cur != nil {
		l.cur.note = text
	}
}

// Suspend marks the running phase done, erases the card, writes one summary
// line ("read in 3m 16s · ...") and stops drawing until the next Phase.
func (l *Live) Suspend(verb string) {
	if l != nil {
		l.end(verb + " in ")
	}
}

// Close is Suspend with "stopped after", if still drawing. It is idempotent.
func (l *Live) Close() {
	if l != nil {
		l.end("stopped after ")
	}
}

func (l *Live) end(lead string) {
	l.mu.Lock()
	if !l.drawing {
		l.mu.Unlock()
		return
	}
	l.drawing = false
	stop, stopped := l.stop, l.stopped
	l.stop, l.stopped = nil, nil
	l.mu.Unlock()
	if stop != nil {
		close(stop)
		<-stopped
	}

	l.omu.Lock()
	defer l.omu.Unlock()
	l.mu.Lock()
	now := l.now()
	l.finish(now)
	l.sample(now)
	line := lead + dur(now.Sub(l.started)) + l.summary()
	for _, s := range []*liveSource{l.prov, l.store} {
		if s != nil {
			s.base, s.baseLim = s.req, s.lim
		}
	}
	l.mu.Unlock()

	cols, _ := l.size()
	var buf bytes.Buffer
	l.moveUp(&buf, cols)
	buf.WriteString("\r\x1b[J")
	l.widths = nil
	_, _ = l.frameW.Write(buf.Bytes())
	buf.Reset()
	output.Infof(&buf, "%s", line)
	_, _ = l.w.Write(buf.Bytes())
}

// moveUp writes the cursor movement to the top of the previous frame. Lines
// wider than the terminal now (it was narrowed) have wrapped into more rows.
func (l *Live) moveUp(buf *bytes.Buffer, cols int) {
	rows := 0
	for _, w := range l.widths {
		rows += max(1, (w+max(1, cols)-1)/max(1, cols))
	}
	if rows > 0 {
		fmt.Fprintf(buf, "\x1b[%dA", rows)
	}
}

// summary is the part of the summary line after the elapsed time. The caller
// holds mu.
func (l *Live) summary() string {
	var b strings.Builder
	for _, s := range []*liveSource{l.prov, l.store} {
		if s == nil {
			continue
		}
		// A provider resets its counters when a read starts.
		base, baseLim := s.base, s.baseLim
		if s.req < base || s.lim < baseLim {
			base, baseLim = 0, 0
		}
		req, lim := s.req-base, s.lim-baseLim
		if req <= 0 {
			continue
		}
		fmt.Fprintf(&b, " · %s %s request%s", s.name, num(req), plural(req))
		if lim > 0 {
			fmt.Fprintf(&b, ", %s rate limited", num(lim))
		}
	}
	if l.memTop > 0 {
		b.WriteString(" · peak memory " + size(l.memTop))
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (l *Live) loop(stop <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	t := time.NewTicker(redrawEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			l.frame()
		}
	}
}

// sample reads the counters once and takes the rate and memory samples, each
// at most once a second. The caller holds mu.
func (l *Live) sample(now time.Time) {
	if l.provFn != nil {
		l.stats = l.provFn()
		l.prov.set(l.stats.Requests, l.stats.RateLimited)
	}
	if l.meter != nil {
		l.store.set(l.meter.Requests(), l.meter.RateLimited())
	}
	for _, s := range []*liveSource{l.prov, l.store} {
		if s != nil {
			s.rate.add(now, s.req)
		}
	}
	if l.cur != nil {
		l.cur.items.add(now, l.cur.done)
	}
	if l.memAt.IsZero() || now.Sub(l.memAt) >= time.Second {
		l.memAt, l.memNow = now, l.mem()
		l.memTop = max(l.memTop, l.memNow)
	}
}

// snapshot builds the card's state from the live one. The caller holds mu.
func (l *Live) snapshot(now time.Time) state {
	s := state{title: l.title, elapsed: now.Sub(l.started), frame: l.frames, mem: l.memNow, top: l.memTop}
	for _, p := range l.phases {
		ph := p.phase
		if p.state == running {
			ph.rate = p.items.rate()
			ph.left = eta(now.Sub(p.stepBegan), ph.done, ph.total)
		}
		s.phases = append(s.phases, ph)
	}
	for _, src := range []*liveSource{l.prov, l.store} {
		if src == nil {
			continue
		}
		out := source{name: src.name, requests: src.req, limited: src.lim, rate: src.rate.rate()}
		if src == l.prov {
			out.pace = l.stats.Pace
		}
		s.sources = append(s.sources, out)
	}
	pal := newPalette()
	for _, c := range l.stats.Counts {
		if c.N > 0 {
			s.data = append(s.data, pal.bold.Render(num(c.N))+pal.subtle.Render(" "+typeName(c.Type, c.N)))
		}
	}
	return s
}

// frame draws one frame in place of the previous one, with a single Write.
func (l *Live) frame() {
	l.omu.Lock()
	defer l.omu.Unlock()
	l.mu.Lock()
	if !l.drawing {
		l.mu.Unlock()
		return
	}
	now := l.now()
	l.sample(now)
	st := l.snapshot(now)
	l.frames++
	l.mu.Unlock()

	cols, rows := l.size()
	card := view(st, cols, rows)
	if l.plain {
		card = ansi.Strip(card)
	}
	var lines []string
	if card != "" {
		lines = strings.Split(card, "\n")
	}
	var buf bytes.Buffer
	l.moveUp(&buf, cols)
	buf.WriteString("\r")
	widths := make([]int, len(lines))
	for i, line := range lines {
		buf.WriteString(line + "\x1b[K\n")
		widths[i] = lipgloss.Width(line)
	}
	buf.WriteString("\x1b[J")
	l.widths = widths
	_, _ = l.frameW.Write(buf.Bytes())
}
