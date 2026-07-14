package playground

import (
	"context"
	"io"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
)

// TUI-15: running an assertion via Enter must never flash a fabricated
// "✗ DENIED" verdict. Until the real Check result lands (assertOneMsg) no
// verdict may be shown, and once it lands the verdict must reflect the actual
// Check outcome.
func TestAssertionEnterNoFalseDeny(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newTestModel()
	m, _ = m.Update(key("7"))     // jump to Assertions
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("enter")) // run + resolve the selected assertion

	mm := m.(Model)
	if mm.section != secQuery {
		t.Fatalf("running an assertion should switch to the Query panel, got section %d", mm.section)
	}
	if mm.hasResult {
		t.Fatal("no verdict may be shown before the real Check result lands (would be a fabricated DENY)")
	}

	// The real Check comes back allowed; the verdict must now read ALLOWED.
	m, _ = m.Update(assertOneMsg{idx: 0, result: assertResult{ran: true, expected: true, got: true, pass: true}})
	mm = m.(Model)
	if !mm.hasResult {
		t.Fatal("expected a verdict after the assertion Check landed")
	}
	if !mm.result.ok {
		t.Fatal("verdict must reflect the actual (allowed) Check result, not a false DENY")
	}
}

// TUI-25: a load result tagged with a store we've since switched away from must
// be dropped, so a previous store's rows don't overwrite the current view.
func TestStaleStoreLoadIgnored(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newTestModel() // storeID "store-1", one tuple loaded
	if got := len(m.(Model).tuples); got != 1 {
		t.Fatalf("precondition: expected 1 tuple, got %d", got)
	}

	// A late load from a different store must be ignored.
	m, _ = m.Update(tuplesLoadedMsg{storeID: "other-store", tuples: nil})
	if got := len(m.(Model).tuples); got != 1 {
		t.Fatalf("stale store load must be dropped, tuples changed to %d", got)
	}

	// A load for the current store applies normally.
	m, _ = m.Update(tuplesLoadedMsg{storeID: "store-1", tuples: []openfga.Tuple{
		{Key: openfga.TupleKey{User: "user:bob", Relation: "viewer", Object: "document:x"}},
		{Key: openfga.TupleKey{User: "user:cara", Relation: "viewer", Object: "document:y"}},
	}})
	if got := len(m.(Model).tuples); got != 2 {
		t.Fatalf("current-store load should apply, got %d tuples", got)
	}
}

// TUI-27: when the API URL came from a one-shot --api-url override, selecting a
// store must NOT auto-persist its id onto the saved profile (whose URL differs).
func TestNoPersistStoreUnderURLOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	a.Overrides.APIURL = "http://flag-override:9999"
	m := newModel(context.Background(), a, cl, "", "")

	m.selectStore(openfga.Store{ID: "store-flag", Name: "flag"})

	p, _ := a.Config.Get(a.Config.Active)
	if p.StoreID != "" {
		t.Fatalf("store id must not be persisted under a --api-url override, got %q", p.StoreID)
	}
}

// TUI-27 (control): without an override, selecting a store persists its id.
func TestPersistStoreWithoutOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "", "")

	m.selectStore(openfga.Store{ID: "store-saved", Name: "saved"})

	p, _ := a.Config.Get(a.Config.Active)
	if p.StoreID != "store-saved" {
		t.Fatalf("store id should be persisted without an override, got %q", p.StoreID)
	}
}

// CFG-19: reducedMotion honors both the canonical OPENFGA_ and legacy OFGA_ env
// var names.
func TestReducedMotionEnvVars(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	t.Setenv("OFGA_REDUCED_MOTION", "")
	t.Setenv("OPENFGA_REDUCED_MOTION", "")
	if reducedMotion() {
		t.Fatal("reducedMotion should be false with neither env var set")
	}

	t.Setenv("OPENFGA_REDUCED_MOTION", "1")
	if !reducedMotion() {
		t.Fatal("OPENFGA_REDUCED_MOTION should enable reduced motion")
	}

	t.Setenv("OPENFGA_REDUCED_MOTION", "")
	t.Setenv("OFGA_REDUCED_MOTION", "1")
	if !reducedMotion() {
		t.Fatal("legacy OFGA_REDUCED_MOTION should still enable reduced motion")
	}
}
