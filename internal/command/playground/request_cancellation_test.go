package playground

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/log/v2"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
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

// TestModelSwitchCancelsPreviousRequestContext covers clearResourcePending's
// other call site: a fresh model load landing with a different model id than
// the one currently active (see update.go's modelLoadedMsg handler).
func TestModelSwitchCancelsPreviousRequestContext(t *testing.T) {
	m := newTestModel().(Model)
	m.modelID = "model-a"
	oldCtx := m.reqCtx

	tm, _ := m.Update(modelLoadedMsg{storeID: m.storeID, gen: m.modelGen, modelID: "model-b", graph: sampleGraph()})
	mm := tm.(Model)

	select {
	case <-oldCtx.Done():
	default:
		t.Fatal("a model switch should cancel the previous model's request context")
	}
	if mm.reqCtx.Err() != nil {
		t.Fatal("the newly installed request context should not be cancelled")
	}
}

// TestSwitchProfileCancelsPreviousRequestContext covers activateResolved,
// which resets connection-wide state without going through
// clearResourcePending.
func TestSwitchProfileCancelsPreviousRequestContext(t *testing.T) {
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
	defer close(release)
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

// TestCanceledLoadCompletionStillFreesPendingSlot is the accounting guarantee
// the brief calls out: a completion carrying a cancellation error must still
// free its pendingLoads slot exactly once, same as any other dropped-stale
// completion — never leaving the spinner stranded on.
func TestCanceledLoadCompletionStillFreesPendingSlot(t *testing.T) {
	m := newTestModel().(Model)
	m.pendingLoads = 1
	m.loading = true

	tm, cmd := m.Update(modelLoadedMsg{storeID: m.storeID, gen: m.modelGen, err: context.Canceled})
	got := tm.(Model)

	if got.pendingLoads != 0 || got.loading {
		t.Fatalf("a cancelled completion must free its pendingLoads slot, got pendingLoads=%d loading=%v", got.pendingLoads, got.loading)
	}
	if cmd != nil {
		t.Fatal("a cancelled completion must not push an error toast or any other command")
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
	tests := []struct {
		name string
		run  func(tea.Model) tea.Model
	}{
		{"auto-select on first stores load", func(m tea.Model) tea.Model {
			mm := m.(Model)
			mm.storeID = "" // nothing selected yet — triggers the auto-select path
			out, _ := mm.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-9", Name: "nine"}}})
			return out
		}},
		{"enter on the stores list", func(m tea.Model) tea.Model {
			mm := m.(Model)
			mm.section = secStores
			mm.focus = shell.FocusPanel // section keys only fire with the panel focused
			out, _ := mm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			return out
		}},
		{"store created", func(m tea.Model) tea.Model {
			out, _ := m.Update(storeCreatedMsg{store: openfga.Store{ID: "store-new", Name: "new"}})
			return out
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start := newTestModel()
			oldCtx := start.(Model).reqCtx

			got := tc.run(start).(Model)

			// selectStore has no early return, so reaching it always renews the
			// context. Identity therefore proves the call happened at all —
			// without this the remaining assertions could pass vacuously on a
			// handler that never selected a store.
			if got.reqCtx == oldCtx {
				t.Fatal("selectStore never ran: the request context was not renewed")
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
