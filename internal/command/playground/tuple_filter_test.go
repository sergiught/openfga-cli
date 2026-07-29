package playground

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/log/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/ui/shell"
)

// The tuples pane's /read request carries a tuple_key filter only when a
// filter is active — same shape the CLI's `tuples read` sends.
func TestTupleReadRequestNoFilter(t *testing.T) {
	req := tupleReadRequest(tupleFilter{})
	if req.TupleKey != nil {
		t.Fatalf("no filter should mean no tuple_key, got %+v", req.TupleKey)
	}
	if req.PageSize != tuplesPageSize {
		t.Fatalf("page size = %d, want %d", req.PageSize, tuplesPageSize)
	}
}

func TestTupleReadRequestWithFilter(t *testing.T) {
	req := tupleReadRequest(tupleFilter{user: "user:anne", object: "document:"})
	if req.TupleKey == nil {
		t.Fatal("active filter should set tuple_key")
	}
	if req.TupleKey.User != "user:anne" || req.TupleKey.Relation != "" || req.TupleKey.Object != "document:" {
		t.Fatalf("tuple_key = %+v, want user:anne / \"\" / document:", req.TupleKey)
	}
}

// vFilterObject allows type:id AND bare-type "type:" (unlike vObject), stays
// lenient on empty, and rejects colon-less or userset-shaped values.
func TestVFilterObject(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"", true},
		{"document:roadmap", true},
		{"document:", true},
		{"  document:  ", true},
		{"document", false},
		{"document:1#viewer", false},
		{":roadmap", false},
		// /read matches object ids literally, so a wildcard would silently
		// return nothing rather than "every document".
		{"document:*", false},
	} {
		err := vFilterObject(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("vFilterObject(%q) = %v, want ok=%v", tc.in, err, tc.ok)
		}
	}
}

// validateTupleFilter mirrors the server's /read rule: object type required,
// and object id and user not both empty. All-empty is valid (it clears).
func TestValidateTupleFilter(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    tupleFilter
		ok   bool
	}{
		{"empty clears", tupleFilter{}, true},
		{"object type:id", tupleFilter{object: "document:roadmap"}, true},
		{"bare type + user", tupleFilter{object: "document:", user: "user:anne"}, true},
		{"type:id + relation", tupleFilter{object: "document:roadmap", relation: "viewer"}, true},
		{"bare type alone", tupleFilter{object: "document:"}, false},
		{"user alone", tupleFilter{user: "user:anne"}, false},
		{"relation alone", tupleFilter{relation: "viewer"}, false},
		{"user + relation, no object", tupleFilter{user: "user:anne", relation: "viewer"}, false},
		// The server splits the object on its first colon, so a colon-less or
		// leading-colon object has no type and is rejected even with a user set.
		{"colon-less object with user", tupleFilter{object: "document", user: "user:anne"}, false},
		{"leading colon with user", tupleFilter{object: ":roadmap", user: "user:anne"}, false},
	} {
		err := validateTupleFilter(tc.f)
		if (err == nil) != tc.ok {
			t.Errorf("%s: validateTupleFilter(%+v) = %v, want ok=%v", tc.name, tc.f, err, tc.ok)
		}
	}
}

func ctrlS() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl} }

// applyFind types a "/" find and applies it. It goes through pump because
// bubbles computes the matches in a command: a test that drops commands leaves
// the list unfiltered, and any assertion about hidden rows passes for free.
func applyFind(t *testing.T, m tea.Model, term string) tea.Model {
	t.Helper()
	msgs := []tea.Msg{key("/")}
	for _, r := range term {
		msgs = append(msgs, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return pump(t, pump(t, m, msgs...), key("enter"))
}

// landTuples delivers the server's answer to the load the model just
// dispatched, addressed to the current store and generation so it isn't
// dropped as stale. Adopting the filter happens here, not at submit.
func landTuples(t *testing.T, m tea.Model, msg tuplesLoadedMsg) (Model, tea.Cmd) {
	t.Helper()
	mm := m.(Model)
	msg.storeID, msg.gen = mm.storeID, mm.tuplesGen
	nm, cmd := m.Update(msg)
	return nm.(Model), cmd
}

// tuplesPanelModel returns a ready model sitting in the Tuples section with
// the panel focused, which is what section keys require.
func tuplesPanelModel() Model {
	m := newTestModel().(Model)
	m.section = secTuples
	m.focus = shell.FocusPanel
	return m
}

// openTupleFilter drives a ready model to the Tuples section and opens the
// filter form with the f key.
func openTupleFilter(t *testing.T) tea.Model {
	t.Helper()
	m, _ := tea.Model(tuplesPanelModel()).Update(key("f"))
	if m.(Model).formKind != formTupleFilter {
		t.Fatalf("f should open the tuple filter form, got kind=%d", m.(Model).formKind)
	}
	return m
}

func TestTupleFilterKeyRequiresStore(t *testing.T) {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	mdl := newModel(context.Background(), a, cl, "", "")
	var m tea.Model = mdl
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	mm := m.(Model)
	mm.section = secTuples
	mm.focus = shell.FocusPanel
	m, _ = tea.Model(mm).Update(key("f"))
	if m.(Model).formKind != formNone {
		t.Fatal("f without a store must not open the filter form")
	}
	if !strings.Contains(m.(Model).status, "select a store") {
		t.Fatalf("status = %q, want the select-a-store hint", m.(Model).status)
	}
}

func TestTupleFilterSubmitAppliesAndReloads(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	genBefore := mm.tuplesGen
	mm.form.SetValues([]string{"user:anne", "", "document:"})
	m, cmd := tea.Model(mm).Update(ctrlS())
	mm = m.(Model)
	if mm.formKind != formNone {
		t.Fatalf("valid submit should close the form, kind=%d err=%q", mm.formKind, mm.formErr)
	}
	// The filter is not adopted at submit: the header must not claim a filter
	// the server has not yet accepted.
	if mm.tupleFilters.applied.active() {
		t.Fatalf("submit must not adopt the filter before the load lands, got %+v", mm.tupleFilters.applied)
	}
	if mm.tuplesGen != genBefore+1 {
		t.Fatalf("submit should bump tuplesGen for the reload, got %d want %d", mm.tuplesGen, genBefore+1)
	}
	if cmd == nil {
		t.Fatal("submit should dispatch a tuples reload command")
	}
	want := tupleFilter{user: "user:anne", object: "document:"}
	mm, _ = landTuples(t, m, tuplesLoadedMsg{filter: want})
	if mm.tupleFilters.applied != want {
		t.Fatalf("tupleFilter = %+v, want %+v", mm.tupleFilters.applied, want)
	}
}

// A load the server rejects must leave the previous rows AND the header
// describing them alone — otherwise the pane claims filtered results while
// showing the old, unfiltered ones.
func TestTupleFilterRejectedLoadKeepsHeaderHonest(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.tuples = []openfga.Tuple{{Key: openfga.TupleKey{User: "user:anne", Relation: "viewer", Object: "document:roadmap"}}}
	mm.form.SetValues([]string{"user:anne", "", "document:"})
	m, _ = tea.Model(mm).Update(ctrlS())
	mm, _ = landTuples(t, m, tuplesLoadedMsg{err: errors.New("400 invalid tuple_key")})
	if mm.tupleFilters.applied.active() {
		t.Fatalf("a rejected filter must not be adopted, got %+v", mm.tupleFilters.applied)
	}
	if len(mm.tuples) != 1 {
		t.Fatalf("a failed load must not drop the rows on screen, got %d", len(mm.tuples))
	}
}

// Applying is announced too: the header breadcrumb is the only other sign, and
// it truncates on a narrow pane.
func TestTupleFilterApplyAnnouncesItself(t *testing.T) {
	mm := tuplesPanelModel()
	f := tupleFilter{object: "document:roadmap"}
	got, cmd := landTuples(t, tea.Model(mm), tuplesLoadedMsg{filter: f})
	if !strings.Contains(got.status, "object=document:roadmap") {
		t.Fatalf("status = %q, want the applied filter named", got.status)
	}
	if cmd == nil {
		t.Fatal("applying should raise a visible toast")
	}
	// Re-reading the same filter is not news.
	got.status = ""
	got2, _ := landTuples(t, tea.Model(got), tuplesLoadedMsg{filter: f})
	if strings.Contains(got2.status, "filtering on") {
		t.Fatalf("an unchanged filter should not re-announce, got %q", got2.status)
	}
}

// Clearing has no other visible confirmation once the rows come back, so it
// must say so — and only after the server has answered.
func TestTupleFilterClearAnnouncesItself(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.tupleFilters.applied = tupleFilter{object: "document:roadmap"}
	mm.form.SetValues([]string{"", "", ""})
	m, _ = tea.Model(mm).Update(ctrlS())
	if m.(Model).tupleFilters.applied.active() != true {
		t.Fatal("the filter should still describe the rows on screen until the reload lands")
	}
	mm, cmd := landTuples(t, m, tuplesLoadedMsg{})
	if mm.tupleFilters.applied.active() {
		t.Fatalf("the landed unfiltered load should clear the filter, got %+v", mm.tupleFilters.applied)
	}
	if !strings.Contains(mm.status, "cleared the filter") {
		t.Fatalf("status = %q, want the cleared-filter confirmation", mm.status)
	}
	if cmd == nil {
		t.Fatal("clearing should raise a visible toast, not just set m.status")
	}
}

// Submitted values are trimmed, so a stray space can't produce a filter that
// looks inactive in the header but still narrows the read.
func TestTupleFilterFormValuesAreTrimmed(t *testing.T) {
	got := tupleFilterFromForm([]string{"  user:anne  ", "  ", " document:roadmap "})
	if want := (tupleFilter{user: "user:anne", object: "document:roadmap"}); got != want {
		t.Fatalf("tupleFilterFromForm = %+v, want %+v", got, want)
	}
	if tupleFilterFromForm([]string{" ", "", "  "}).active() {
		t.Fatal("all-whitespace values must read as no filter, not an invisible one")
	}
}

func TestTupleFilterInvalidSubmitResumes(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.form.SetValues([]string{"user:anne", "", ""}) // user alone: server would reject
	m, _ = tea.Model(mm).Update(ctrlS())
	mm = m.(Model)
	if mm.formKind != formTupleFilter || mm.formErr == "" {
		t.Fatalf("invalid combination should resume the form with an error, kind=%d err=%q", mm.formKind, mm.formErr)
	}
	if mm.tupleFilters.applied.active() {
		t.Fatalf("invalid submit must not set the filter, got %+v", mm.tupleFilters.applied)
	}
}

func TestTupleFilterEmptySubmitClears(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.tupleFilters.applied = tupleFilter{object: "document:roadmap"}
	mm.form.SetValues([]string{"", "", ""})
	m, cmd := tea.Model(mm).Update(ctrlS())
	if m.(Model).formKind != formNone {
		t.Fatal("an all-empty submit is valid and should close the form")
	}
	if cmd == nil {
		t.Fatal("clearing should dispatch an unfiltered reload")
	}
	if mm, _ := landTuples(t, m, tuplesLoadedMsg{}); mm.tupleFilters.applied.active() {
		t.Fatalf("empty submit should clear the filter, got %+v", mm.tupleFilters.applied)
	}
}

func TestTupleFilterFormPrefilledFromActiveFilter(t *testing.T) {
	mm := tuplesPanelModel()
	f := tupleFilter{user: "user:anne", relation: "viewer", object: "document:"}
	mm.tupleFilters.confirm(f)
	m, _ := tea.Model(mm).Update(key("f"))
	got := m.(Model).form.Values()
	if got[0] != "user:anne" || got[1] != "viewer" || got[2] != "document:" {
		t.Fatalf("form should pre-fill the active filter, got %v", got)
	}
}

// A filter the server rejected is still what the user asked for, so reopening
// the form must offer it back for editing rather than making them retype it.
func TestTupleFilterFormKeepsRejectedInput(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.form.SetValues([]string{"user:anne", "can view", "document:"})
	m, _ = tea.Model(mm).Update(ctrlS())
	mm, _ = landTuples(t, m, tuplesLoadedMsg{err: errors.New("400 invalid tuple_key")})
	m, _ = tea.Model(mm).Update(key("f"))
	got := m.(Model).form.Values()
	if got[0] != "user:anne" || got[1] != "can view" || got[2] != "document:" {
		t.Fatalf("a rejected filter should come back for editing, got %v", got)
	}
}

// A reload dispatched while a filter submit is in flight must carry the
// submitted filter, not the one still on screen — otherwise the reload lands
// last and the user's filter vanishes with no error and no toast.
func TestTupleFilterSurvivesConcurrentReload(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.form.SetValues([]string{"user:anne", "", "document:"})
	m, _ = tea.Model(mm).Update(ctrlS())
	want := tupleFilter{user: "user:anne", object: "document:"}
	if got := m.(Model).tupleFilters.wanted; got != want {
		t.Fatalf("submit should record the pending filter, got %+v", got)
	}
	// r, racing the submitted load, must re-send the same filter.
	m, _ = m.Update(key("r"))
	if got := m.(Model).tupleFilters.wanted; got != want {
		t.Fatalf("a racing reload must keep the pending filter, got %+v", got)
	}
	mm, _ = landTuples(t, m, tuplesLoadedMsg{filter: want})
	if mm.tupleFilters.applied != want {
		t.Fatalf("tupleFilter = %+v, want %+v", mm.tupleFilters.applied, want)
	}
}

// Deleting the active store leaves no store to filter, and f refuses to open
// without one — so a filter left behind here could never be cleared.
func TestStoreDeleteClearsTupleFilter(t *testing.T) {
	configtest.Isolate(t)
	mm := tuplesPanelModel()
	mm.tupleFilters.applied = tupleFilter{object: "document:roadmap"}
	mm.tupleFilters.wanted = mm.tupleFilters.applied
	mm.storeDeleting = true
	m, _ := tea.Model(mm).Update(storeDeletedMsg{
		origin: mm.mutationOrigin(mm.storeID, mm.modelID, mm.storeDeleteGen),
		id:     mm.storeID,
	})
	if got := m.(Model).tupleFilters; got != (tupleFilters{}) {
		t.Fatalf("deleting the active store must clear every filter field, got %+v", got)
	}
}

func TestTupleFilterPersistsAcrossRefresh(t *testing.T) {
	mm := tuplesPanelModel()
	mm.tupleFilters.applied = tupleFilter{object: "document:roadmap"}
	nm, cmd := tea.Model(mm).Update(key("r"))
	got := nm.(Model)
	if want := (tupleFilter{object: "document:roadmap"}); got.tupleFilters.applied != want {
		t.Fatalf("r must keep the filter, got %+v", got.tupleFilters.applied)
	}
	if cmd == nil {
		t.Fatal("r should dispatch a reload")
	}
}

func TestTupleFilterEscLeavesFilterUntouched(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.tupleFilters.applied = tupleFilter{object: "document:roadmap"}
	m, _ = tea.Model(mm).Update(key("esc"))
	mm = m.(Model)
	if mm.formKind != formNone {
		t.Fatal("esc should close the form")
	}
	if want := (tupleFilter{object: "document:roadmap"}); mm.tupleFilters.applied != want {
		t.Fatalf("esc must not change the filter, got %+v", mm.tupleFilters.applied)
	}
}

// The Tuples empty state tells the user to "press f"; that must work from the
// sidebar too, like the a/d call-to-action keys, not only once the panel is
// focused.
func TestTupleFilterKeyReachesPanelFromSidebar(t *testing.T) {
	mm := newTestModel().(Model)
	mm.section = secTuples
	mm.focus = shell.FocusSidebar
	m, _ := tea.Model(mm).Update(key("f"))
	if m.(Model).formKind != formTupleFilter {
		t.Fatalf("f on the sidebar should open the tuple filter, got kind=%d", m.(Model).formKind)
	}
}

// f is page-down in the Model section; the Tuples-only sidebar shortcut must
// not pull focus into other panels.
func TestFilterKeyFromSidebarIsTuplesOnly(t *testing.T) {
	mm := newTestModel().(Model)
	mm.section = secModel
	mm.focus = shell.FocusSidebar
	m, _ := tea.Model(mm).Update(key("f"))
	if got := m.(Model).focus; got != shell.FocusSidebar {
		t.Fatalf("f outside Tuples must leave sidebar focus alone, got %v", got)
	}
}

func TestMainTitleShowsActiveTupleFilter(t *testing.T) {
	m := newTestModel().(Model)
	m.section = secTuples
	if got := m.mainTitle(); strings.Contains(got, "filter:") {
		t.Fatalf("no filter should mean a plain title, got %q", got)
	}
	m.tupleFilters.applied = tupleFilter{user: "user:anne", object: "document:"}
	got := m.mainTitle()
	for _, want := range []string{"filter:", "user=user:anne", "object=document:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("title %q should contain %q", got, want)
		}
	}
	if strings.Contains(got, "relation=") {
		t.Fatalf("unset fields should not render, got %q", got)
	}
}

func TestSectionStatusMarksFilteredTuples(t *testing.T) {
	m := newTestModel().(Model)
	m.section = secTuples
	m.tupleFilters.applied = tupleFilter{object: "document:roadmap"}
	if got := m.sectionStatus(); !strings.Contains(got, "matching tuple") {
		t.Fatalf("filtered count should say matching, got %q", got)
	}
	m.tuplesCapped = true
	if got := m.sectionStatus(); !strings.Contains(got, "matching") || !strings.Contains(got, "more exist") {
		t.Fatalf("capped filtered count should keep both markers, got %q", got)
	}
}

func TestTupleHintFilterAware(t *testing.T) {
	if got := tupleHint("", true); got != "Select a store first — press 2" {
		t.Fatalf("no-store hint must win, got %q", got)
	}
	if got := tupleHint("store-1", false); !strings.Contains(got, "press a to add") {
		t.Fatalf("unfiltered empty hint should suggest adding, got %q", got)
	}
	if got := tupleHint("store-1", true); !strings.Contains(got, "press f") {
		t.Fatalf("filtered empty hint should point at f, got %q", got)
	}
}

// The Tuples key hints must advertise f, and must keep / distinct from it so
// the client-side and server-side filters aren't confused for each other.
func TestHelpAdvertisesTupleFilterKey(t *testing.T) {
	m := newTestModel().(Model)
	m.section = secTuples
	body := m.helpBody()
	for _, want := range []string{"f", "filter on the server", "loaded rows"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Tuples help should mention %q, got:\n%s", want, body)
		}
	}
	// "/" must not be described as a filter here, or the two read as one key.
	if strings.Contains(body, "/ filter") {
		t.Fatalf("/ should be described as a find, not a filter, got:\n%s", body)
	}
}

// The footer key row is always on screen, unlike the ? overlay, so it is the
// only place a user reliably discovers f.
func TestFooterAdvertisesTupleFilterKey(t *testing.T) {
	m := tuplesPanelModel()
	keys := strings.Join(m.statusKeys(), " ")
	if !strings.Contains(keys, "f filter") {
		t.Fatalf("Tuples footer should advertise f, got %q", keys)
	}
	if !strings.Contains(keys, "/ find") {
		t.Fatalf("Tuples footer should call / a find, not a filter, got %q", keys)
	}
}

// A write reloads the tuples pane; the active filter must ride along, or the
// reload would silently widen the view back out.
func TestTupleFilterSurvivesMutationReload(t *testing.T) {
	mm := tuplesPanelModel()
	mm.tupleFilters.applied = tupleFilter{object: "document:roadmap"}
	mm.tupleMutating = true
	m, cmd := tea.Model(mm).Update(tupleWrittenMsg{
		origin: mm.mutationOrigin(mm.storeID, mm.modelID, mm.tupleMutationGen),
		label:  "user:anne viewer document:roadmap",
	})
	if cmd == nil {
		t.Fatal("a write should dispatch a tuples reload")
	}
	if want := (tupleFilter{object: "document:roadmap"}); m.(Model).tupleFilters.applied != want {
		t.Fatalf("a write reload must keep the filter, got %+v", m.(Model).tupleFilters.applied)
	}
}

// A reconnect changes the server as well as the store, so the filter is even
// staler there than on a store switch.
func TestActivateResolvedClearsTupleFilter(t *testing.T) {
	configtest.Isolate(t)
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "store-1", "")
	m.tupleFilters.applied = tupleFilter{object: "document:roadmap"}

	other, _ := openfga.NewClient("http://localhost:9090")
	m.activateResolved(config.Resolved{StoreID: "store-2", APIURL: "http://localhost:9090"}, other, "switched")

	if m.tupleFilters != (tupleFilters{}) {
		t.Fatalf("a reconnect must clear every filter field, got %+v", m.tupleFilters)
	}
}

// A filter is store-specific: switching stores must clear it.
func TestSelectStoreClearsTupleFilter(t *testing.T) {
	configtest.Isolate(t)
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "store-1", "")
	m.tupleFilters.applied = tupleFilter{object: "document:roadmap"}

	m.selectStore(openfga.Store{ID: "store-2", Name: "other"})

	if m.tupleFilters != (tupleFilters{}) {
		t.Fatalf("a store switch must clear every filter field, got %+v", m.tupleFilters)
	}
}

// --- wire-level: the filter must actually reach /read, and the filter the
// model adopts must be the one that load ran with. Both halves are load
// bearing and neither is visible from a hand-built message.

func TestLoadTuplesCmdSendsTupleKeyAndEchoesFilter(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(openfga.ReadResponse{})
	}))
	t.Cleanup(srv.Close)
	cl, err := openfga.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	f := tupleFilter{user: "user:anne", relation: "viewer", object: "document:roadmap"}
	msg := loadTuplesCmd(context.Background(), cl, "store-1", f, 7)().(tuplesLoadedMsg)
	if msg.err != nil {
		t.Fatalf("read failed: %v", msg.err)
	}
	if msg.filter != f {
		t.Fatalf("the load must echo the filter it ran with, got %+v want %+v", msg.filter, f)
	}
	tk, _ := body["tuple_key"].(map[string]any)
	if tk == nil {
		t.Fatalf("an active filter must reach the wire as tuple_key, body = %v", body)
	}
	if tk["user"] != "user:anne" || tk["relation"] != "viewer" || tk["object"] != "document:roadmap" {
		t.Fatalf("tuple_key = %v, want the submitted filter", tk)
	}
}

func TestLoadTuplesCmdOmitsTupleKeyWhenUnfiltered(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(openfga.ReadResponse{})
	}))
	t.Cleanup(srv.Close)
	cl, _ := openfga.NewClient(srv.URL)

	msg := loadTuplesCmd(context.Background(), cl, "store-1", tupleFilter{}, 1)().(tuplesLoadedMsg)
	if msg.filter.active() {
		t.Fatalf("an unfiltered load must echo the zero filter, got %+v", msg.filter)
	}
	if _, ok := body["tuple_key"]; ok {
		t.Fatalf("no filter must mean no tuple_key on the wire, body = %v", body)
	}
}

// --- layout: the Tuples pane carries two hints in a ~21-column master pane.
// Overflowing it wraps the list and drops the status bar entirely, which is
// how the f advertisement got silently deleted once already.

func TestTuplesPaneFitsAndKeepsAdvertisingF(t *testing.T) {
	for _, w := range []int{80, 100, 120} {
		mm := tuplesPanelModel()
		m, _ := tea.Model(mm).Update(tea.WindowSizeMsg{Width: w, Height: 30})
		view := ansi.Strip(m.(Model).viewString())
		if !strings.Contains(view, "f filter") {
			t.Fatalf("w=%d: the footer must advertise f, got:\n%s", w, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Fatalf("w=%d: line overflows the frame by %d cols: %q", w, got-w, line)
			}
		}
	}
}

// An applied "/" find must survive a reload. SetItems re-runs it over the new
// rows; skipping that leaves the pane reading "No items." under a header that
// says rows are there.
func TestClientFindSurvivesTupleReload(t *testing.T) {
	mm := tuplesPanelModel()
	mm.tuples = append(mm.tuples, openfga.Tuple{
		Key: openfga.TupleKey{User: "user:bob", Relation: "viewer", Object: "document:other"},
	})
	mm.populateTuples()
	m := applyFind(t, tea.Model(mm), "anne")
	if got := len(m.(Model).tuplesList.Model.VisibleItems()); got != 1 {
		t.Fatalf("the find should narrow two rows to one, got %d visible", got)
	}

	mm, _ = landTuples(t, m, tuplesLoadedMsg{tuples: []openfga.Tuple{
		{Key: openfga.TupleKey{User: "user:anne", Relation: "owner", Object: "document:roadmap"}},
		{Key: openfga.TupleKey{User: "user:bob", Relation: "viewer", Object: "document:other"}},
	}})
	if got := len(mm.tuplesList.Model.VisibleItems()); got != 1 {
		t.Fatalf("the applied find must be re-run over the reloaded rows, got %d visible", got)
	}
	// The v toggle rebuilds every list; the find must survive that too.
	m3, _ := tea.Model(mm).Update(key("v"))
	if got := len(m3.(Model).tuplesList.Model.VisibleItems()); got != 1 {
		t.Fatalf("toggling compact view must not drop the applied find, got %d visible", got)
	}
}

// When the find hides every loaded row the pane is empty but the store is not,
// and an applied find leaves no other mark on screen — so the body has to name
// the cause, or the filter breadcrumb above it takes the blame.
func TestFindHidingEveryRowExplainsItself(t *testing.T) {
	// A find that matches, then a reload whose rows it no longer matches — the
	// way a stale find actually ends up hiding everything.
	m := applyFind(t, tea.Model(tuplesPanelModel()), "anne")
	mm, _ := landTuples(t, m, tuplesLoadedMsg{tuples: []openfga.Tuple{
		{Key: openfga.TupleKey{User: "user:bob", Relation: "viewer", Object: "document:other"}},
	}})
	if got := len(mm.tuplesList.Model.VisibleItems()); got != 0 {
		t.Fatalf("the stale find should hide every row, got %d visible", got)
	}
	body := ansi.Strip(mm.viewString())
	if !strings.Contains(body, "/ find hides all") {
		t.Fatalf("an empty-by-find pane must say so, got:\n%s", body)
	}
}

// Every reload path builds its command through tuplesReloadCmd, so one
// wire-level check covers all of them: what the user asked for is what gets
// sent, not the filter the rows on screen happen to carry.
func TestTuplesReloadCmdSendsTheWantedFilter(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(openfga.ReadResponse{})
	}))
	t.Cleanup(srv.Close)
	cl, _ := openfga.NewClient(srv.URL)

	mm := tuplesPanelModel()
	mm.client = cl
	mm.tupleFilters.confirm(tupleFilter{object: "document:old"}) // what is on screen
	mm.tupleFilters.request(tupleFilter{object: "document:new"}) // what was just asked for
	mm.tuplesReloadCmd()()

	tk, _ := body["tuple_key"].(map[string]any)
	if tk == nil || tk["object"] != "document:new" {
		t.Fatalf("a reload must send the wanted filter, got %v", body)
	}
}

// A filter the server refuses must not stay armed: it would go on failing every
// later reload — r, and the reload each tuple write triggers — with nothing on
// screen saying why. The user's text survives for the form regardless.
func TestRejectedFilterDoesNotPoisonLaterReloads(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.form.SetValues([]string{"user:anne", "can view", "document:"})
	m, _ = tea.Model(mm).Update(ctrlS())
	mm, _ = landTuples(t, m, tuplesLoadedMsg{err: errors.New("400 invalid tuple_key")})
	if mm.tupleFilters.wanted.active() {
		t.Fatalf("a refused filter must not stay armed for later reloads, got %+v", mm.tupleFilters.wanted)
	}
	if !mm.tupleFilters.draft.active() {
		t.Fatal("the draft must keep the refused text so f can offer it back")
	}
}

// The header names every set field, object first, and never renders raw
// terminal escapes from a value the user typed.
func TestTupleFilterFieldsRendersEveryField(t *testing.T) {
	got := tupleFilterFields(tupleFilter{user: "user:anne", relation: "viewer", object: "document:roadmap"})
	if want := "object=document:roadmap user=user:anne relation=viewer"; got != want {
		t.Fatalf("tupleFilterFields = %q, want %q", got, want)
	}
	if got := tupleFilterFields(tupleFilter{object: "doc\x1b]0;pwned\x07:1"}); strings.ContainsRune(got, 0x1b) {
		t.Fatalf("filter values must be sanitized for the header, got %q", got)
	}
}

// The form's own field validators must actually be wired, not just correct.
func TestTupleFilterFormValidatesFields(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.form.SetValues([]string{"anne", "", "document:roadmap"}) // user with no colon
	m, _ = tea.Model(mm).Update(ctrlS())
	if m.(Model).formKind != formTupleFilter {
		t.Fatal("a malformed user must keep the form open")
	}
}

// The store can be deleted while the form sits open on top of it.
func TestTupleFilterSubmitWithoutStoreIsRefused(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.storeID = ""
	mm.form.SetValues([]string{"", "", "document:roadmap"})
	m, cmd := tea.Model(mm).Update(ctrlS())
	if m.(Model).formErr == "" {
		t.Fatal("submitting with no store must raise an error, not dispatch a read")
	}
	if cmd != nil {
		t.Fatal("submitting with no store must not dispatch a read")
	}
}
