package auth0

import (
	"context"
	"errors"
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
	maxDrain     = 4 << 10
)

// errKeptRateLimiting is what a request gets back when all its sends were
// answered 429. A transport error is not retried by the SDK, which would
// otherwise sleep where Ctrl-C cannot reach and start over.
var errKeptRateLimiting = errors.New(
	"Auth0 kept rate-limiting the read (10 answers of HTTP 429 in a row for one request); wait a minute and run it again")

// pacer is the HTTP transport of every Management API request of a provider.
// It spaces requests out, slows down when Auth0 answers 429 and speeds back up
// on success, and counts every request sent and the 429 answers of the read in
// progress. It reads the status code and Retry-After header of each response
// and nothing else.
type pacer struct {
	base  http.RoundTripper
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error // replaceable in tests

	mu     sync.Mutex
	gap    time.Duration // spacing between request starts, 0 when not rate limited
	next   time.Time     // earliest start of the next request
	closed time.Time     // no request is sent before this, the latest Retry-After
	widen  time.Time     // when the gap was last widened
	n      int           // answers of HTTP 429
	sent   int           // every send, resends included
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

	p.n, p.sent, p.onWait = 0, 0, onWait
}

// count is how many 429 answers the read has had.
func (p *pacer) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.n
}

// stats is how many requests the read has sent, how many were answered 429 and
// the spacing currently kept between requests.
func (p *pacer) stats() (sent, limited int, gap time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.sent, p.n, p.gap
}

// sending counts one send.
func (p *pacer) sending() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.sent++
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

// isClosed says whether Auth0 asked us to hold back at this moment.
func (p *pacer) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.now().Before(p.closed)
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

// limited records a 429 answer to a request sent at sentAt: it counts and
// closes the pacer for the time Auth0 asked. It widens the gap only if the
// request was sent after the last widening, so one dry spell that answers
// many requests in flight widens it once. It returns the progress callback.
func (p *pacer) limited(resp *http.Response, sentAt time.Time) func() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.n++

	now := p.now()

	if sentAt.After(p.widen) {
		p.gap = min(max(2*p.gap, minGap), maxGap)
		p.widen = now
	}

	reopen := now.Add(retryAfter(resp))
	if reopen.After(p.closed) {
		p.closed = reopen
	}

	if reopen.After(p.next) {
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
		// Take a slot and wait for it; if the pacer was closed meanwhile, take
		// a new slot, which starts at or after the reopening.
		for {
			if wait := p.slot(); wait > 0 {
				if err := p.sleep(ctx, wait); err != nil {
					if req.Body != nil {
						_ = req.Body.Close()
					}

					return nil, err
				}
			}

			if !p.isClosed() {
				break
			}
		}

		sentAt := p.now()

		// Our client replaces the one the SDK builds, so the User-Agent is set here.
		out := req.Clone(ctx)
		out.Header.Set("User-Agent", userAgent)

		p.sending()

		resp, err := p.base.RoundTrip(out)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			p.succeeded()

			return resp, nil
		}

		onWait := p.limited(resp, sentAt)

		// A body that cannot be produced again is not sent twice; the caller
		// gets the 429 and decides. Only a 429 that is followed by another
		// send is reported as a wait.
		resendable := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
		if !resendable {
			return resp, nil
		}

		if sent >= maxSends {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain))
			_ = resp.Body.Close()

			return nil, errKeptRateLimiting
		}

		if onWait != nil {
			onWait()
		}

		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain))
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
