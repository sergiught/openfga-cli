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
type Live struct {
	size  func() (cols, rows int)
	now   func() time.Time
	mem   func() uint64
	async bool // Phase starts a goroutine that redraws every 100 ms

	mu      sync.Mutex
	w       io.Writer
	title   string
	phases  []*livePhase
	prov    *liveSource
	store   *liveSource
	counts  func() []snapshot.Count
	pace    func() time.Duration
	drawing bool
	started time.Time // when drawing last started or resumed
	lines   int       // lines of the previous frame
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

// liveSource is a counter pair polled each frame, with the values at the last
// summary line.
type liveSource struct {
	name          string
	read          func() (requests, limited int)
	rate          window
	base, baseLim int
}

// New returns a live status drawing on w, or nil when w is not a terminal or
// output is quiet, plain, terse or not interactive.
func New(w io.Writer, title string, phases ...string) *Live {
	f, ok := w.(*os.File)
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
	l.async = true
	return l
}

func newLive(w io.Writer, size func() (int, int), now func() time.Time, mem func() uint64, title string, phases ...string) *Live {
	l := &Live{w: w, size: size, now: now, mem: mem, title: title}
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

// Provider sets the identity provider whose requests and items are shown. The
// stats function is polled each frame.
func (l *Live) Provider(label string, stats func() snapshot.Stats) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prov = &liveSource{name: label, read: func() (int, int) {
		s := stats()
		return s.Requests, s.RateLimited
	}}
	l.counts = func() []snapshot.Count { return stats().Counts }
	l.pace = func() time.Duration { return stats().Pace }
}

// Store sets the meter of the store's requests, polled each frame.
func (l *Live) Store(m *client.Meter) {
	if l == nil || m == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store = &liveSource{name: "store", read: func() (int, int) { return m.Requests(), m.RateLimited() }}
}

// Phase starts the named phase running; the phase that was running is done.
// It starts or resumes drawing.
func (l *Live) Phase(name string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
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
	if !l.drawing {
		l.drawing, l.started, l.lines = true, now, 0
		if l.async {
			l.stop, l.stopped = make(chan struct{}), make(chan struct{})
			go l.loop(l.stop, l.stopped)
		}
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

	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.finish(now)
	l.poll(now)
	var buf bytes.Buffer
	if l.lines > 0 {
		fmt.Fprintf(&buf, "\x1b[%dA", l.lines)
	}
	buf.WriteString("\r\x1b[J")
	l.lines = 0
	output.Infof(&buf, "%s", lead+dur(now.Sub(l.started))+l.summary())
	_, _ = l.w.Write(buf.Bytes())
	for _, s := range []*liveSource{l.prov, l.store} {
		if s != nil {
			s.base, s.baseLim = s.read()
		}
	}
}

// summary is the part of the summary line after the elapsed time.
func (l *Live) summary() string {
	var b strings.Builder
	for _, s := range []*liveSource{l.prov, l.store} {
		if s == nil {
			continue
		}
		req, lim := s.read()
		req, lim = req-s.base, lim-s.baseLim
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

// poll samples the counters and memory, each at most once a second.
func (l *Live) poll(now time.Time) {
	for _, s := range []*liveSource{l.prov, l.store} {
		if s != nil {
			req, _ := s.read()
			s.rate.add(now, req)
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
		req, lim := src.read()
		out := source{name: src.name, requests: req, limited: lim, rate: src.rate.rate()}
		if src == l.prov && l.pace != nil {
			out.pace = l.pace()
		}
		s.sources = append(s.sources, out)
	}
	if l.counts != nil {
		pal := newPalette()
		for _, c := range l.counts() {
			if c.N > 0 {
				s.data = append(s.data, pal.bold.Render(num(c.N))+pal.subtle.Render(" "+typeName(c.Type, c.N)))
			}
		}
	}
	return s
}

// frame draws one frame in place of the previous one, with a single Write.
func (l *Live) frame() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.drawing {
		return
	}
	now := l.now()
	l.poll(now)
	cols, rows := l.size()
	card := view(l.snapshot(now), cols, rows)
	l.frames++
	var buf bytes.Buffer
	if l.lines > 0 {
		fmt.Fprintf(&buf, "\x1b[%dA", l.lines)
	}
	buf.WriteString("\r")
	lines := strings.Split(card, "\n")
	if card == "" {
		lines = nil
	}
	for _, line := range lines {
		buf.WriteString(line + "\x1b[K\n")
	}
	buf.WriteString("\x1b[J")
	l.lines = len(lines)
	_, _ = l.w.Write(buf.Bytes())
}
