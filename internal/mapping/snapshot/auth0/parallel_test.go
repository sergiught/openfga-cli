package auth0

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// tenant serves n organizations, each with one enabled connection and one
// member, and n connections, each with one enabled client. before runs first
// on every request and may answer it, in which case it returns true.
type tenant struct {
	n      int
	before func(w http.ResponseWriter, r *http.Request) bool
}

var tenantTypes = []string{
	"snapshot.connection", "snapshot.organization", "snapshot.organization.connection", "snapshot.organization.member",
}

func (tn tenant) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tn.before != nil && tn.before(w, r) {
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/api/v2")

		var orgs, conns []string
		for i := range tn.n {
			orgs = append(orgs, fmt.Sprintf(`{"id":"org_%02d","name":"acme-%02d"}`, i, i))
			conns = append(conns, fmt.Sprintf(`{"id":"con_%02d","name":"c%02d","strategy":"auth0"}`, i, i))
		}

		var id int

		switch {
		case path == "/organizations":
			writeJSON(w, `{"organizations":[`+strings.Join(orgs, ",")+`]}`)
		case path == "/connections":
			writeJSON(w, `{"connections":[`+strings.Join(conns, ",")+`]}`)
		case scan(path, "/connections/con_%02d/clients", &id):
			writeJSON(w, fmt.Sprintf(`{"clients":[{"client_id":"cl_%02d"}]}`, id))
		case scan(path, "/organizations/org_%02d/enabled_connections", &id):
			if page := r.URL.Query().Get("page"); page != "" && page != "0" {
				writeJSON(w, `{"enabled_connections":[],"start":1,"limit":100,"total":1}`)

				return
			}

			writeJSON(w, fmt.Sprintf(`{"enabled_connections":[{"connection_id":"con_%02d","connection":{"name":"c","strategy":"auth0"}}],`+
				`"start":0,"limit":100,"total":1}`, id))
		case scan(path, "/organizations/org_%02d/members", &id):
			writeJSON(w, fmt.Sprintf(`{"members":[{"user_id":"auth0|u%02d","email":"u@x.io","name":"U","picture":"p"}]}`, id))
		default:
			http.Error(w, "no fixture for "+path, http.StatusNotFound)
		}
	}
}

func scan(path, format string, id *int) bool {
	n, err := fmt.Sscanf(path, format, id)

	return err == nil && n == 1 && fmt.Sprintf(format, *id) == path
}

// expectedSubjects is the sequential read of a tenant of n organizations.
func expectedSubjects(n int) map[string][]string {
	want := map[string][]string{}

	for i := range n {
		want["snapshot.connection"] = append(want["snapshot.connection"], fmt.Sprintf("connection con_%02d", i))
		want["snapshot.organization"] = append(want["snapshot.organization"], fmt.Sprintf("organization org_%02d", i))
		want["snapshot.organization.connection"] = append(want["snapshot.organization.connection"],
			fmt.Sprintf("organization org_%02d connection con_%02d", i, i))
		want["snapshot.organization.member"] = append(want["snapshot.organization.member"],
			fmt.Sprintf("organization org_%02d member auth0|u%02d", i, i))
	}

	return want
}

func subjectsByType(events []map[string]any) map[string][]string {
	got := map[string][]string{}

	for _, e := range events {
		typ, _ := e["type"].(string)
		subject, _ := e["subject"].(string)
		got[typ] = append(got[typ], subject)
	}

	return got
}

// withoutTimes returns the events without their arrival times and sources, which differ between servers.
func withoutTimes(events []map[string]any) []map[string]any {
	out := make([]map[string]any, len(events))

	for i, e := range events {
		c := map[string]any{}

		for k, v := range e {
			if k != "time" && k != "source" {
				c[k] = v
			}
		}

		out[i] = c
	}

	return out
}

func TestSnapshotRateLimitedReadMatchesUnthrottled(t *testing.T) {
	t.Parallel()

	plain, _ := newProvider(t, tenant{n: 6}.handler())
	want, err := plain.Snapshot(context.Background(), tenantTypes, nil)
	noErr(t, err)
	equal(t, 0, want.RateLimited)

	var served atomic.Int64

	p, _ := newProvider(t, tenant{n: 6, before: func(w http.ResponseWriter, _ *http.Request) bool {
		if served.Add(1) > 7 {
			return false
		}

		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)

		return true
	}}.handler())

	var (
		mu   sync.Mutex
		said []string
	)

	got, err := p.Snapshot(context.Background(), tenantTypes, func(msg string) {
		mu.Lock()
		defer mu.Unlock()

		said = append(said, msg)
	})
	noErr(t, err)
	equal(t, withoutTimes(want.Events), withoutTimes(got.Events))
	equal(t, 7, got.RateLimited)
	contains(t, strings.Join(said, "\n"), "waiting on Auth0's rate limit")
}

func TestSnapshotParallelKeepsOrganizationOrder(t *testing.T) {
	t.Parallel()

	const n = 20

	// Later organizations answer first.
	p, _ := newProvider(t, tenant{n: n, before: func(_ http.ResponseWriter, r *http.Request) bool {
		var id int
		if _, err := fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/api/v2/organizations/org_"), "%d", &id); err == nil {
			time.Sleep(time.Duration(n-id) * time.Millisecond)
		}

		return false
	}}.handler())

	var (
		mu   sync.Mutex
		said []string
	)

	res, err := p.Snapshot(context.Background(), tenantTypes, func(msg string) {
		mu.Lock()
		defer mu.Unlock()

		said = append(said, msg)
	})
	noErr(t, err)
	equal(t, expectedSubjects(n), subjectsByType(res.Events))

	// Progress counts finished organizations and never goes back.
	for _, prefix := range []string{"reading connections of ", "reading members of "} {
		last := 0

		for _, msg := range said {
			var done int
			if rest, ok := strings.CutPrefix(msg, prefix); ok {
				if _, err := fmt.Sscanf(rest, "%d/%d organizations", &done, new(int)); err != nil {
					t.Fatalf("progress %q: %v", msg, err)
				}

				if done <= last {
					t.Errorf("progress %q after %d organizations", msg, last)
				}

				last = done
			}
		}

		equal(t, n, last)
	}
}

func TestSnapshotRunsAtMostReadWorkersRequestsAtOnce(t *testing.T) {
	t.Parallel()

	var (
		inFlight, peak atomic.Int64
		timedOut       atomic.Bool

		mu       sync.Mutex
		arrivals int
	)

	second := make(chan struct{})

	p, _ := newProvider(t, tenant{n: 20, before: func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/v2/organizations" || r.URL.Path == "/api/v2/connections" {
			return false
		}

		cur := inFlight.Add(1)
		defer inFlight.Add(-1)

		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}

		// The first request waits until a second one is in flight, so a
		// sequential read cannot pass whatever the machine's speed.
		mu.Lock()
		arrivals++
		me := arrivals
		mu.Unlock()

		switch me {
		case 1:
			select {
			case <-second:
			case <-time.After(10 * time.Second):
				timedOut.Store(true)
			}
		case 2:
			close(second)
		}

		time.Sleep(10 * time.Millisecond)

		return false
	}}.handler())

	_, err := p.Snapshot(context.Background(), tenantTypes, nil)
	noErr(t, err)

	if timedOut.Load() {
		t.Error("no second request was in flight while the first waited: the read is not parallel")
	}

	if got := peak.Load(); got > readWorkers {
		t.Errorf("peak requests in flight = %d, want at most %d", got, readWorkers)
	}
}

type countingTransport struct {
	base   http.RoundTripper
	active atomic.Int64
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.active.Add(1)
	defer c.active.Add(-1)

	return c.base.RoundTrip(r)
}

func TestSnapshotCanceledDuringParallelReadLeavesNothingRunning(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var arrivals atomic.Int64

	p, _ := newProvider(t, tenant{n: 20, before: func(_ http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/members") && arrivals.Add(1) == 3 {
			cancel()
		}

		time.Sleep(20 * time.Millisecond)

		return false
	}}.handler())

	ct := &countingTransport{base: p.limits.base}
	p.limits.base = ct

	res, err := p.Snapshot(ctx, []string{"snapshot.organization.member"}, nil)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}

	equal(t, int64(0), ct.active.Load())
}

func TestSnapshotParallelFailureNamesTheCause(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, tenant{n: 20, before: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/v2/organizations/org_05/members" {
			time.Sleep(10 * time.Millisecond)
			w.WriteHeader(http.StatusForbidden)
			writeJSON(w, `{"message":"Insufficient scope"}`)

			return true
		}

		time.Sleep(5 * time.Millisecond)

		return false
	}}.handler())

	res, err := p.Snapshot(context.Background(), []string{"snapshot.organization.member"}, nil)
	isErr(t, err)

	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}

	if errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want the 403, not a cancellation", err)
	}

	equal(t, clierr.KindForbidden, clierr.Classify(err))
	contains(t, err.Error(), "Insufficient scope")
}

func TestSnapshotSendsUserAgent(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex

	agents := map[string]bool{}

	p, _ := newProvider(t, tenant{n: 2, before: func(_ http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()

		agents[r.UserAgent()] = true

		return false
	}}.handler())

	_, err := p.Snapshot(context.Background(), tenantTypes, nil)
	noErr(t, err)
	equal(t, map[string]bool{"ofga-cli": true}, agents)
}

// A task that cancels the context and returns nil, with tasks still to start,
// must not make parallel report success.
func TestParallelCanceledContextIsReported(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var started atomic.Int64

	err := parallel(ctx, 100, func(context.Context, int) error {
		started.Add(1)
		cancel()

		return nil
	}, nil)
	equal(t, context.Canceled, err)

	if n := started.Load(); n >= 100 {
		t.Errorf("%d tasks started, want the cancellation to stop the rest", n)
	}
}
