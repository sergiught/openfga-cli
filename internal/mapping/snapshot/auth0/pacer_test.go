package auth0

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
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

	resp := &http.Response{StatusCode: s.statuses[idx], Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}
	for k, v := range s.headers {
		resp.Header.Set(k, v)
	}

	return resp, nil
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
	noErr(t, err)

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

	resp, err := get(t, p)
	noErr(t, err)
	equal(t, http.StatusTooManyRequests, resp.StatusCode)

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
	noErr(t, err)
	equal(t, http.StatusTooManyRequests, resp.StatusCode)
	equal(t, 10, s.sends)
	equal(t, 10, p.count())
	equal(t, 9, waited) // the tenth 429 is not followed by a send
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
