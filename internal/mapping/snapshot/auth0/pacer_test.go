package auth0

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a clock whose sleeps advance it instead of waiting.
type fakeClock struct {
	mu    sync.Mutex
	t     time.Time
	waits []time.Duration
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.waits = append(c.waits, d)
	c.t = c.t.Add(d)

	return nil
}

// script answers each send with the next status, the last one forever, and
// records when each send happened and what body it carried.
type script struct {
	clock    *fakeClock
	statuses []int
	headers  map[string]string

	closed atomic.Int64 // response bodies closed by the pacer

	mu     sync.Mutex
	sends  int
	times  []time.Time
	bodies []string
	onSend func()
}

func (s *script) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	idx := min(s.sends, len(s.statuses)-1)
	s.sends++
	s.times = append(s.times, s.clock.now())
	onSend := s.onSend
	s.mu.Unlock()

	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)

		s.mu.Lock()
		s.bodies = append(s.bodies, string(b))
		s.mu.Unlock()
	}

	if onSend != nil {
		onSend()
	}

	resp := &http.Response{StatusCode: s.statuses[idx], Header: http.Header{}, Body: &closeCounter{Reader: strings.NewReader(""), n: &s.closed}}
	for k, v := range s.headers {
		resp.Header.Set(k, v)
	}

	return resp, nil
}

type closeCounter struct {
	io.Reader
	n *atomic.Int64
}

func (c *closeCounter) Close() error {
	c.n.Add(1)

	return nil
}

func newTestPacer(statuses ...int) (*pacer, *script, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)}
	s := &script{clock: clock, statuses: statuses, headers: map[string]string{}}
	p := newPacer(s)
	p.now, p.sleep = clock.now, clock.sleep

	return p, s, clock
}

func get(t *testing.T, rt http.RoundTripper) (*http.Response, error) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	if err != nil {
		return nil, err
	}

	return rt.RoundTrip(req)
}

func TestPacerRetriesAfterRateLimit(t *testing.T) {
	t.Parallel()

	p, s, clock := newTestPacer(http.StatusTooManyRequests, http.StatusOK)
	waited := 0
	p.begin(func() { waited++ })

	resp, err := get(t, p)
	noErr(t, err)
	equal(t, http.StatusOK, resp.StatusCode)
	equal(t, 2, s.sends)
	equal(t, 1, p.count())
	equal(t, 1, waited)
	equal(t, []time.Duration{time.Second}, clock.waits)

	p.begin(nil) // a new read counts from zero
	equal(t, 0, p.count())
}

func TestPacerRetryAfter(t *testing.T) {
	t.Parallel()

	for header, want := range map[string]time.Duration{
		"3":       3 * time.Second,
		"":        time.Second,
		"soon":    time.Second,
		"0":       time.Second,
		"-4":      time.Second,
		"2.5":     time.Second,
		"500":     60 * time.Second,
		"60":      60 * time.Second,
		"9999999": 60 * time.Second,
	} {
		p, s, clock := newTestPacer(http.StatusTooManyRequests, http.StatusOK)
		if header != "" {
			s.headers["Retry-After"] = header
		}

		_, err := get(t, p)
		noErr(t, err)
		equal(t, []time.Duration{want}, clock.waits)
	}
}

func TestPacerGap(t *testing.T) {
	t.Parallel()

	p, s, _ := newTestPacer(http.StatusTooManyRequests)

	var gaps []time.Duration

	s.onSend = func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		gaps = append(gaps, p.gap)
	}

	_, err := get(t, p)
	if !errors.Is(err, errKeptRateLimiting) {
		t.Fatalf("error = %v, want errKeptRateLimiting", err)
	}

	// The gap each send saw: none until the first 429, then 100 ms, doubling,
	// never above 2 s.
	equal(t, []time.Duration{
		0, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond,
		1600 * time.Millisecond, 2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second,
	}, gaps)

	// Successes bring it down to nothing.
	s.statuses, s.sends = []int{http.StatusOK}, 0

	prev := 2 * time.Second

	for i := 0; i < 500 && prev > 0; i++ {
		_, err := get(t, p)
		noErr(t, err)

		p.mu.Lock()
		cur := p.gap
		p.mu.Unlock()

		if cur >= prev {
			t.Fatalf("gap did not shrink: %v after %v", cur, prev)
		}

		prev = cur
	}

	equal(t, time.Duration(0), prev)
}

func TestPacerSecondRequestWaitsForTheReopening(t *testing.T) {
	t.Parallel()

	p, s, clock := newTestPacer(http.StatusTooManyRequests, http.StatusOK)
	s.headers["Retry-After"] = "3"
	start := clock.now()

	// A request that cannot be sent again gets the 429 and leaves the pacer closed.
	req, err := http.NewRequest(http.MethodPost, "http://example.invalid/", io.NopCloser(strings.NewReader("x")))
	noErr(t, err)

	resp, err := p.RoundTrip(req)
	noErr(t, err)
	equal(t, http.StatusTooManyRequests, resp.StatusCode)
	equal(t, 1, s.sends)

	resp, err = get(t, p)
	noErr(t, err)
	equal(t, http.StatusOK, resp.StatusCode)
	equal(t, []time.Duration{3 * time.Second}, clock.waits)
	equal(t, []time.Time{start, start.Add(3 * time.Second)}, s.times)
}

func TestPacerCanceledWhileWaiting(t *testing.T) {
	t.Parallel()

	p, s, _ := newTestPacer(http.StatusTooManyRequests, http.StatusOK)
	ctx, cancel := context.WithCancel(context.Background())

	t.Cleanup(cancel)

	p.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()

		return ctx.Err()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/", nil)
	noErr(t, err)

	resp, err := p.RoundTrip(req)
	if resp != nil {
		t.Errorf("response = %v, want nil", resp)
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}

	equal(t, 1, s.sends)
}

func TestPacerGivesUpAfterTenSends(t *testing.T) {
	t.Parallel()

	p, s, _ := newTestPacer(http.StatusTooManyRequests)
	waited := 0
	p.begin(func() { waited++ })

	resp, err := get(t, p)
	if resp != nil {
		t.Errorf("response = %v, want nil", resp)
	}

	if !errors.Is(err, errKeptRateLimiting) {
		t.Fatalf("error = %v, want errKeptRateLimiting", err)
	}

	equal(t, "Auth0 kept rate-limiting the read (10 answers of HTTP 429 in a row for one request); "+
		"wait a minute and run it again", err.Error())
	equal(t, 10, s.sends)
	equal(t, 10, p.count())
	equal(t, 9, waited) // the tenth 429 is not followed by a send
	equal(t, 10, int(s.closed.Load()))
}

func TestPacerReportsNoWaitWhenNothingIsSentAgain(t *testing.T) {
	t.Parallel()

	p, _, _ := newTestPacer(http.StatusTooManyRequests)
	waited := 0
	p.begin(func() { waited++ })

	req, err := http.NewRequest(http.MethodPost, "http://example.invalid/", io.NopCloser(strings.NewReader("x")))
	noErr(t, err)

	_, err = p.RoundTrip(req)
	noErr(t, err)
	equal(t, 1, p.count())
	equal(t, 0, waited)
}

func TestPacerSetsUserAgentOnACopy(t *testing.T) {
	t.Parallel()

	var seen http.Header

	p := newPacer(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = r.Header

		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}))

	req, err := http.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	noErr(t, err)
	req.Header.Set("User-Agent", "other")

	_, err = p.RoundTrip(req)
	noErr(t, err)
	equal(t, "ofga-cli", seen.Get("User-Agent"))
	equal(t, "other", req.Header.Get("User-Agent"))
}

func TestPacerResendsBodyOnlyWithGetBody(t *testing.T) {
	t.Parallel()

	p, s, _ := newTestPacer(http.StatusTooManyRequests, http.StatusOK)

	req, err := http.NewRequest(http.MethodPost, "http://example.invalid/", strings.NewReader("payload"))
	noErr(t, err)

	resp, err := p.RoundTrip(req)
	noErr(t, err)
	equal(t, http.StatusOK, resp.StatusCode)
	equal(t, []string{"payload", "payload"}, s.bodies)

	p, s, _ = newTestPacer(http.StatusTooManyRequests, http.StatusOK)

	req, err = http.NewRequest(http.MethodPost, "http://example.invalid/", io.NopCloser(strings.NewReader("payload")))
	noErr(t, err)

	resp, err = p.RoundTrip(req)
	noErr(t, err)
	equal(t, http.StatusTooManyRequests, resp.StatusCode)
	equal(t, 1, s.sends)
}

func TestPacerSlotsSpaceRequestsByTheGap(t *testing.T) {
	t.Parallel()

	p, _, _ := newTestPacer(http.StatusOK)
	p.gap = 100 * time.Millisecond

	// With the clock standing still, requests that arrive together start one
	// gap apart.
	equal(t, []time.Duration{0, 100 * time.Millisecond, 200 * time.Millisecond},
		[]time.Duration{p.slot(), p.slot(), p.slot()})
}

func TestPacerWidensOncePerWindow(t *testing.T) {
	t.Parallel()

	const inFlight = 8

	p, s, _ := newTestPacer(http.StatusTooManyRequests, http.StatusTooManyRequests, http.StatusTooManyRequests,
		http.StatusTooManyRequests, http.StatusTooManyRequests, http.StatusTooManyRequests,
		http.StatusTooManyRequests, http.StatusTooManyRequests, http.StatusOK)

	// Every request is in flight before any answer comes back.
	var arrived sync.WaitGroup

	arrived.Add(inFlight)

	gate := make(chan struct{})

	var once sync.Once

	s.onSend = func() {
		s.mu.Lock()
		first := s.sends <= inFlight
		s.mu.Unlock()

		if first {
			arrived.Done()
			<-gate
		}
	}

	go func() {
		arrived.Wait()
		once.Do(func() { close(gate) })
	}()

	var done sync.WaitGroup

	for range inFlight {
		done.Add(1)

		go func() {
			defer done.Done()

			_, err := get(t, p)
			if err != nil {
				t.Error(err)

				return
			}
		}()
	}

	done.Wait()

	p.mu.Lock()
	gap := p.gap
	p.mu.Unlock()

	if gap == 0 || gap > minGap {
		t.Errorf("gap = %v after %d answers of 429 at once, want %v at most, not widened %d times", gap, inFlight, minGap, inFlight)
	}
}

func TestPacerDoesNotSendIntoAClosedPacer(t *testing.T) {
	t.Parallel()

	p, s, clock := newTestPacer(http.StatusTooManyRequests, http.StatusOK)
	s.headers["Retry-After"] = "3"
	p.gap = 100 * time.Millisecond
	start := clock.now()

	aSent, aRelease := make(chan struct{}), make(chan struct{})
	bWaiting, bRelease := make(chan struct{}), make(chan struct{})

	s.onSend = func() {
		close(aSent)
		<-aRelease
	}

	p.sleep = func(ctx context.Context, d time.Duration) error {
		if d == 100*time.Millisecond { // request B's first wait, for its slot
			close(bWaiting)
			<-bRelease
		}

		return clock.sleep(ctx, d)
	}

	var wg sync.WaitGroup

	// A cannot be sent again, so it gets its 429 and leaves the pacer closed.
	wg.Add(1)

	go func() {
		defer wg.Done()

		req, err := http.NewRequest(http.MethodPost, "http://example.invalid/", io.NopCloser(strings.NewReader("x")))
		if err != nil {
			t.Error(err)

			return
		}

		resp, err := p.RoundTrip(req)
		if err != nil {
			t.Error(err)

			return
		}
		equal(t, http.StatusTooManyRequests, resp.StatusCode)
	}()

	<-aSent

	// B reserves its slot, 100 ms out, while A is still in flight.
	wg.Add(1)

	go func() {
		defer wg.Done()

		s.mu.Lock()
		s.onSend = nil
		s.mu.Unlock()

		resp, err := get(t, p)
		if err != nil {
			t.Error(err)

			return
		}
		equal(t, http.StatusOK, resp.StatusCode)
	}()

	<-bWaiting
	close(aRelease)

	// A's 429 closes the pacer for 3 s, then B's wait for its slot ends.
	for p.count() == 0 {
		time.Sleep(time.Millisecond)
	}

	close(bRelease)
	wg.Wait()

	equal(t, 2, len(s.times))
	equal(t, start.Add(3*time.Second), s.times[1])
}

// vclock is a clock for goroutines: time moves only when every worker is
// asleep on it, to the earliest wake-up.
type vclock struct {
	mu       sync.Mutex
	t        time.Time
	active   int
	sleepers []*vsleeper
}

type vsleeper struct {
	at time.Time
	ch chan struct{}
}

func (c *vclock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *vclock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	s := &vsleeper{at: c.t.Add(d), ch: make(chan struct{})}
	c.sleepers = append(c.sleepers, s)
	c.advance()
	c.mu.Unlock()

	<-s.ch

	return nil
}

func (c *vclock) finished() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.active--
	c.advance()
}

// advance wakes the earliest sleepers when nothing else is running.
func (c *vclock) advance() {
	if c.active == 0 || len(c.sleepers) < c.active {
		return
	}

	earliest := c.sleepers[0].at
	for _, s := range c.sleepers {
		if s.at.Before(earliest) {
			earliest = s.at
		}
	}

	if earliest.After(c.t) {
		c.t = earliest
	}

	rest := c.sleepers[:0]

	for _, s := range c.sleepers {
		if s.at.After(c.t) {
			rest = append(rest, s)
		} else {
			close(s.ch)
		}
	}

	c.sleepers = rest
}

// TestPacerAgainstATokenBucket drives the whole pacer, 8 workers at a time,
// against a tenant that allows a burst of 10 requests and then 2 a second,
// and wants the read to take about as long as that rate allows.
func TestPacerAgainstATokenBucket(t *testing.T) {
	t.Parallel()

	const (
		requests = 200
		workers  = 8
		burst    = 10
		perSec   = 2.0
		latency  = 100 * time.Millisecond
	)

	clock := &vclock{t: time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC), active: workers}
	begin := clock.t
	tokens, last := float64(burst), begin

	var bucket sync.Mutex

	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		bucket.Lock()
		at := clock.now()
		tokens = min(burst, tokens+at.Sub(last).Seconds()*perSec)
		last = at

		status := http.StatusOK
		if tokens >= 1 {
			tokens--
		} else {
			status = http.StatusTooManyRequests
		}

		bucket.Unlock()

		if err := clock.sleep(context.Background(), latency); err != nil {
			return nil, err
		}

		resp := &http.Response{StatusCode: status, Header: http.Header{}, Body: http.NoBody}
		resp.Header.Set("Retry-After", "1")

		return resp, nil
	})

	p := newPacer(base)
	p.now, p.sleep = clock.now, clock.sleep

	var (
		mu     sync.Mutex
		issued int
		wg     sync.WaitGroup
	)

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()
			defer clock.finished()

			for {
				mu.Lock()
				issued++
				mine := issued
				mu.Unlock()

				if mine > requests {
					return
				}

				req, err := http.NewRequest(http.MethodGet, "http://example.invalid/", nil)
				if err != nil {
					t.Error(err)
					return
				}

				resp, err := p.RoundTrip(req)
				if err != nil {
					t.Error(err)
					return
				}
				equal(t, http.StatusOK, resp.StatusCode)
			}
		}()
	}

	wg.Wait()

	took := clock.now().Sub(begin)
	ideal := time.Duration(float64(requests-burst) / perSec * float64(time.Second))

	if took > ideal*115/100 {
		t.Errorf("%d requests took %v, want within 15%% of %v", requests, took, ideal)
	}
}
