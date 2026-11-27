package mapping

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"testing"
	"time"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// fakeProvider answers Snapshot from a canned result. calls counts the reads
// and gotTypes and gotSince record what was asked for; both may be nil.
type fakeProvider struct {
	types    []snapshot.Type
	result   *snapshot.Result
	err      error
	calls    *int
	gotTypes *[]string
	gotSince *time.Time
	// log, when non-nil, gets "provider read" for each Snapshot.
	log *[]string
	// stream, when set, hands the result's events to Request.Save one at a time
	// instead of returning them, and records "emit <type>", then "read finished"
	// or "stopped" in steps (which may be nil).
	stream bool
	steps  *[]string
	// hook, when set, runs first in Snapshot; an error from it ends the read.
	hook func(req snapshot.Request) error
	// units, when set, hands the events to Request.Save one unit at a time,
	// skipping the units Request.Done reports finished. readUnits (may be nil)
	// gets the key of each unit actually read. failAfter, when > 0, ends the
	// read with an error once that many units were saved.
	units     []fakeUnit
	failAfter int
	readUnits *[]string
	// source, when set, is what Source reports.
	source string
}

// fakeUnit is one unit of work of a fakeProvider: its key and its events.
type fakeUnit struct {
	key    string
	events []map[string]any
}

func (fakeProvider) Name() string  { return "fake" }
func (fakeProvider) Label() string { return "Fake" }
func (p fakeProvider) Source() string {
	if p.source != "" {
		return p.source
	}
	return "fake.example"
}
func (p fakeProvider) Types() []snapshot.Type { return p.types }

func (fakeProvider) Stats() snapshot.Stats { return snapshot.Stats{} }

func (p fakeProvider) Snapshot(_ context.Context, req snapshot.Request, progress func(snapshot.Progress)) (*snapshot.Result, error) {
	if p.calls != nil {
		*p.calls++
	}
	if p.log != nil {
		*p.log = append(*p.log, "provider read")
	}
	if p.gotTypes != nil {
		*p.gotTypes = req.Types
	}
	if p.gotSince != nil {
		*p.gotSince = req.Since
	}
	if progress != nil {
		progress(snapshot.Progress{Message: "reading fake"})
	}
	if p.hook != nil {
		if err := p.hook(req); err != nil {
			return nil, err
		}
	}
	if p.units != nil && req.Save != nil && p.err == nil {
		saved := 0
		for i, u := range p.units {
			if progress != nil {
				progress(snapshot.Progress{Step: "units", Done: i + 1, Total: len(p.units)})
			}
			if req.Done != nil && req.Done(u.key) {
				continue
			}
			if p.readUnits != nil {
				*p.readUnits = append(*p.readUnits, u.key)
			}
			if err := req.Save(snapshot.Unit{Key: u.key, Order: i}, u.events, true); err != nil {
				return nil, err
			}
			saved++
			if p.failAfter > 0 && saved == p.failAfter {
				return nil, fmt.Errorf("fake provider failed after %d units", saved)
			}
		}
		res := *p.result
		res.Events = nil
		return &res, nil
	}
	if p.stream && req.Save != nil && p.err == nil {
		note := func(s string) {
			if p.steps != nil {
				*p.steps = append(*p.steps, s)
			}
		}
		for i, e := range p.result.Events {
			note(fmt.Sprintf("emit %v", e["type"]))
			if err := req.Save(snapshot.Unit{Key: "all"}, []map[string]any{e}, i == len(p.result.Events)-1); err != nil {
				note("stopped")
				return nil, err
			}
		}
		note("read finished")
		res := *p.result
		res.Events = nil
		return &res, nil
	}
	return p.result, p.err
}

// useFakeProvider registers p as "fake" until the test ends. The registry is
// shared, so a test that calls it must not run in parallel.
func useFakeProvider(t *testing.T, p fakeProvider) {
	t.Helper()
	providers["fake"] = providerEntry{
		Label: "Fake",
		Types: func() []snapshot.Type { return p.types },
		New:   func(*cobra.Command) (snapshot.Provider, error) { return p, nil },
	}
	t.Cleanup(func() { delete(providers, "fake") })
}
