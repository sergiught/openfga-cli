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

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/log/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/fga"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
	"github.com/sergiught/openfga-cli/internal/ui/shell"
)

// The tuples pane's /read request carries a tuple_key filter only when a
// filter is active — same shape the CLI's `tuples read` sends.
func TestTupleReadRequestNoFilter(t *testing.T) {
	req := tupleReadRequest(tupleFilter{})
	if req.TupleKey != nil {
		t.Fatalf("no filter should mean no tuple_key, got %+v", req.TupleKey)
	}
	if req.PageSize != 100 {
		t.Fatalf("page size = %d, want 100", req.PageSize)
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
		{"   ", true}, // whitespace-only reads as unset, like every other field
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
		// The server's rule ignores the relation, so it cannot stand in for the
		// object id or the user.
		{"bare type + relation only", tupleFilter{object: "document:", relation: "viewer"}, false},
		{"padded leading colon with user", tupleFilter{object: " :roadmap", user: "user:anne"}, false},
		{"padded user with bare type", tupleFilter{object: "document:", user: " user:anne "}, true},
		{"whitespace user with bare type", tupleFilter{object: "document:", user: "   "}, false},
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

// readBody starts a server that records the last /read body it was sent.
func readBody(t *testing.T) (*openfga.Client, func() map[string]any) {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		body = b
		_ = json.NewEncoder(w).Encode(openfga.ReadResponse{})
	}))
	t.Cleanup(srv.Close)
	cl, err := openfga.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return cl, func() map[string]any { return body }
}

// rejectedFilterModel submits a filter the server refuses (the relation has a
// space, which its proto rule rejects) and lands failErr as the read's outcome.
func rejectedFilterModel(t *testing.T, cl *openfga.Client, failErr error) Model {
	t.Helper()
	m := openTupleFilter(t)
	mm := m.(Model)
	if cl != nil {
		mm.client = cl
	}
	mm.form.SetValues([]string{"user:anne", "can view", "document:"})
	m, _ = tea.Model(mm).Update(ctrlS())
	mm, _ = landTuples(t, m, tuplesLoadedMsg{err: failErr})
	return mm
}

// errRefused is what the server returns for a filter it will not accept;
// errUnreachable is a read that never got there.
var (
	errRefused     = errors.New("400 invalid tuple_key")
	errUnreachable = errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
)

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

func TestTupleFilterSubmitDefersAdoptionUntilTheLoadLands(t *testing.T) {
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
	if !mm.loading {
		t.Fatal("submit should mark the pane loading, as every other reload does")
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

	// An ordinary unfiltered reload has nothing to announce.
	mm.status = ""
	again, _ := landTuples(t, tea.Model(mm), tuplesLoadedMsg{})
	if strings.Contains(again.status, "cleared") {
		t.Fatalf("a reload with no filter to clear must stay quiet, got %q", again.status)
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
func TestTupleFilterKeyFromSidebarIsTuplesOnly(t *testing.T) {
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

// The hint that names f in an empty filtered pane is what makes the sidebar
// call-to-action path mean anything; test it through the render, not just the
// function.
func TestWriteHiddenByFilterSaysSo(t *testing.T) {
	mm := tuplesPanelModel()
	mm.tupleFilters.confirm(tupleFilter{object: "document:roadmap"})
	mm.pendingTupleSelect = "folder:secret#viewer@user:zed" // not in the result
	mm.populateTuples()
	if !strings.Contains(mm.status, "filter hides it") {
		t.Fatalf("a write the filter excludes must say so, got %q", mm.status)
	}
}

// The notice is specific to a write the filter hides: a write it shows, and a
// write with no filter at all, must both stay quiet.
func TestWriteHiddenByFilterStaysQuietOtherwise(t *testing.T) {
	shown := tuplesPanelModel()
	shown.tupleFilters.confirm(tupleFilter{object: "document:roadmap"})
	shown.pendingTupleSelect = fga.FormatTuple(shown.tuples[0].Key)
	shown.populateTuples()
	if strings.Contains(shown.status, "filter hides it") {
		t.Fatalf("a write the filter shows must stay quiet, got %q", shown.status)
	}

	unfiltered := tuplesPanelModel()
	unfiltered.pendingTupleSelect = "folder:secret#viewer@user:zed"
	unfiltered.populateTuples()
	if strings.Contains(unfiltered.status, "filter hides it") {
		t.Fatalf("with no filter there is nothing to blame, got %q", unfiltered.status)
	}

	inflight := tuplesPanelModel()
	inflight.tupleFilters.confirm(tupleFilter{object: "document:roadmap"})
	inflight.tupleMutating = true
	inflight.pendingTupleSelect = "folder:secret#viewer@user:zed"
	inflight.populateTuples()
	if strings.Contains(inflight.status, "filter hides it") {
		t.Fatalf("a write still in flight has not been hidden yet, got %q", inflight.status)
	}
}

func TestFilteredEmptyPaneNamesF(t *testing.T) {
	mm := tuplesPanelModel()
	mm.tupleFilters.confirm(tupleFilter{object: "document:roadmap"})
	mm.tuples = nil
	mm.populateTuples()
	if body := ansi.Strip(mm.sectionBody()); !strings.Contains(body, "press f") {
		t.Fatalf("a filtered empty pane must point at f, got:\n%s", body)
	}
}

// reject() cannot tell a 400 from a dead socket, so the notice must not blame
// the filter when the read never arrived — the user would edit one that is fine.
func TestFilterDialogDistinguishesAConnectionFailure(t *testing.T) {
	mm := rejectedFilterModel(t, nil, errUnreachable)
	// The connection can come back before the user reopens the form, so the
	// notice must not be re-derived from a live flag.
	mm.connLost = false
	m, _ := tea.Model(mm).Update(key("f"))
	body := ansi.Strip(func() string { _, b := m.(Model).dialogContent(); return b }())
	if strings.Contains(body, "refused") {
		t.Fatalf("a connection failure must not be reported as a refusal, got:\n%s", body)
	}
	if !strings.Contains(body, "never reached the server") {
		t.Fatalf("a connection failure should say so, got:\n%s", body)
	}
}

// Reopening the form before the server has answered is not a refusal.
func TestFilterDialogDoesNotCryRefusalMidFlight(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.form.SetValues([]string{"", "", "document:roadmap"})
	m, _ = tea.Model(mm).Update(ctrlS())
	m, _ = m.Update(key("f")) // reopened while the read is still in flight
	if _, body := m.(Model).dialogContent(); strings.Contains(ansi.Strip(body), "refused") {
		t.Fatalf("nothing was refused yet, got:\n%s", ansi.Strip(body))
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
// The Tuples key row is tiered: the count on the other side of the footer is
// worth more than the compact hint on a narrow terminal.
func TestTuplesFooterTiersOnWidth(t *testing.T) {
	narrow := tuplesPanelModel()
	narrow.width = 100
	if keys := strings.Join(narrow.statusKeys(), " "); !strings.Contains(keys, "d del ") || strings.Contains(keys, "compact") || strings.Contains(keys, "detail") {
		t.Fatalf("a narrow footer should drop the compact hint, got %q", keys)
	}
	wide := tuplesPanelModel()
	wide.width = 140
	if keys := strings.Join(wide.statusKeys(), " "); !strings.Contains(keys, "d delete") || (!strings.Contains(keys, "compact") && !strings.Contains(keys, "detail")) {
		t.Fatalf("a wide footer should spell out delete and keep the compact hint, got %q", keys)
	}
}

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

// A reconnect changes the server as well as the store, so the filter is even
// staler there than on a store switch.
func TestActivateResolvedClearsTupleFilter(t *testing.T) {
	configtest.Isolate(t)
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "store-1", "")
	m.tupleFilters.applied = tupleFilter{object: "document:roadmap"}
	// A "/" find is cleared alongside it, so it has to be applied to begin with.
	m.tuplesList.SetItems([]uilist.Item{{TitleText: "user:anne", Filter: "user:anne"}})
	m.tuplesList.Model.SetFilterText("anne")

	other, _ := openfga.NewClient("http://localhost:9090")
	m.activateResolved(config.Resolved{StoreID: "store-2", APIURL: "http://localhost:9090"}, other, "switched")

	if m.tupleFilters != (tupleFilters{}) {
		t.Fatalf("a reconnect must clear every filter field, got %+v", m.tupleFilters)
	}
	if m.tuplesList.Model.FilterState() != list.Unfiltered {
		t.Fatalf("a reconnect must clear the / find too, got %v", m.tuplesList.Model.FilterState())
	}
}

// A filter is store-specific: switching stores must clear it.
func TestSelectStoreClearsTupleFilter(t *testing.T) {
	configtest.Isolate(t)
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "store-1", "")
	m.tupleFilters.applied = tupleFilter{object: "document:roadmap"}
	// A "/" find is cleared alongside it, so it has to be applied to begin with.
	m.tuplesList.SetItems([]uilist.Item{{TitleText: "user:anne", Filter: "user:anne"}})
	m.tuplesList.Model.SetFilterText("anne")

	m.selectStore(openfga.Store{ID: "store-2", Name: "other"})

	if m.tupleFilters != (tupleFilters{}) {
		t.Fatalf("a store switch must clear every filter field, got %+v", m.tupleFilters)
	}
	if m.tuplesList.Model.FilterState() != list.Unfiltered {
		t.Fatalf("a store switch must clear the / find too, got %v", m.tuplesList.Model.FilterState())
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
		// The key row is truncated from the right, so the last keycap surviving
		// is what proves the row still fits.
		if !strings.Contains(view, "? help") {
			t.Fatalf("w=%d: the key row is truncated, got:\n%s", w, view)
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
	if !strings.Contains(body, "hides 1 loaded row —") {
		t.Fatalf("an empty-by-find pane must say so, got:\n%s", body)
	}
}

// While the user is still typing a find, the "/" input is drawn inside the
// list — so swapping the list out for the hint would delete the box they are
// typing into, exactly when a term stops matching and they need to fix it.
func TestFindHintStaysOutOfTheWayWhileTyping(t *testing.T) {
	var m tea.Model = tuplesPanelModel()
	m = pump(t, m, key("/"))
	for _, r := range "zzzz" {
		m = pump(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	mm := m.(Model)
	if !mm.tuplesList.SettingFilter() {
		t.Fatal("expected to be mid-typing a find")
	}
	if got := len(mm.tuplesList.Model.VisibleItems()); got != 0 {
		t.Fatalf("the term should match nothing, got %d visible", got)
	}
	body := ansi.Strip(mm.viewString())
	if strings.Contains(body, "hides") {
		t.Fatalf("the hint must not replace the input being typed into, got:\n%s", body)
	}
	if !strings.Contains(body, "find: zzzz") {
		t.Fatalf("the find input must stay on screen, got:\n%s", body)
	}
}

// The header names every set field, object first, and never renders raw
// terminal escapes from a value the user typed.
func TestTupleFilterFieldsRendersEveryField(t *testing.T) {
	got := tupleFilterFields(tupleFilter{user: "user:anne", relation: "viewer", object: "document:roadmap"})
	if want := "object=document:roadmap user=user:anne relation=viewer"; got != want {
		t.Fatalf("tupleFilterFields = %q, want %q", got, want)
	}
	const evil = "a\x1b]0;pwned\x07:1"
	for name, f := range map[string]tupleFilter{
		"user":     {user: evil, object: "document:roadmap"},
		"relation": {relation: evil, object: "document:roadmap"},
		"object":   {object: evil},
	} {
		if got := tupleFilterFields(f); strings.ContainsRune(got, 0x1b) {
			t.Errorf("%s must be sanitized for the header, got %q", name, got)
		}
	}
}

// The form's own field validators must actually be wired, not just correct.
func TestTupleFilterFormValidatesUser(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.form.SetValues([]string{"anne", "", "document:roadmap"}) // user with no colon
	m, _ = tea.Model(mm).Update(ctrlS())
	if m.(Model).formKind != formTupleFilter {
		t.Fatal("a malformed user must keep the form open")
	}
	if got := ansi.Strip(m.(Model).form.View()); !strings.Contains(got, "user") && !strings.Contains(got, "User") {
		t.Fatalf("the error should name the user field, got:\n%s", got)
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

// --- the reload paths, at the wire. tuplesReloadCmd is the single builder, but
// only a request that actually leaves each call site proves the site uses it.

// r and the reload a write triggers must both send the filter the user asked
// for, not the one the rows on screen were read with.
func TestReloadPathsSendTheWantedFilter(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  func(Model) tea.Msg
	}{
		{"r", func(Model) tea.Msg { return key("r") }},
		{"write reload", func(m Model) tea.Msg {
			return tupleWrittenMsg{origin: m.mutationOrigin(m.storeID, m.modelID, m.tupleMutationGen), label: "x"}
		}},
	} {
		cl, body := readBody(t)
		mm := tuplesPanelModel()
		mm.client = cl
		mm.tupleMutating = true
		mm.tupleFilters.confirm(tupleFilter{object: "document:old"})
		mm.tupleFilters.request(tupleFilter{object: "document:new"})

		_, cmd := tea.Model(mm).Update(tc.msg(mm))
		if cmd == nil {
			t.Fatalf("%s: expected a reload command", tc.name)
		}
		var queue []tea.Msg
		collectCmd(cmd, &queue) // a write dispatches several loads at once
		tk, _ := body()["tuple_key"].(map[string]any)
		if tk == nil || tk["object"] != "document:new" {
			t.Fatalf("%s: reload sent %v, want the wanted filter", tc.name, body())
		}
	}
}

// After a rejection the wanted filter backs out, so a later reload must read
// the store the way the rows on screen were read — unfiltered here.
func TestReloadAfterRejectionSendsNoFilter(t *testing.T) {
	cl, body := readBody(t)
	mm := rejectedFilterModel(t, cl, errRefused)

	_, cmd := tea.Model(mm).Update(key("r"))
	if cmd == nil {
		t.Fatal("expected a reload command")
	}
	var queue []tea.Msg
	collectCmd(cmd, &queue)
	if body() == nil {
		t.Fatal("no /read reached the server")
	}
	if _, ok := body()["tuple_key"]; ok {
		t.Fatalf("a reload after a rejection must not re-send the refused filter, got %v", body())
	}
}

// A refused filter is still owed to the user: an unrelated reload landing in
// between must not wipe it out of the form.
func TestRejectedDraftSurvivesAnUnrelatedReload(t *testing.T) {
	mm := rejectedFilterModel(t, nil, errRefused)
	mm, _ = landTuples(t, tea.Model(mm), tuplesLoadedMsg{}) // e.g. the reload a write triggers
	m, _ := tea.Model(mm).Update(key("f"))
	if got := m.(Model).form.Values(); got[1] != "can view" {
		t.Fatalf("the refused filter should still be in the form, got %v", got)
	}
}

// --- the render branches the header, dialog and footer gained.

func TestMainTitleAtTheDisplayCap(t *testing.T) {
	m := tuplesPanelModel()
	m.tuplesCapped = true
	if got := m.mainTitle(); !strings.Contains(got, "first 500") || !strings.Contains(got, "press f") {
		t.Fatalf("a capped unfiltered pane should point at f, got %q", got)
	}
	m.tupleFilters.confirm(tupleFilter{object: "document:roadmap"})
	got := m.mainTitle()
	if !strings.Contains(got, "object=document:roadmap") || !strings.Contains(got, "first 500") {
		t.Fatalf("a capped filtered pane should show both the filter and the cap, got %q", got)
	}
	// The header truncates from the right, so a long filter must not be able to
	// push the cap marker off the end.
	m.tupleFilters.confirm(tupleFilter{
		object:   "document:" + strings.Repeat("x", 120),
		user:     "user:" + strings.Repeat("y", 60),
		relation: strings.Repeat("z", 40),
	})
	nm, _ := tea.Model(m).Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = nm.(Model)
	if view := ansi.Strip(m.viewString()); !strings.Contains(view, "first 500") {
		t.Fatalf("the cap marker must survive a long filter, got:\n%s", view)
	}
}

func TestFilterDialogExplainsItself(t *testing.T) {
	m := openTupleFilter(t).(Model)
	title, body := m.dialogContent()
	if title != "Filter Tuples" {
		t.Fatalf("dialog title = %q", title)
	}
	body = ansi.Strip(body)
	if !strings.Contains(body, "Re-reads from the server") {
		t.Fatalf("the dialog must say it re-reads from the server, got:\n%s", body)
	}
	if !strings.Contains(body, "ctrl+s apply") {
		t.Fatalf("the dialog must label ctrl+s as apply, got:\n%s", body)
	}
	keys := strings.Join(m.statusKeys(), " ")
	if !strings.Contains(keys, "ctrl+s apply") || strings.Contains(keys, "save") {
		t.Fatalf("the footer must say apply, not save, got %q", keys)
	}
}

// A rejection with nothing applied leaves the header showing no filter while
// the form holds the refused text; the dialog has to reconcile the two.
func TestFilterDialogFlagsARefusedDraft(t *testing.T) {
	mm := rejectedFilterModel(t, nil, errRefused)
	m, _ := tea.Model(mm).Update(key("f"))
	_, body := m.(Model).dialogContent()
	if !strings.Contains(ansi.Strip(body), "refused") {
		t.Fatalf("the dialog should say the filter was refused, got:\n%s", ansi.Strip(body))
	}
}

// The tuples list carries its own filter chrome: the two keys named apart in
// the title bar, and a prompt that does not repeat the word "filter".
func TestTuplesListFilterChrome(t *testing.T) {
	m := tuplesPanelModel()
	idle := ansi.Strip(m.tuplesList.View())
	if !strings.Contains(idle, "/ find") || !strings.Contains(idle, "f filter") {
		t.Fatalf("the list should name both filters, got:\n%s", idle)
	}
	nm, _ := tea.Model(m).Update(key("/"))
	typing := ansi.Strip(nm.(Model).tuplesList.View())
	if !strings.Contains(typing, "find:") || strings.Contains(typing, "filter:") {
		t.Fatalf("the / input should prompt with find:, got:\n%s", typing)
	}
}

// The object field's validator must be wired, not merely correct.
func TestTupleFilterFormValidatesObject(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.form.SetValues([]string{"", "", "document:1#viewer"}) // a userset, not an object
	m, _ = tea.Model(mm).Update(ctrlS())
	if m.(Model).formKind != formTupleFilter {
		t.Fatal("a userset-shaped object must keep the form open")
	}
}
