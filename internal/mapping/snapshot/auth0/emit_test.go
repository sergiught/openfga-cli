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

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

var allTypes = []string{
	"snapshot.connection", "snapshot.user", "snapshot.organization", "snapshot.organization.connection",
	"snapshot.organization.member", "snapshot.organization.member.role",
}

// emitTenant is a tenant of n organizations with a connection, three users and
// a member holding one role each. members, when set, runs before an
// organization's member list is answered.
func emitTenant(n int, members func(i int)) http.HandlerFunc {
	base := tenant{n: n}.handler()

	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v2")

		var id int

		switch {
		case path == "/users":
			usersHandler(3)(w, r)
		case scan(path, "/organizations/org_%02d/members", &id):
			if members != nil {
				members(id)
			}

			writeJSON(w, fmt.Sprintf(`{"members":[{"user_id":"auth0|u%02d","email":"u@x.io","name":"U","picture":"p",`+
				`"roles":[{"id":"rol_admin","name":"admin"}]}]}`, id))
		default:
			base(w, r)
		}
	}
}

// collector is an Emit that keeps what it is given, and fails the test when it
// is called from two goroutines at once.
type collector struct {
	t      *testing.T
	inside atomic.Bool
	n      int // deliberately not atomic: -race catches a second goroutine

	mu     sync.Mutex
	events []map[string]any
}

func (c *collector) emit(events []map[string]any) error {
	if !c.inside.CompareAndSwap(false, true) {
		c.t.Error("Emit called while another call was running")
	}

	defer c.inside.Store(false)

	c.n++

	time.Sleep(time.Millisecond)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.events = append(c.events, events...)

	return nil
}

func (c *collector) snapshot() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]map[string]any(nil), c.events...)
}

func TestEmitGivesTheEventsResultEventsWouldHold(t *testing.T) {
	t.Parallel()

	plain, _ := newProvider(t, emitTenant(25, nil))
	want, err := plain.Snapshot(context.Background(), snapshot.Request{Types: allTypes}, nil)
	noErr(t, err)

	p, _ := newProvider(t, emitTenant(25, nil))
	c := &collector{t: t}
	got, err := p.Snapshot(context.Background(), snapshot.Request{Types: allTypes, Emit: c.emit}, nil)
	noErr(t, err)

	if got.Events != nil {
		t.Errorf("Result.Events has %d events, want nil", len(got.Events))
	}

	equal(t, 25*5+3, len(want.Events))
	equal(t, withoutTimes(want.Events), withoutTimes(c.snapshot()))
	equal(t, want.Counts, got.Counts)
}

func TestEmitKeepsOrganizationOrderAndEmitsPrefixes(t *testing.T) {
	t.Parallel()

	gates := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}

	p, _ := newProvider(t, emitTenant(3, func(i int) { <-gates[i] }))
	c := &collector{t: t}

	types := []string{"snapshot.organization", "snapshot.organization.member", "snapshot.organization.member.role"}
	done := make(chan error, 1)

	go func() {
		_, err := p.Snapshot(context.Background(), snapshot.Request{Types: types, Emit: c.emit}, nil)
		done <- err
	}()

	members := func() []string {
		var out []string

		for _, s := range subjects(t, c.snapshot()) {
			if strings.Contains(s, " member ") && !strings.Contains(s, " role ") {
				out = append(out, s)
			}
		}

		return out
	}
	waitFor := func(want []string) {
		t.Helper()

		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if len(members()) >= len(want) {
				break
			}
		}

		equal(t, want, members())
	}

	// The third organization finishes first and is held back.
	close(gates[2])
	time.Sleep(150 * time.Millisecond)
	equal(t, []string(nil), members())

	// The first one is emitted while the second is still blocked.
	close(gates[0])
	waitFor([]string{"organization org_00 member auth0|u00"})

	close(gates[1])
	waitFor([]string{
		"organization org_00 member auth0|u00", "organization org_01 member auth0|u01", "organization org_02 member auth0|u02",
	})

	select {
	case err := <-done:
		noErr(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Snapshot did not return")
	}

	// Roles come after every member, in organization order.
	all := subjects(t, c.snapshot())
	equal(t, []string{
		"organization org_00", "organization org_01", "organization org_02",
		"organization org_00 member auth0|u00", "organization org_01 member auth0|u01", "organization org_02 member auth0|u02",
		"organization org_00 member auth0|u00 role rol_admin", "organization org_01 member auth0|u01 role rol_admin",
		"organization org_02 member auth0|u02 role rol_admin",
	}, all)
}

func TestEmitErrorEndsTheReadUnchanged(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, emitTenant(3, nil))
	boom := errors.New("rule failed on the first user")

	_, err := p.Snapshot(context.Background(), snapshot.Request{
		Types: allTypes,
		Emit: func(events []map[string]any) error {
			for _, e := range events {
				if e["type"] == "snapshot.user" {
					return boom
				}
			}

			return nil
		},
	}, nil)
	if err != boom { //nolint:errorlint // the error must come back as it is
		t.Fatalf("err = %v, want the Emit error itself", err)
	}

	equal(t, 0, len(rec.find("/organizations")))
}

func TestEmitErrorInAnOrganizationReadStopsTheRead(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, emitTenant(30, nil))
	boom := errors.New("rule failed on a member")

	_, err := p.Snapshot(context.Background(), snapshot.Request{
		Types: allTypes,
		Emit: func(events []map[string]any) error {
			for _, e := range events {
				if e["type"] == "snapshot.organization.member" {
					return boom
				}
			}

			return nil
		},
	}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the Emit error", err)
	}
}

func TestEmitFallsBackToTheExportOnce(t *testing.T) {
	t.Parallel()

	var lines []string
	for i := range 1500 {
		lines = append(lines, exportUserLine(i))
	}

	// The first page of the list looks small, the second reports the limit: the
	// users of the first page are discarded for the export's.
	f := &exportFake{file: gzipLines(lines...), users: func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			usersPage(w, r, 1500, func(int) []int { return []int{5000, 5001} })

			return
		}

		writeJSON(w, `{"start":0,"limit":100,"total":500,"users":[{"user_id":"auth0|9000"},{"user_id":"auth0|9001"}]}`)
	}}
	p, _ := newExportProvider(t, 1500, f)
	c := &collector{t: t}

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}, Emit: c.emit}, nil)
	noErr(t, err)
	equal(t, 0, len(res.Events))
	equal(t, []snapshot.Count{{Type: "snapshot.user", N: 1500}}, res.Counts)

	got := subjects(t, c.snapshot())
	equal(t, 1500, len(got))
	equal(t, "user auth0|0000", got[0])
	equal(t, "user auth0|1499", got[1499])

	seen := map[string]bool{}

	for _, s := range got {
		if seen[s] {
			t.Errorf("%s emitted twice", s)
		}

		seen[s] = true
	}

	for _, e := range c.snapshot() {
		if e["id"] == "" {
			t.Error("event without id")
		}
	}
}
