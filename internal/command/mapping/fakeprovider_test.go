package mapping

import (
	"context"
	"github.com/spf13/cobra"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// fakeProvider answers Snapshot from a canned result. calls counts the reads
// and gotTypes records the types asked for; both may be nil.
type fakeProvider struct {
	types    []snapshot.Type
	result   *snapshot.Result
	err      error
	calls    *int
	gotTypes *[]string
}

func (fakeProvider) Name() string             { return "fake" }
func (fakeProvider) Label() string            { return "Fake" }
func (p fakeProvider) Types() []snapshot.Type { return p.types }

func (p fakeProvider) Snapshot(_ context.Context, types []string, progress func(string)) (*snapshot.Result, error) {
	if p.calls != nil {
		*p.calls++
	}
	if p.gotTypes != nil {
		*p.gotTypes = types
	}
	if progress != nil {
		progress("reading fake")
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
