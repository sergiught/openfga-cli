package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// --- (c) a successful tuple write/delete must refresh (not leave stale) the
// Changes pane, going through the same begin/end load + gen accounting as
// every other reload.

func TestTupleWrittenRefreshesChangesPane(t *testing.T) {
	newChanges := changesFeed(3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(openfga.ReadChangesResponse{Changes: newChanges})
	}))
	defer srv.Close()
	cl, err := openfga.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	m := newTestModel().(Model)
	m.client = cl
	// Seed a stale changes list distinct from what the mock server will
	// return, so a successful refresh is observable.
	m.changes = []openfga.TupleChange{{TupleKey: openfga.TupleKey{User: "user:stale", Relation: "viewer", Object: "doc:1"}}}
	staleGenBefore := m.changesGen
	pendingBefore := m.pendingLoads

	nm, cmd := m.Update(tupleWrittenMsg{label: "user:anne#viewer@doc:1"})
	got := nm.(Model)
	if cmd == nil {
		t.Fatal("a successful tuple write should dispatch a command (toast + reloads)")
	}
	if got.changesGen == staleGenBefore {
		t.Fatal("a successful tuple write should bump changesGen so a stale in-flight response can't win")
	}
	if got.pendingLoads <= pendingBefore {
		t.Fatal("the changes reload must go through beginLoad() like every other dispatched load")
	}

	// Drain the command(s) tupleWrittenMsg dispatched (toast + tuples/changes
	// reloads) until the model settles, mirroring pump()'s loop but starting
	// from the already-produced (got, cmd) pair.
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
	foundRefreshed := false
	for _, ch := range fm.changes {
		if ch.TupleKey.User == "user:002" { // newest of newChanges (index 2, the last one)
			foundRefreshed = true
		}
		if ch.TupleKey.User == "user:stale" {
			t.Fatal("the stale seeded change must not survive a successful write's refresh")
		}
	}
	if !foundRefreshed {
		t.Fatalf("expected the reloaded changes to include the mock server's data; got %+v", fm.changes)
	}
}
