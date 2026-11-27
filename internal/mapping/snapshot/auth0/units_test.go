package auth0

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// saved is one Save call.
type saved struct {
	unit     snapshot.Unit
	events   []map[string]any
	finished bool
}

// saver is a Save that keeps its calls, and fails the test when it is called
// from two goroutines at once.
type saver struct {
	t      *testing.T
	mu     sync.Mutex
	inside bool
	calls  []saved
	onSave func(n int, c saved) error
}

func (s *saver) save(u snapshot.Unit, events []map[string]any, finished bool) error {
	s.mu.Lock()

	if s.inside {
		s.t.Error("Save called while another call was running")
	}

	s.inside = true
	c := saved{unit: u, events: events, finished: finished}
	s.calls = append(s.calls, c)
	n := len(s.calls)
	s.mu.Unlock()

	var err error
	if s.onSave != nil {
		err = s.onSave(n, c)
	}

	s.mu.Lock()
	s.inside = false
	s.mu.Unlock()

	return err
}

func (s *saver) snapshot() []saved {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.calls)
}

func typeIndex(types []string, e map[string]any) int {
	typ, _ := e["type"].(string)

	return slices.Index(types, typ)
}

func renumber(events []map[string]any) []map[string]any {
	out := withoutTimes(events)
	for i, e := range out {
		e["id"] = fmt.Sprintf("snap_%06d", i+1)
	}

	return out
}

func TestSaveParityWithResultEvents(t *testing.T) {
	t.Parallel()

	plain, _ := newProvider(t, emitTenant(3, nil))
	want, err := plain.Snapshot(context.Background(), snapshot.Request{Types: allTypes}, nil)
	noErr(t, err)
	equal(t, 3*5+3, len(want.Events))

	p, _ := newProvider(t, emitTenant(3, nil))
	s := &saver{t: t}
	got, err := p.Snapshot(context.Background(), snapshot.Request{Types: allTypes, Save: s.save}, nil)
	noErr(t, err)

	if got.Events != nil {
		t.Errorf("Result.Events has %d events, want nil", len(got.Events))
	}

	equal(t, want.Counts, got.Counts)

	type arrived struct {
		e       map[string]any
		unit    snapshot.Unit
		arrival int
	}

	var all []arrived

	finished := map[string]int{}

	n := 0

	for _, c := range s.snapshot() {
		if c.finished {
			finished[c.unit.Key]++

			if len(c.events) != 0 {
				t.Errorf("finished call for %s carries %d events", c.unit.Key, len(c.events))
			}
		}

		for _, e := range c.events {
			all = append(all, arrived{e, c.unit, n})
			n++
		}
	}

	slices.SortFunc(all, func(a, b arrived) int {
		if d := typeIndex(allTypes, a.e) - typeIndex(allTypes, b.e); d != 0 {
			return d
		}

		if d := a.unit.Order - b.unit.Order; d != 0 {
			return d
		}

		if d := strings.Compare(a.unit.Key, b.unit.Key); d != 0 {
			return d
		}

		return a.arrival - b.arrival
	})

	events := make([]map[string]any, len(all))
	for i, a := range all {
		events[i] = a.e
	}

	equal(t, renumber(want.Events), renumber(events))

	wantUnits := map[string]int{"connections": 1, "users": 1, "organizations": 1}
	for i := range 3 {
		wantUnits[fmt.Sprintf("org-connections/org_%02d", i)] = 1
		wantUnits[fmt.Sprintf("org-members/org_%02d", i)] = 1
	}

	equal(t, wantUnits, finished)
}

func TestDoneUnitsAreNotRead(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, emitTenant(3, nil))
	s := &saver{t: t}
	skipped := map[string]bool{"connections": true, "org-members/org_01": true}

	var (
		mu       sync.Mutex
		progress []snapshot.Progress
	)

	_, err := p.Snapshot(context.Background(), snapshot.Request{
		Types: allTypes, Save: s.save, Done: func(key string) bool { return skipped[key] },
	}, func(pr snapshot.Progress) {
		mu.Lock()
		defer mu.Unlock()

		progress = append(progress, pr)
	})
	noErr(t, err)

	equal(t, 0, len(rec.find("/connections")))
	equal(t, 0, len(rec.find("/organizations/org_01/members")))
	equal(t, 1, len(rec.find("/organizations/org_00/members")))
	equal(t, 1, len(rec.find("/organizations/org_02/members")))

	for _, c := range s.snapshot() {
		if skipped[c.unit.Key] {
			t.Errorf("Save was called for the done unit %s", c.unit.Key)
		}
	}

	last := 0

	for _, pr := range progress {
		if pr.Step == "members of organizations" {
			last = pr.Done
			equal(t, 3, pr.Total)
		}
	}

	equal(t, 3, last)
}

func TestDoneOrganizationsAreListedButNotSaved(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, emitTenant(2, nil))
	s := &saver{t: t}

	_, err := p.Snapshot(context.Background(), snapshot.Request{
		Types: allTypes, Save: s.save, Done: func(key string) bool { return key == "organizations" },
	}, nil)
	noErr(t, err)

	notEmpty(t, rec.find("/organizations"))

	for _, c := range s.snapshot() {
		if c.unit.Key == "organizations" {
			t.Errorf("Save was called for the done unit organizations")
		}
	}
}

func TestSaveErrorStopsTheRead(t *testing.T) {
	t.Parallel()

	boom := errors.New("disk full")
	p, _ := newProvider(t, emitTenant(3, nil))
	s := &saver{t: t, onSave: func(n int, _ saved) error {
		if n == 3 { // the second unit's events, after connections' events and its finish
			return boom
		}

		return nil
	}}

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: allTypes, Save: s.save}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
}

func TestRolesAreNotHeldUntilAllMembers(t *testing.T) {
	t.Parallel()

	rolesSaved := make(chan struct{})

	var once sync.Once

	timedOut := make(chan struct{}, 1)

	h := emitTenant(2, func(i int) {
		if i != 1 {
			return
		}

		select {
		case <-rolesSaved:
		case <-time.After(5 * time.Second):
			timedOut <- struct{}{}
		}
	})

	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		h(w, r)
	})
	s := &saver{t: t, onSave: func(_ int, c saved) error {
		for _, e := range c.events {
			if e["type"] == typeOrgMemberRole && c.unit.Key == "org-members/org_00" {
				once.Do(func() { close(rolesSaved) })
			}
		}

		return nil
	}}

	_, err := p.Snapshot(context.Background(), snapshot.Request{
		Types: []string{typeOrganization, typeOrgMember, typeOrgMemberRole}, Save: s.save,
	}, nil)
	noErr(t, err)

	select {
	case <-timedOut:
		t.Fatal("the first organization's roles were held until the second organization's members finished")
	default:
	}
}

func TestSourceIsTheDomain(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, emitTenant(1, nil))

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{typeConnection}}, nil)
	noErr(t, err)
	equal(t, res.Source, p.Source())
}
