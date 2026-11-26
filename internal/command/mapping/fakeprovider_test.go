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
	// stream, when set, hands the result's events to Request.Emit one at a time
	// instead of returning them, and records "emit <type>", then "read finished"
	// or "stopped" in steps (which may be nil).
	stream bool
	steps  *[]string
}

func (fakeProvider) Name() string             { return "fake" }
func (fakeProvider) Label() string            { return "Fake" }
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
	if p.stream && req.Emit != nil && p.err == nil {
		note := func(s string) {
			if p.steps != nil {
				*p.steps = append(*p.steps, s)
			}
		}
		for _, e := range p.result.Events {
			note(fmt.Sprintf("emit %v", e["type"]))
			if err := req.Emit([]map[string]any{e}); err != nil {
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
