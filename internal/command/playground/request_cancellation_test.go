package playground

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/log/v2"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/ui/shell"
)

// --- TUI-F10: superseded loads must be cancelled instead of running to
// completion. The generation protocol already drops a stale result once it
// lands; these tests cover the addition on top of it — the in-flight request
// itself stopping the moment it's superseded. ---

// TestSelectStoreCancelsPreviousRequestContext is the direct, deterministic
// check requested in the brief: switching stores must cancel the
// request-scoped context the previous selection's loads were dispatched with.
func TestSelectStoreCancelsPreviousRequestContext(t *testing.T) {
	m := newTestModel().(Model)

	m.selectStore(openfga.Store{ID: "store-a", Name: "A"})
	oldCtx := m.reqCtx
	if oldCtx == nil {
		t.Fatal("selectStore should leave a non-nil request context installed")
	}

	m.selectStore(openfga.Store{ID: "store-b", Name: "B"})

	select {
	case <-oldCtx.Done():
	default:
		t.Fatal("switching stores should cancel the previous selection's request context")
	}
	if !errors.Is(oldCtx.Err(), context.Canceled) {
		t.Fatalf("previous request context Err() = %v, want context.Canceled", oldCtx.Err())
	}
	if m.reqCtx.Err() != nil {
		t.Fatalf("the newly installed request context should not be cancelled, got %v", m.reqCtx.Err())
	}
}

// TestModelSwitchDoesNotCancelStoreScopedLoads pins the scope rule that makes
// cancellation safe: cancel only where the whole batch is superseded *and*
// re-dispatched.
//
// A model switch goes through clearResourcePending, which bumps only the three
// mutation generations — the store-scoped tuples/changes loads in flight are
// still current, and the model-switch paths re-dispatch only the model. So it
// must NOT cancel: doing so kills a live tuples load nothing re-issues, leaving
// the pane permanently empty and (because staleCancel drops cancellations
// silently) with no toast, status or spinner to show for it.
func TestModelSwitchDoesNotCancelStoreScopedLoads(t *testing.T) {
	m := newTestModel().(Model)
	m.modelID = "model-a"
	oldCtx := m.reqCtx

	tm, _ := m.Update(modelLoadedMsg{storeID: m.storeID, gen: m.modelGen, modelID: "model-b", graph: sampleGraph()})
	mm := tm.(Model)

	if err := oldCtx.Err(); err != nil {
		t.Fatalf("a model switch must not cancel store-scoped reads (got %v): "+
			"clearResourcePending doesn't bump tuplesGen/changesGen and nothing re-dispatches them", err)
	}
	if mm.reqCtx != oldCtx {
		t.Fatal("a model switch should leave the request context in place, not renew it")
	}
	// The generation protocol is still what drops the superseded model response.
	if mm.modelID != "model-b" {
		t.Fatalf("model switch should have applied: modelID = %q, want model-b", mm.modelID)
	}
}

// TestModelSwitchKeepsInFlightTupleLoadAlive is the behavioural half of the
// rule above: the load must survive the switch and still populate its pane.
func TestModelSwitchKeepsInFlightTupleLoadAlive(t *testing.T) {
	m := newTestModel().(Model)
	m.modelID = "model-a"
	m.tuples = nil

	// A tuples load is dispatched (as selectStore would) and still in flight
	// when the user switches models.
	inFlightGen := m.tuplesGen
	inFlightCtx := m.reqCtx
	// Two slots: the tuples load in flight, and the model load whose completion
	// is delivered below (its own handler frees that one). Without the second,
	// the model switch would drain the counter to zero and endLoad's clamp would
	// make the accounting assertion at the end hold no matter what.
	m.beginLoad()
	m.beginLoad()

	tm, _ := m.Update(modelLoadedMsg{storeID: m.storeID, gen: m.modelGen, modelID: "model-b", graph: sampleGraph()})
	mm := tm.(Model)

	if err := inFlightCtx.Err(); err != nil {
		t.Fatalf("the in-flight tuples load was cancelled by the model switch: %v", err)
	}
	if mm.pendingLoads == 0 {
		t.Fatal("the in-flight tuples load must still hold a pending slot after the model switch")
	}

	// It lands afterwards and must populate the pane rather than being dropped.
	tm2, _ := mm.Update(tuplesLoadedMsg{storeID: mm.storeID, gen: inFlightGen, tuples: []openfga.Tuple{
		{Key: openfga.TupleKey{User: "user:anne", Relation: "owner", Object: "document:roadmap"}},
	}})
	got := tm2.(Model)

	if len(got.tuples) != 1 {
		t.Fatalf("the tuples load that survived the model switch should have populated the pane, got %d tuples", len(got.tuples))
	}
	if want := mm.pendingLoads - 1; got.pendingLoads != want {
		t.Fatalf("pendingLoads = %d, want %d — the completion must free exactly its own slot", got.pendingLoads, want)
	}
}

// TestSwitchProfileCancelsPreviousRequestContext covers activateResolved,
// which resets connection-wide state without going through
// clearResourcePending.
func TestSwitchProfileCancelsPreviousRequestContext(t *testing.T) {
	// switchProfile persists the active profile — isolate the config so the
	// test never touches the developer's real one.
	configtest.Isolate(t)

	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: "http://server-a.example"})
	cfg.Set("other", config.Profile{APIURL: "http://server-b.example"})
	cl, _ := openfga.NewClient("http://server-a.example")
	a := cli.New(log.New(io.Discard), cfg, "test")
	m := newModel(context.Background(), a, cl, "", "")
	oldCtx := m.reqCtx

	m.switchProfile("other")

	select {
	case <-oldCtx.Done():
	default:
		t.Fatal("switching profiles should cancel the previous connection's request context")
	}
	if m.reqCtx.Err() != nil {
		t.Fatal("the newly installed request context should not be cancelled")
	}
}

// TestReqCtxDerivedFromProgramContext proves the request-scoped context is a
// child of the program-lifetime one, so tearing the program down (Ctrl-C/
// SIGINT cancelling ctx) also cancels reqCtx — no separate shutdown wiring is
// needed, and nothing is left running after the program exits.
func TestReqCtxDerivedFromProgramContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(ctx, a, cl, "", "")

	cancel()

	select {
	case <-m.reqCtx.Done():
	default:
		t.Fatal("cancelling the program-lifetime context should cancel the derived request context")
	}
}

// TestSupersededLoadCancelsInFlightHTTPRequest is the end-to-end proof: a real
// load blocked on a real (mock) server aborts the moment it's superseded,
// instead of running to completion. This is the worst-case scenario the
// finding calls out (expandCmd's chain of Expand/Check calls), reproduced
// here with loadModelCmd for simplicity — the mechanism (context passed
// through to the SDK's http.Client) is identical for every generation-tracked
// read command.
func TestSupersededLoadCancelsInFlightHTTPRequest(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	// Registered after srv.Close() so LIFO releases the parked handler *first*.
	// The other order deadlocks: srv.Close() waits on a handler that is waiting
	// on release, so a failed assertion would hang to the package timeout
	// instead of reporting.
	defer close(release)

	cl, _ := openfga.NewClient(srv.URL)
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "store-a", "")

	cmd := loadModelCmd(m.reqCtx, m.client, m.storeID, m.modelGen)

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the load never reached the mock server")
	}

	// Switching stores supersedes the in-flight load: its request context
	// must be cancelled, aborting the blocked HTTP call immediately instead
	// of waiting for `release`.
	m.selectStore(openfga.Store{ID: "store-b", Name: "B"})

	select {
	case msg := <-done:
		got, ok := msg.(modelLoadedMsg)
		if !ok {
			t.Fatalf("expected modelLoadedMsg, got %T", msg)
		}
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("expected the superseded load's context to be cancelled, got err=%v", got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("superseded load kept running instead of being cancelled")
	}
}

// TestStoreSwitchDoesNotCancelTheStoresRefresh is the inverse of the test
// above, pinning the one read deliberately kept off the request-scoped
// context. The stores list is connection-scoped: selectStore neither bumps
// storesGen nor re-dispatches loadStoresCmd, so cancelling a refresh that is
// still current (the Stores section's "r" key) would strand the list on its
// previous contents with no toast, status or spinner — the same hazard
// clearResourcePending was fixed for, one call frame up.
func TestStoreSwitchDoesNotCancelTheStoresRefresh(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		select {
		case <-release:
			_, _ = w.Write([]byte(`{"stores":[{"id":"store-a","name":"A"}]}`))
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	// Registered after srv.Close() so LIFO releases the parked handler first —
	// see the note in TestSupersededLoadCancelsInFlightHTTPRequest.
	defer releaseOnce()

	cl, _ := openfga.NewClient(srv.URL)
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "store-a", "")

	cmd := loadStoresCmd(m.ctx, m.client, m.storesGen)

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the stores refresh never reached the mock server")
	}

	m.selectStore(openfga.Store{ID: "store-b", Name: "B"})

	// Assert it is still parked *before* releasing: if the switch cancelled it,
	// the handler has already returned and the result lands here. Releasing
	// first would make both select arms ready in the handler and the test flaky.
	select {
	case msg := <-done:
		t.Fatalf("the store switch cut the stores refresh short: %#v", msg)
	case <-time.After(100 * time.Millisecond):
	}

	releaseOnce()
	select {
	case msg := <-done:
		got, ok := msg.(storesLoadedMsg)
		if !ok {
			t.Fatalf("expected storesLoadedMsg, got %T", msg)
		}
		if got.err != nil {
			t.Fatalf("the stores refresh must survive a store switch, got err=%v", got.err)
		}
		if len(got.stores) != 1 {
			t.Fatalf("the surviving stores refresh should carry its page, got %d stores", len(got.stores))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stores refresh never completed")
	}
}

// TestCanceledLoadCompletionStillFreesPendingSlot is the accounting guarantee
// the brief calls out: a completion carrying a cancellation error must still
// free its pendingLoads slot exactly once, same as any other dropped-stale
// completion — never leaving the spinner stranded on.
func TestCanceledLoadCompletionStillFreesPendingSlot(t *testing.T) {
	// staleCancel is applied identically across ten handlers; sample the
	// distinct shapes — a plain load, a store-scoped load, and the query
	// handler, which carries its own extra pending-state bookkeeping.
	tests := []struct {
		name string
		msg  func(Model) tea.Msg
	}{
		{"modelLoadedMsg", func(m Model) tea.Msg {
			return modelLoadedMsg{storeID: m.storeID, gen: m.modelGen, err: context.Canceled}
		}},
		{"tuplesLoadedMsg", func(m Model) tea.Msg {
			return tuplesLoadedMsg{storeID: m.storeID, gen: m.tuplesGen, err: context.Canceled}
		}},
		{"changesLoadedMsg", func(m Model) tea.Msg {
			return changesLoadedMsg{storeID: m.storeID, gen: m.changesGen, err: context.Canceled}
		}},
		{"assertionsLoadedMsg", func(m Model) tea.Msg {
			return assertionsLoadedMsg{storeID: m.storeID, modelID: m.modelID, gen: m.assertLoadGen, err: context.Canceled}
		}},
		{"queryResultMsg", func(m Model) tea.Msg {
			return queryResultMsg{storeID: m.storeID, modelID: m.modelID, gen: m.queryGen, err: context.Canceled}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel().(Model)
			m.pendingLoads = 1
			m.loading = true

			tm, cmd := m.Update(tc.msg(m))
			got := tm.(Model)

			if got.pendingLoads != 0 || got.loading {
				t.Fatalf("a cancelled completion must free its pendingLoads slot exactly once, got pendingLoads=%d loading=%v",
					got.pendingLoads, got.loading)
			}
			if cmd != nil {
				t.Fatal("a cancelled completion must not push an error toast or any other command")
			}
			if got.connLost {
				t.Fatal("a cancelled completion must not be reported as a lost connection")
			}
			if got.status == "connection failed" || strings.Contains(got.status, "failed") {
				t.Fatalf("a cancelled completion must not set a failure status, got %q", got.status)
			}
		})
	}
}

// TestStoreSelectionKeepsUsableRequestContext guards the evaluation-order
// hazard the cancellation wiring introduces (same class as TUI-F8): every
// handler that reaches selectStore mutates the model through a pointer, and
// the renewed reqCtx must survive into the model those handlers return. If a
// mutation were dropped, the live model would keep the *cancelled* context and
// every later request would fail instantly — and, because staleCancel now
// drops cancellations silently, it would do so with no visible error at all.
func TestStoreSelectionKeepsUsableRequestContext(t *testing.T) {
	// The profile switcher needs a config with profiles in it; newTestModel's is
	// empty, which would leave that case selecting nothing at all.
	withProfiles := func() tea.Model {
		cfg := config.New()
		cfg.Set("default", config.Profile{APIURL: "http://server-a.example"})
		cfg.Set("other", config.Profile{APIURL: "http://server-b.example"})
		cl, _ := openfga.NewClient("http://server-a.example")
		a := cli.New(log.New(io.Discard), cfg, "test")
		var m tea.Model = newModel(context.Background(), a, cl, "store-1", "")
		m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
		return m
	}

	tests := []struct {
		name  string
		start func() tea.Model
		run   func(tea.Model) tea.Model
	}{
		{"auto-select on first stores load", newTestModel, func(m tea.Model) tea.Model {
			mm := m.(Model)
			mm.storeID = "" // nothing selected yet — triggers the auto-select path
			out, _ := mm.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-9", Name: "nine"}}})
			return out
		}},
		{"enter on the stores list", newTestModel, func(m tea.Model) tea.Model {
			mm := m.(Model)
			mm.section = secStores
			mm.focus = shell.FocusPanel // section keys only fire with the panel focused
			out, _ := mm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			return out
		}},
		{"store created", newTestModel, func(m tea.Model) tea.Model {
			out, _ := m.Update(storeCreatedMsg{store: openfga.Store{ID: "store-new", Name: "new"}})
			return out
		}},
		{"enter on the profiles list", withProfiles, func(m tea.Model) tea.Model {
			// Reaches switchProfile -> activateResolved, the connection-wide
			// renewal — the highest-consequence site of this hazard.
			mm := m.(Model)
			mm.section = secProfiles
			mm.focus = shell.FocusPanel
			// Move off the active profile first: switchProfile early-returns
			// with "already on profile X" when the pick is the current one.
			down, _ := mm.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			out, _ := down.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			return out
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// selectStore persists the store and switchProfile the profile —
			// isolate the config so neither touches the real one.
			configtest.Isolate(t)

			start := tc.start()
			oldCtx := start.(Model).reqCtx

			got := tc.run(start).(Model)

			// Renewal is unconditional once selectStore/activateResolved is
			// reached, so a changed identity proves the handler got there.
			// Without this the remaining assertions would pass vacuously on a
			// handler that selected nothing (or, for profiles, early-returned
			// on "already on profile X").
			if got.reqCtx == oldCtx {
				t.Fatal("the renewing call never ran: the request context is unchanged")
			}
			if !errors.Is(oldCtx.Err(), context.Canceled) {
				t.Fatalf("the superseded selection's context should be cancelled, got %v", oldCtx.Err())
			}
			if err := got.reqCtx.Err(); err != nil {
				t.Fatalf("returned model holds a cancelled request context (%v) — "+
					"selectStore's renewal was dropped, so every later load would fail silently", err)
			}
		})
	}
}

// TestCanceledResolutionDoesNotSurfaceAsError proves a cancelled request's
// error never renders as a user-facing error: resolutionMsg carrying
// context.Canceled must be dropped exactly like a stale response (no toast,
// no resTree change), not shown as a scary error.
func TestCanceledResolutionDoesNotSurfaceAsError(t *testing.T) {
	m := newTestModel().(Model)
	m.resTree = nil
	m.showRes = false

	tm, cmd := m.Update(resolutionMsg{storeID: m.storeID, modelID: m.modelID, gen: m.resGen, err: context.Canceled})
	got := tm.(Model)

	if got.showRes || got.resTree != nil {
		t.Fatal("a cancelled resolution must not be shown")
	}
	if cmd != nil {
		t.Fatal("a cancelled resolution must not push an error toast")
	}
}
