package playground

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/log/v2"

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
	if req.PageSize != 100 {
		t.Fatalf("page size should stay 100, got %d", req.PageSize)
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
	if m.(Model).status != "select a store first" {
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
	if want := (tupleFilter{user: "user:anne", object: "document:"}); mm.tupleFilter != want {
		t.Fatalf("tupleFilter = %+v, want %+v", mm.tupleFilter, want)
	}
	if mm.tuplesGen != genBefore+1 {
		t.Fatalf("submit should bump tuplesGen for the reload, got %d want %d", mm.tuplesGen, genBefore+1)
	}
	if cmd == nil {
		t.Fatal("submit should dispatch a tuples reload command")
	}
}

// Submitted values are trimmed, so a stray space can't produce a filter that
// looks inactive in the header but still narrows the read.
func TestTupleFilterSubmitTrimsValues(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.form.SetValues([]string{"  user:anne  ", "  ", " document:roadmap "})
	m, _ = tea.Model(mm).Update(ctrlS())
	mm = m.(Model)
	if want := (tupleFilter{user: "user:anne", object: "document:roadmap"}); mm.tupleFilter != want {
		t.Fatalf("tupleFilter = %+v, want %+v", mm.tupleFilter, want)
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
	if mm.tupleFilter.active() {
		t.Fatalf("invalid submit must not set the filter, got %+v", mm.tupleFilter)
	}
}

func TestTupleFilterEmptySubmitClears(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.tupleFilter = tupleFilter{object: "document:roadmap"}
	mm.form.SetValues([]string{"", "", ""})
	m, cmd := tea.Model(mm).Update(ctrlS())
	mm = m.(Model)
	if mm.tupleFilter.active() {
		t.Fatalf("empty submit should clear the filter, got %+v", mm.tupleFilter)
	}
	if cmd == nil {
		t.Fatal("clearing should dispatch an unfiltered reload")
	}
}

func TestTupleFilterFormPrefilledFromActiveFilter(t *testing.T) {
	mm := tuplesPanelModel()
	mm.tupleFilter = tupleFilter{user: "user:anne", relation: "viewer", object: "document:"}
	m, _ := tea.Model(mm).Update(key("f"))
	got := m.(Model).form.Values()
	if got[0] != "user:anne" || got[1] != "viewer" || got[2] != "document:" {
		t.Fatalf("form should pre-fill the active filter, got %v", got)
	}
}

func TestTupleFilterPersistsAcrossRefresh(t *testing.T) {
	mm := tuplesPanelModel()
	mm.tupleFilter = tupleFilter{object: "document:roadmap"}
	nm, cmd := tea.Model(mm).Update(key("r"))
	got := nm.(Model)
	if want := (tupleFilter{object: "document:roadmap"}); got.tupleFilter != want {
		t.Fatalf("r must keep the filter, got %+v", got.tupleFilter)
	}
	if cmd == nil {
		t.Fatal("r should dispatch a reload")
	}
}

func TestTupleFilterEscLeavesFilterUntouched(t *testing.T) {
	m := openTupleFilter(t)
	mm := m.(Model)
	mm.tupleFilter = tupleFilter{object: "document:roadmap"}
	m, _ = tea.Model(mm).Update(key("esc"))
	mm = m.(Model)
	if mm.formKind != formNone {
		t.Fatal("esc should close the form")
	}
	if want := (tupleFilter{object: "document:roadmap"}); mm.tupleFilter != want {
		t.Fatalf("esc must not change the filter, got %+v", mm.tupleFilter)
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
	m.tupleFilter = tupleFilter{user: "user:anne", object: "document:"}
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
	m.tupleFilter = tupleFilter{object: "document:roadmap"}
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
	if !strings.Contains(body, "/read") {
		t.Fatalf("Tuples help should label f as the server-side /read filter, got:\n%s", body)
	}
}

// A filter is store-specific: switching stores must clear it.
func TestSelectStoreClearsTupleFilter(t *testing.T) {
	configtest.Isolate(t)
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "store-1", "")
	m.tupleFilter = tupleFilter{object: "document:roadmap"}

	m.selectStore(openfga.Store{ID: "store-2", Name: "other"})

	if m.tupleFilter.active() {
		t.Fatalf("store switch must clear the tuple filter, got %+v", m.tupleFilter)
	}
}
