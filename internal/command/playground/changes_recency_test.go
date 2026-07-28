package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/go-openfga/openfga"
)

// changesFeed builds n changes in ascending (oldest-first) time order, each
// with a distinguishing user id ("user:000".."user:0NN") so ordering is
// verifiable after the model reorders/truncates them.
func changesFeed(n int) []openfga.TupleChange {
	changes := make([]openfga.TupleChange, n)
	for i := range changes {
		changes[i] = openfga.TupleChange{
			TupleKey: openfga.TupleKey{
				User:     fmt.Sprintf("user:%03d", i),
				Relation: "viewer",
				Object:   "doc:1",
			},
			Operation: "TUPLE_OPERATION_WRITE",
			Timestamp: time.Unix(int64(i), 0).UTC(),
		}
	}
	return changes
}

// changesFeedServer serves the entire feed in a single page (empty
// continuation token), which is enough to exercise ChangesAll's drain loop
// without needing multi-page pagination logic in the test.
func changesFeedServer(t *testing.T, changes []openfga.TupleChange) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(openfga.ReadChangesResponse{Changes: changes})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- (a)/(d) loadChangesCmd keeps the LAST changesDisplayCap changes and
// renders them newest-first, whether the feed exceeds the cap or not.

func TestLoadChangesCmdOverCapKeepsLastChangesNewestFirst(t *testing.T) {
	feed := changesFeed(250)
	srv := changesFeedServer(t, feed)
	cl, err := openfga.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	msg := loadChangesCmd(context.Background(), cl, "store-1", 1)()
	got, ok := msg.(changesLoadedMsg)
	if !ok {
		t.Fatalf("loadChangesCmd result = %#v, want changesLoadedMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if !got.capped {
		t.Fatal("250 changes over a 200 cap should report capped=true")
	}
	if got.total != 250 {
		t.Fatalf("total = %d, want 250 (the full feed size, known because the feed is fully drained)", got.total)
	}
	if len(got.changes) != changesDisplayCap {
		t.Fatalf("len(changes) = %d, want the %d cap", len(got.changes), changesDisplayCap)
	}
	// The LAST 200 of the 250 are users 050..249. Newest-first means index 0
	// is user:249 (the very last write) and index 199 is user:050 (the oldest
	// surviving entry once the oldest 50 are dropped).
	if got.changes[0].TupleKey.User != "user:249" {
		t.Fatalf("changes[0].User = %q, want %q (newest first)", got.changes[0].TupleKey.User, "user:249")
	}
	if got.changes[199].TupleKey.User != "user:050" {
		t.Fatalf("changes[199].User = %q, want %q (oldest of the retained window)", got.changes[199].TupleKey.User, "user:050")
	}
	// None of the dropped oldest 50 changes (user:000..user:049) should survive.
	for _, ch := range got.changes {
		if ch.TupleKey.User < "user:050" {
			t.Fatalf("found dropped-oldest entry %q still present in the retained window", ch.TupleKey.User)
		}
	}
}

func TestLoadChangesCmdUnderCapKeepsAllNewestFirst(t *testing.T) {
	feed := changesFeed(50)
	srv := changesFeedServer(t, feed)
	cl, err := openfga.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	msg := loadChangesCmd(context.Background(), cl, "store-1", 1)()
	got, ok := msg.(changesLoadedMsg)
	if !ok {
		t.Fatalf("loadChangesCmd result = %#v, want changesLoadedMsg", msg)
	}
	if got.capped {
		t.Fatal("50 changes under a 200 cap should not be capped")
	}
	if got.total != 50 {
		t.Fatalf("total = %d, want 50", got.total)
	}
	if len(got.changes) != 50 {
		t.Fatalf("len(changes) = %d, want all 50", len(got.changes))
	}
	if got.changes[0].TupleKey.User != "user:049" || got.changes[49].TupleKey.User != "user:000" {
		t.Fatalf("changes not newest-first: first=%q last=%q", got.changes[0].TupleKey.User, got.changes[49].TupleKey.User)
	}
}

// --- (b) the status text must be honest: no "first", and it should say
// "latest" alongside the true total once the feed exceeds the display cap.

func TestChangesSectionStatusHonestWhenCapped(t *testing.T) {
	m := Model{section: secChanges}
	m.changes = changesFeed(changesDisplayCap) // 200 retained
	m.changesCapped = true
	m.changesTotal = 250

	got := m.sectionStatus()
	if strings.Contains(got, "first") {
		t.Fatalf("status %q must not claim to show the first changes", got)
	}
	if !strings.Contains(got, "latest") {
		t.Fatalf("status %q should say 'latest' when the pane is capped", got)
	}
	if !strings.Contains(got, "250") {
		t.Fatalf("status %q should mention the true total (250)", got)
	}
}

func TestChangesSectionStatusUncappedStaysPlain(t *testing.T) {
	m := Model{section: secChanges}
	m.changes = changesFeed(5)
	m.changesCapped = false

	got := m.sectionStatus()
	if got != "5 changes" {
		t.Fatalf("uncapped status = %q, want %q", got, "5 changes")
	}
}

// --- (c) a successful tuple write/delete must not leave the Changes pane
// showing stale data — but since ChangesAll has no reverse/tail fetch (it
// always drains the store's ENTIRE lifetime change feed), an eager reload on
// every mutation would turn every tuple write into a potentially expensive
// full-history scan, even when the user never looks at Changes. So the fix is
// lazy invalidation, not an eager reload: clear the stale data and mark it
// stale so onEnterSection's existing lazy load re-fires next time the tab is
// actually opened, going through the same begin/end load + gen accounting as
// every other reload.

func TestTupleWrittenInvalidatesChangesWithoutEagerReload(t *testing.T) {
	var changesCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/changes") {
			changesCalls.Add(1)
		}
		_ = json.NewEncoder(w).Encode(openfga.ReadResponse{})
	}))
	defer srv.Close()
	cl, err := openfga.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	m := newTestModel().(Model)
	m.client = cl
	// Seed stale changes data (list + counters) distinct from an invalidated
	// state, so invalidation is observable.
	m.changes = []openfga.TupleChange{{TupleKey: openfga.TupleKey{User: "user:stale", Relation: "viewer", Object: "doc:1"}}}
	m.changesCapped = true
	m.changesTotal = 250
	staleGenBefore := m.changesGen
	pendingBefore := m.pendingLoads

	nm, cmd := m.Update(tupleWrittenMsg{label: "user:anne#viewer@doc:1"})
	got := nm.(Model)
	if cmd == nil {
		t.Fatal("a successful tuple write should dispatch a command (toast + tuples reload)")
	}
	if got.changes != nil {
		t.Fatalf("changes = %+v, want cleared (invalidated) immediately, not left stale", got.changes)
	}
	if got.changesCapped || got.changesTotal != 0 {
		t.Fatalf("stale capped/total counters must be reset alongside the invalidation: capped=%v total=%d", got.changesCapped, got.changesTotal)
	}
	if !got.changesStale {
		t.Fatal("a successful tuple write should mark changes stale so onEnterSection's lazy load re-fires")
	}
	if got.changesGen == staleGenBefore {
		t.Fatal("changesGen should still be bumped (with no matching load dispatched) to fence off " +
			"any load already in flight from before the write, so it can't land afterward and " +
			"silently overwrite the invalidated state with pre-write data")
	}
	// Exactly one load (tuples) begins here, not two — no beginLoad for a
	// changes reload that never gets dispatched.
	if got.pendingLoads != pendingBefore+1 {
		t.Fatalf("pendingLoads = %d, want exactly %d (tuples reload only, no eager changes load)", got.pendingLoads, pendingBefore+1)
	}
	midGen := got.changesGen

	// Drain the command(s) tupleWrittenMsg dispatched (toast + tuples reload)
	// until the model settles, mirroring pump()'s loop but starting from the
	// already-produced (got, cmd) pair.
	queue := []tea.Msg{}
	collectCmd(cmd, &queue)
	var final tea.Model = got
	for i := 0; len(queue) > 0; i++ {
		if i > 1000 {
			t.Fatal("did not settle")
		}
		msg := queue[0]
		queue = queue[1:]
		var c tea.Cmd
		final, c = final.Update(msg)
		collectCmd(c, &queue)
	}
	fm := final.(Model)
	if fm.pendingLoads != 0 {
		t.Fatalf("pendingLoads = %d after everything settles, want 0", fm.pendingLoads)
	}
	if changesCalls.Load() != 0 {
		t.Fatalf("the /changes endpoint was hit %d times after a tuple write; want 0 — invalidation must not eagerly scan the change feed", changesCalls.Load())
	}

	// Only now, when the user actually opens the Changes tab, must the
	// invalidated data be reloaded.
	fm.section = secChanges
	nm2, cmd2 := fm.onEnterSection()
	entered := nm2.(Model)
	if cmd2 == nil {
		t.Fatal("entering Changes after an invalidating write should dispatch a reload")
	}
	if entered.changesGen == staleGenBefore || entered.changesGen == midGen {
		t.Fatal("entering Changes should bump changesGen again for the deferred reload, distinct from both the pre-write gen and the write's own fencing bump")
	}
	if entered.changesStale {
		t.Fatal("changesStale should be cleared once the deferred reload is dispatched")
	}

	msg := cmd2()
	loaded, ok := msg.(changesLoadedMsg)
	if !ok {
		t.Fatalf("expected a changesLoadedMsg from the deferred reload, got %#v", msg)
	}
	final2, _ := entered.Update(loaded)
	fm2 := final2.(Model)
	// The mock server responds to /changes with an empty ReadResponse{} body
	// (no Changes field set), i.e. zero changes — checking the deferred load
	// actually landed (and cleared changesStale) is enough here; a
	// populated-data variant is already covered by
	// TestLoadChangesCmdOverCapKeepsLastChangesNewestFirst.
	if len(fm2.changes) != 0 {
		t.Fatalf("changes = %+v, want empty (mock server has none)", fm2.changes)
	}
	if fm2.changesStale {
		t.Fatal("changesStale should stay cleared once the deferred reload lands")
	}
	if changesCalls.Load() != 1 {
		t.Fatalf("the /changes endpoint should be hit exactly once, on tab entry; got %d calls", changesCalls.Load())
	}
}
