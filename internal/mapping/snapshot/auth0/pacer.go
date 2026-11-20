package auth0

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	maxSends     = 10 // sends of one request, the first included
	minGap       = 100 * time.Millisecond
	maxGap       = 2 * time.Second
	gapFloor     = 5 * time.Millisecond
	maxRetryWait = 60 * time.Second
	defaultWait  = time.Second
	userAgent    = "ofga-cli"
)

// pacer is the HTTP transport of every Management API request of a provider.
// It spaces requests out, slows down when Auth0 answers 429 and speeds back up
// on success, and counts the 429 answers of the read in progress. It reads the
// status code and Retry-After header of each response and nothing else.
type pacer struct {
	base  http.RoundTripper
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error // replaceable in tests

	mu     sync.Mutex
	gap    time.Duration // spacing between request starts, 0 when not rate limited
	next   time.Time     // earliest start of the next request
	n      int
	onWait func()
}

func newPacer(base http.RoundTripper) *pacer {
	return &pacer{base: base, now: time.Now, sleep: sleepContext}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// begin starts counting for a new read; onWait runs on each 429.
func (p *pacer) begin(onWait func()) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.n, p.onWait = 0, onWait
}

// count is how many 429 answers the read has had.
func (p *pacer) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.n
}

// slot reserves the next start time and returns how long to wait for it.
func (p *pacer) slot() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()

	start := now
	if p.next.After(start) {
		start = p.next
	}

	p.next = start.Add(p.gap)

	return start.Sub(now)
}

// succeeded speeds back up after an answer that is not a 429.
func (p *pacer) succeeded() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.gap = p.gap * 19 / 20
	if p.gap < gapFloor {
		p.gap = 0
	}
}

// limited records a 429 answer: it counts, widens the gap and closes the gate
// for the time Auth0 asked. It returns the progress callback to run.
func (p *pacer) limited(resp *http.Response) func() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.n++

	p.gap = min(max(2*p.gap, minGap), maxGap)

	if reopen := p.now().Add(retryAfter(resp)); reopen.After(p.next) {
		p.next = reopen
	}

	return p.onWait
}

// retryAfter is the Retry-After header as whole seconds, 1 s when it is
// missing or not a positive integer, at most 60 s.
func retryAfter(resp *http.Response) time.Duration {
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || secs <= 0 {
		return defaultWait
	}

	if secs > int(maxRetryWait/time.Second) {
		return maxRetryWait
	}

	return time.Duration(secs) * time.Second
}

func (p *pacer) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	for sent := 1; ; sent++ {
		if wait := p.slot(); wait > 0 {
			if err := p.sleep(ctx, wait); err != nil {
				if req.Body != nil {
					_ = req.Body.Close()
				}

				return nil, err
			}
		}

		// Our client replaces the one the SDK builds, so the User-Agent is set here.
		out := req.Clone(ctx)
		out.Header.Set("User-Agent", userAgent)

		resp, err := p.base.RoundTrip(out)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			p.succeeded()

			return resp, nil
		}

		onWait := p.limited(resp)

		// A body that cannot be produced again is not sent twice; the caller
		// gets the 429 and decides. Only a 429 that is followed by another
		// send is reported as a wait.
		resendable := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
		if sent >= maxSends || !resendable {
			return resp, nil
		}

		if onWait != nil {
			onWait()
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if req.GetBody != nil && req.Body != nil && req.Body != http.NoBody {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}

			req = req.Clone(ctx)
			req.Body = body
		}
	}
}
