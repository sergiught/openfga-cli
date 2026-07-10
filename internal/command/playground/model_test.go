package playground

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/app"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/fga"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

func sampleGraph() fga.Graph {
	return fga.ParseModel(&openfga.AuthorizationModel{
		ID:            "model-1",
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{
				Type: "document",
				Relations: map[string]any{
					"owner":  map[string]any{"this": map[string]any{}},
					"viewer": map[string]any{"computedUserset": map[string]any{"relation": "owner"}},
				},
				Metadata: map[string]any{"relations": map[string]any{
					"owner": map[string]any{"directly_related_user_types": []any{map[string]any{"type": "user"}}},
				}},
			},
		},
	})
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "shift+up":
		return tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}
	case "shift+down":
		return tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}
	case "shift+left":
		return tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}
	case "shift+right":
		return tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	default:
		r := []rune(s)[0]
		return tea.KeyPressMsg{Code: r, Text: s}
	}
}

func newTestModel() tea.Model {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := app.New(log.New(io.Discard), config.New(), "test")
	mdl := newModel(context.Background(), a, cl, "store-1")
	var m tea.Model = mdl
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = m.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-1", Name: "demo"}, {ID: "store-2", Name: "other"}}})
	m, _ = m.Update(modelLoadedMsg{modelID: "model-1", graph: sampleGraph()})
	m, _ = m.Update(modelsListedMsg{models: []openfga.AuthorizationModel{{ID: "model-1", SchemaVersion: "1.1"}}})
	m, _ = m.Update(tuplesLoadedMsg{tuples: []openfga.Tuple{
		{Key: openfga.TupleKey{User: "user:anne", Relation: "owner", Object: "document:roadmap"}},
	}})
	m, _ = m.Update(changesLoadedMsg{changes: []openfga.TupleChange{
		{TupleKey: openfga.TupleKey{User: "user:anne", Relation: "owner", Object: "document:roadmap"}, Operation: "TUPLE_OPERATION_WRITE"},
	}})
	m, _ = m.Update(assertionsLoadedMsg{modelID: "model-1", assertions: []openfga.Assertion{
		{TupleKey: openfga.CheckRequestTupleKey{User: "user:anne", Relation: "viewer", Object: "document:roadmap"}, Expectation: true},
	}})
	return m
}

func render(t *testing.T, m tea.Model, ctx string) {
	t.Helper()
	if strings.TrimSpace(m.(Model).viewString()) == "" {
		t.Fatalf("empty view: %s", ctx)
	}
}

func TestSections(t *testing.T) {
	m := newTestModel()
	for _, k := range []string{"1", "2", "3", "4", "5", "6"} {
		m, _ = m.Update(key(k))
		render(t, m, "section "+k)
	}
	// tab cycle wraps around.
	for i := 0; i < len(sectionNames)+1; i++ {
		m, _ = m.Update(key("tab"))
		render(t, m, "tab cycle")
	}
}

// TestFocusEnterDescendsEscAscends covers the core master-detail toggle: the
// model launches in sidebar (tab) focus, enter descends into the panel without
// changing the section, and esc returns to the sidebar.
func TestFocusEnterDescendsEscAscends(t *testing.T) {
	m := newTestModel()
	if m.(Model).focus != shell.FocusSidebar {
		t.Fatal("should launch in sidebar (tab) focus")
	}
	before := m.(Model).section
	m, _ = m.Update(key("enter"))
	if mod := m.(Model); mod.focus != shell.FocusPanel {
		t.Fatal("enter should descend into panel focus")
	} else if mod.section != before {
		t.Fatal("enter should not change the section")
	}
	m, _ = m.Update(key("esc"))
	if m.(Model).focus != shell.FocusSidebar {
		t.Fatal("esc should return to sidebar focus")
	}
}

// TestFocusSidebarMovesTabsPanelDoesNot verifies ↑↓ move the highlighted tab
// in sidebar focus, while in panel focus tab/arrows never switch sections
// (strict modes).
func TestFocusSidebarMovesTabsPanelDoesNot(t *testing.T) {
	m := newTestModel() // secStores, sidebar focus
	m, _ = m.Update(key("down"))
	if m.(Model).section != secModel {
		t.Fatal("down in sidebar focus should move to the next tab")
	}
	m, _ = m.Update(key("up"))
	if m.(Model).section != secStores {
		t.Fatal("up in sidebar focus should move to the previous tab")
	}
	m, _ = m.Update(key("enter")) // descend
	sec := m.(Model).section
	for _, k := range []string{"tab", "shift+tab", "right", "left"} {
		m, _ = m.Update(key(k))
		if m.(Model).section != sec {
			t.Fatalf("%q must not switch sections in panel focus", k)
		}
		if m.(Model).focus != shell.FocusPanel {
			t.Fatalf("%q must not drop panel focus", k)
		}
	}
}

// TestFocusLayeredEscInQuery verifies esc unwinds one level at a time: first
// esc closes the edit form (staying in the panel), second esc returns to the
// sidebar.
func TestFocusLayeredEscInQuery(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5"))     // Query tab
	m, _ = m.Update(key("enter")) // descend
	m = pump(t, m, key("i"))      // open the edit form
	if !m.(Model).editing {
		t.Fatal("i should start editing")
	}
	m, _ = m.Update(key("esc")) // layer 1: close the form, stay in the panel
	if mod := m.(Model); mod.editing {
		t.Fatal("first esc should exit editing")
	} else if mod.focus != shell.FocusPanel {
		t.Fatal("first esc should stay in panel focus")
	}
	m, _ = m.Update(key("esc")) // layer 2: back to the sidebar
	if m.(Model).focus != shell.FocusSidebar {
		t.Fatal("second esc should return to sidebar focus")
	}
}

func TestStoresSelectAndModelSwitch(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("1"))     // Stores tab
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("down"))  // move to store-2 in the list
	m, _ = m.Update(key("enter")) // select it
	if mod := m.(Model); mod.storeID != "store-2" {
		t.Fatalf("expected store-2 selected after descend+enter, got %q", mod.storeID)
	}
	render(t, m, "store selected")

	m, _ = m.Update(key("esc"))   // back to the sidebar
	m, _ = m.Update(key("2"))     // Model tab
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("m"))     // open model picker
	if !m.(Model).modelPicking {
		t.Fatal("m should open the model picker in the panel")
	}
	render(t, m, "model picking")
	m, _ = m.Update(key("esc")) // cancel picker (layered esc)
	if m.(Model).modelPicking {
		t.Fatal("esc should close the model picker")
	}
	render(t, m, "model picker cancelled")
}

func TestFiltering(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("3"))     // Tuples
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("/"))     // start filter
	for _, r := range "anne" {
		m, _ = m.Update(key(string(r)))
	}
	render(t, m, "filtering tuples")
	m, _ = m.Update(key("esc")) // clear filter
	render(t, m, "filter cleared")
}

func TestQueryForm(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5"))     // Query
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("m"))     // cycle mode -> list-objects
	m, _ = m.Update(key("i"))     // edit
	for _, r := range "document" {
		m, _ = m.Update(key(string(r)))
	}
	render(t, m, "query editing")
	m, _ = m.Update(key("esc")) // cancel editing
	m, _ = m.Update(queryResultMsg{title: "Check", lines: []string{"user:anne viewer document:roadmap"}, ok: true, badge: true})
	render(t, m, "query result")
}

func TestCreateStoreForm(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("1"))     // Stores
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("n"))     // create form
	render(t, m, "create store form")
	for _, r := range "newstore" {
		m, _ = m.Update(key(string(r)))
	}
	m, _ = m.Update(key("esc")) // cancel
	render(t, m, "create store cancelled")
}

func TestWriteTupleForm(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("3"))     // Tuples
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("a"))     // write form
	render(t, m, "write tuple form")
	m, _ = m.Update(key("esc"))
	render(t, m, "write tuple cancelled")
}

func TestAssertionsRun(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("6"))
	m, _ = m.Update(assertTestMsg{
		results: []assertResult{{ran: true, label: "user:anne viewer document:roadmap", expected: true, got: true, pass: true}},
		passed:  1, total: 1,
	})
	render(t, m, "assertions results")
}

func TestAssertionAddFlow(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("6"))     // Assertions
	m, _ = m.Update(key("enter")) // descend
	m, _ = m.Update(key("a"))     // add form
	if mod := m.(Model); mod.formKind != formWriteAssertion || mod.assertEditIdx != -1 {
		t.Fatalf("a should open the add form; got kind=%d idx=%d", mod.formKind, mod.assertEditIdx)
	}
	for _, r := range "user:zed" {
		m, _ = m.Update(key(string(r)))
	}
	m, _ = m.Update(key("tab"))
	for _, r := range "admin" {
		m, _ = m.Update(key(string(r)))
	}
	m, _ = m.Update(key("tab"))
	for _, r := range "repo:x" {
		m, _ = m.Update(key(string(r)))
	}
	m, _ = m.Update(key("tab")) // Expect toggle (starts Allowed)
	m, _ = m.Update(key(" "))   // flip to Denied
	m, cmd := m.Update(key("enter"))
	if mod := m.(Model); mod.formKind != formNone {
		t.Fatal("submitting the form should close it")
	}
	if cmd == nil {
		t.Fatal("submitting should trigger the assertion write")
	}
}

func TestAssertionEditPrefill(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("6"))
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("e")) // edit the selected (only) assertion
	mod := m.(Model)
	if mod.formKind != formWriteAssertion || mod.assertEditIdx != 0 {
		t.Fatalf("e should open the edit form for idx 0; got kind=%d idx=%d", mod.formKind, mod.assertEditIdx)
	}
	got := mod.form.Values()
	want := []string{"user:anne", "viewer", "document:roadmap", "true"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("prefill[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAssertionRunOneSetsBadge(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("6"))
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(assertOneMsg{idx: 0, result: assertResult{ran: true, expected: true, got: false, pass: false}})
	mod := m.(Model)
	if len(mod.assertResults) != 1 || !mod.assertResults[0].ran {
		t.Fatal("assertOneMsg should record the row's result")
	}
	if body := stripANSIView(mod.viewString()); !strings.Contains(body, "FAIL") {
		t.Fatalf("body should show the FAIL badge; got:\n%s", body)
	}
}

func TestAssertionDeleteWrites(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("6"))
	m, _ = m.Update(key("enter"))
	_, cmd := m.Update(key("d")) // delete the selected assertion
	if cmd == nil {
		t.Fatal("d should trigger a write with the assertion removed")
	}
}

// skipBackgroundMsg filters out the cursor-blink and animation-tick messages
// that would otherwise loop forever (or block on a timer) while pumping.
func skipBackgroundMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case spinner.TickMsg, cursor.BlinkMsg:
		return true
	}
	if rt := reflect.TypeOf(msg); rt != nil && strings.Contains(rt.PkgPath(), "/cursor") {
		return true // cursor's unexported initialBlinkMsg
	}
	return false
}

// runCmd executes a command but gives up if it doesn't return promptly. The
// messages we care about (query results, graph frames) arrive near-instantly;
// the long timer-based cursor-blink commands we'd discard anyway block for
// ~half a second each, so abandoning them keeps the pump fast.
func runCmd(cmd tea.Cmd) tea.Msg {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg
	case <-time.After(40 * time.Millisecond):
		return nil
	}
}

// collectCmd runs a command (recursing into batches) and enqueues the resulting
// messages, mimicking the Bubble Tea runtime so async command results actually
// flow back into the model.
func collectCmd(cmd tea.Cmd, queue *[]tea.Msg) {
	if cmd == nil {
		return
	}
	switch msg := runCmd(cmd).(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			collectCmd(c, queue)
		}
	case nil:
	default:
		if !skipBackgroundMsg(msg) {
			*queue = append(*queue, msg)
		}
	}
}

// pump feeds messages into the model and keeps processing every follow-up
// command until the model settles, returning the final model.
func pump(t *testing.T, m tea.Model, msgs ...tea.Msg) tea.Model {
	t.Helper()
	queue := append([]tea.Msg(nil), msgs...)
	for i := 0; len(queue) > 0; i++ {
		if i > 1000 {
			t.Fatal("pump did not settle")
		}
		msg := queue[0]
		queue = queue[1:]
		var cmd tea.Cmd
		m, cmd = m.Update(msg)
		collectCmd(cmd, &queue)
	}
	return m
}

// TestQueryFormTabNavigationRunsCheck types into all three check fields,
// advancing with tab, and asserts the form completed and dispatched a check
// carrying every typed value.
func TestQueryFormTabNavigationRunsCheck(t *testing.T) {
	var got struct{ user, relation, object string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/check") {
			var body struct {
				TupleKey struct {
					User     string `json:"user"`
					Relation string `json:"relation"`
					Object   string `json:"object"`
				} `json:"tuple_key"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			got.user = body.TupleKey.User
			got.relation = body.TupleKey.Relation
			got.object = body.TupleKey.Object
			_, _ = w.Write([]byte(`{"allowed":true}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cl, _ := openfga.NewClient(srv.URL)
	a := app.New(log.New(io.Discard), config.New(), "test")
	mdl := newModel(context.Background(), a, cl, "store-1")
	var m tea.Model = mdl
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = m.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-1", Name: "demo"}}})

	m, _ = m.Update(key("5"))     // Query section (default mode: check)
	m, _ = m.Update(key("enter")) // descend into the panel
	m = pump(t, m, key("i"))      // begin editing
	if !m.(Model).editing {
		t.Fatal("expected the query form to be in editing mode")
	}

	for _, r := range "user:anne" {
		m = pump(t, m, key(string(r)))
	}
	m = pump(t, m, key("tab")) // -> relation field
	for _, r := range "viewer" {
		m = pump(t, m, key(string(r)))
	}
	m = pump(t, m, key("tab")) // -> object field
	for _, r := range "document:roadmap" {
		m = pump(t, m, key(string(r)))
	}
	m = pump(t, m, key("enter")) // submit from the last field

	mod := m.(Model)
	if mod.editing {
		t.Error("form should have completed and left editing mode")
	}
	if got.user != "user:anne" || got.relation != "viewer" || got.object != "document:roadmap" {
		t.Errorf("check received user=%q relation=%q object=%q — tab navigation lost field values",
			got.user, got.relation, got.object)
	}
	if !mod.hasResult || !mod.result.ok {
		t.Errorf("expected an allowed check result; hasResult=%v ok=%v", mod.hasResult, mod.result.ok)
	}
}

// TestHistoryCapsAtFive pushes 7 entries and checks the history is capped at
// 5, newest first (the most recently pushed entry lands at index 0, and the
// oldest surviving entry — the third pushed, since the first two are
// evicted — lands at index 4).
func TestHistoryCapsAtFive(t *testing.T) {
	mod := newTestModel().(Model)
	for i := 0; i < 7; i++ {
		mod.pushHistory(histEntry{mode: "check", ok: i%2 == 0, ms: int64(i)})
	}
	if len(mod.history) != 5 {
		t.Fatalf("history len = %d, want 5", len(mod.history))
	}
	if mod.history[0].ms != 6 {
		t.Errorf("history[0].ms = %d, want 6 (newest pushed first)", mod.history[0].ms)
	}
	if mod.history[4].ms != 2 {
		t.Errorf("history[4].ms = %d, want 2 (oldest surviving entry)", mod.history[4].ms)
	}
}

// TestCheckCmdRecordsLatencyAndVals drives checkCmd directly against a slow
// mock server and asserts the returned message carries both the measured
// latency and the three values the query ran with.
func TestCheckCmdRecordsLatencyAndVals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer srv.Close()

	cl, _ := openfga.NewClient(srv.URL)
	cmd := checkCmd(context.Background(), cl, "store-1", "user:anne", "viewer", "document:roadmap")
	msg, ok := cmd().(queryResultMsg)
	if !ok {
		t.Fatal("checkCmd should return a queryResultMsg")
	}
	if msg.ms < 5 {
		t.Errorf("ms = %d, want >= 5 (server slept 5ms)", msg.ms)
	}
	want := [3]string{"user:anne", "viewer", "document:roadmap"}
	if msg.vals != want {
		t.Errorf("vals = %v, want %v", msg.vals, want)
	}
}

// TestVerdictFlashClearsAfterOneTick verifies a badge result sets the
// one-frame flash and schedules its own clear, and that the clear does not
// re-arm — mirroring the fadeMsg precedent in TestSectionFadingTransition.
func TestVerdictFlashClearsAfterOneTick(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5")) // Query
	m, cmd := m.Update(queryResultMsg{lines: []string{"user:anne viewer document:roadmap"}, ok: true, badge: true})
	mod := m.(Model)
	if !mod.flash {
		t.Fatal("a badge result should set flash=true")
	}
	if cmd == nil {
		t.Fatal("a badge result should schedule the flash-clear tick")
	}

	m2, cmd2 := mod.Update(flashMsg{})
	final := m2.(Model)
	if final.flash {
		t.Error("flashMsg should clear flash")
	}
	if cmd2 != nil {
		t.Error("flashMsg should not re-arm")
	}
}

// TestDigitKeyRerunsHistoryEntry drives a full check through the form, then
// presses "1" in the Query section (not editing) and asserts it reruns the
// same query — hitting the server again — rather than switching sections.
func TestDigitKeyRerunsHistoryEntry(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/check") {
			hits++
			_, _ = w.Write([]byte(`{"allowed":true}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cl, _ := openfga.NewClient(srv.URL)
	a := app.New(log.New(io.Discard), config.New(), "test")
	mdl := newModel(context.Background(), a, cl, "store-1")
	var m tea.Model = mdl
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = m.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-1", Name: "demo"}}})

	m, _ = m.Update(key("5"))     // Query section
	m, _ = m.Update(key("enter")) // descend into the panel
	m = pump(t, m, key("i"))
	for _, r := range "user:anne" {
		m = pump(t, m, key(string(r)))
	}
	m = pump(t, m, key("tab"))
	for _, r := range "viewer" {
		m = pump(t, m, key(string(r)))
	}
	m = pump(t, m, key("tab"))
	for _, r := range "document:roadmap" {
		m = pump(t, m, key(string(r)))
	}
	m = pump(t, m, key("enter")) // first run

	mod := m.(Model)
	if len(mod.history) != 1 {
		t.Fatalf("history len after first run = %d, want 1", len(mod.history))
	}
	if hits != 1 {
		t.Fatalf("server hits after first run = %d, want 1", hits)
	}

	m2 := pump(t, mod, key("1")) // rerun history[0]
	mod2 := m2.(Model)
	if hits != 2 {
		t.Errorf("server hits after digit rerun = %d, want 2 (digit should have rerun the check)", hits)
	}
	if mod2.section != secQuery {
		t.Errorf("digit rerun should not change section; got %v", mod2.section)
	}
	if len(mod2.history) != 2 {
		t.Errorf("history len after rerun = %d, want 2", len(mod2.history))
	}
}

// TestQueryDigitWithoutHistoryIsNoop verifies that inside the Query panel a
// digit addressing no history entry is a no-op: strict panel focus never
// switches sections (only the sidebar does).
func TestQueryDigitWithoutHistoryIsNoop(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5"))     // Query, no history yet
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("1"))     // no matching history -> no-op
	mod := m.(Model)
	if mod.section != secQuery {
		t.Errorf("digit with no history in the panel should stay in Query; got %v", mod.section)
	}
}

// TestQueryBodyRendersNonBadgeResultInCard verifies list-objects/list-users
// results (badge=false) still render their title+bullets, under the same
// "Result" section header as badge results.
func TestQueryBodyRendersNonBadgeResultInCard(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5"))     // Query
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("m"))     // cycle to list-objects
	m, _ = m.Update(queryResultMsg{title: "objects", lines: []string{"document:roadmap"}})
	plain := stripANSIView(m.(Model).viewString())
	if !strings.Contains(plain, "objects") || !strings.Contains(plain, "document:roadmap") {
		t.Error("non-badge result should render title+bullets in the query body")
	}
}

// TestGraphSpringScrollSettles drives the spring-scroll animation and verifies
// the viewport reaches the requested offset and the animation flag clears.
func TestGraphSpringScrollSettles(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("2"))     // Model section (graph view)
	m, _ = m.Update(key("enter")) // descend into the panel

	// Make the content taller than the viewport so there is room to scroll.
	mod := m.(Model)
	mod.graphVP.SetContent(strings.Repeat("relation line\n", 200))
	target := mod.graphMaxOffset()
	if target == 0 {
		t.Fatal("expected a scrollable graph viewport")
	}

	// "end" springs the viewport to the bottom; pump runs every animation frame
	// until the spring settles.
	var m2 tea.Model = mod
	m2 = pump(t, m2, key("end"))

	final := m2.(Model)
	if final.graphAnimating {
		t.Error("animation should have settled")
	}
	if final.graphVP.YOffset() != target {
		t.Errorf("YOffset = %d, want %d", final.graphVP.YOffset(), target)
	}
}

func TestEntranceSettlesAndTickerStops(t *testing.T) {
	// newTestModel already fires a WindowSizeMsg during setup, which snaps
	// the entrance to settled — so this constructs via newModel directly to
	// observe the pre-resize boot state.
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := app.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "store-1")
	if !m.entering {
		t.Fatal("model must boot in the entering state")
	}
	var cur tea.Model = m
	var cmd tea.Cmd
	for i := 0; i < 60; i++ { // 60 ticks × 33ms ≈ 2s, far past the ~700ms settle
		cur, cmd = cur.(Model).Update(entranceTickMsg{})
		if !cur.(Model).entering {
			break
		}
	}
	if cur.(Model).entering {
		t.Fatal("entrance must settle")
	}
	if cmd != nil {
		t.Fatal("settled entrance must not re-arm its ticker")
	}
}

// TestBootSizeStartsEntranceThenResizeSnaps is the regression test for the
// entrance never playing: bubbletea delivers the initial tea.WindowSizeMsg
// at startup, before Init() runs, and that same message flips m.ready (which
// gates all rendering). Snapping the entrance unconditionally on it — as the
// handler used to — killed the animation before the first renderable frame
// ever painted. The first size message must leave the entrance running; only
// a later, mid-flight resize should snap it.
func TestBootSizeStartsEntranceThenResizeSnaps(t *testing.T) {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := app.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "store-1")
	var cur tea.Model = m

	// The FIRST WindowSizeMsg is bubbletea's boot-time size report.
	cur, _ = cur.(Model).Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	got := cur.(Model)
	if !got.ready {
		t.Fatal("the first WindowSizeMsg must set ready")
	}
	if !got.entering || got.entranceFrac <= 0 {
		t.Fatalf("the boot-time size report must not snap the entrance: entering=%v entranceFrac=%v", got.entering, got.entranceFrac)
	}

	// Pump a few entrance ticks: still mid-flight, not yet settled.
	for i := 0; i < 3; i++ {
		cur, _ = cur.(Model).Update(entranceTickMsg{})
	}
	if got = cur.(Model); !got.entering {
		t.Fatal("entrance should still be running a few ticks after boot")
	}

	// A SECOND WindowSizeMsg is a genuine mid-flight resize and must snap.
	cur, _ = cur.(Model).Update(tea.WindowSizeMsg{Width: 90, Height: 28})
	if got = cur.(Model); got.entering || got.entranceFrac != 0 {
		t.Fatal("a resize after boot must snap the entrance to settled")
	}
}

func TestDriftAdvancesAndLoops(t *testing.T) {
	m := newTestModel()
	m2, cmd := m.Update(driftTickMsg{})
	if m2.(Model).drift <= 0 {
		t.Fatal("drift phase must advance")
	}
	if cmd == nil {
		t.Fatal("drift is ambient by design: it must re-arm")
	}
	mm := m2.(Model)
	mm.drift = 0.999
	m3, _ := mm.Update(driftTickMsg{})
	if m3.(Model).drift >= 1 {
		t.Fatal("drift phase must wrap below 1")
	}
}

func TestCreateStoreRendersAsOverlay(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("1"))     // Stores
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("n"))     // create form -> overlay
	plain := stripANSIView(m.(Model).viewString())
	if !strings.Contains(plain, "Create Store") {
		t.Error("overlay should show the dialog title")
	}
	if !strings.Contains(plain, "Stores") {
		t.Error("the shell (sidebar nav) should still be visible behind the dialog")
	}
}

func TestCommandPaletteJumpsToSection(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("1")) // Stores
	m, _ = m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if !m.(Model).paletteOpen {
		t.Fatal("ctrl+k should open the command palette")
	}
	if !strings.Contains(stripANSIView(m.(Model).viewString()), "Go to") {
		t.Error("palette overlay should be visible")
	}
	m, _ = m.Update(key("esc"))
	if m.(Model).paletteOpen {
		t.Error("esc should close the palette")
	}
}

// TestCommandPaletteDialogCornersStayOnScreen guards against the palette
// dialog growing taller than the terminal: if its list is sized to the full
// main-pane budget instead of the dialog's own interior budget, the dialog's
// total height (list + hint + title/blank + border) exceeds the terminal
// height and the modal's rounded corners clip off-screen.
func TestCommandPaletteDialogCornersStayOnScreen(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if !m.(Model).paletteOpen {
		t.Fatal("ctrl+k should open the command palette")
	}
	view := m.(Model).viewString()
	hasTop, hasBottom := strings.Contains(view, "╭"), strings.Contains(view, "╰")
	if !hasTop || !hasBottom {
		t.Fatalf("dialog corners must both be on screen: top=%v bottom=%v", hasTop, hasBottom)
	}
}

// stripANSIView strips CSI sequences for assertions.
func stripANSIView(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// TestQueryDashboardHeightCapKeepsStatusBarVisible guards against the query
// dashboard (mode chip + form + result card + history strip) growing taller
// than the available content area on short terminals: renderMain doesn't cap
// its content height, so an over-tall body pushed the status bar off the
// bottom of the frame (clampFrame truncates the tail, not the overflowing
// section). At 100x12 the uncapped body is tall enough to trigger it.
func TestQueryDashboardHeightCapKeepsStatusBarVisible(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m, _ = m.Update(key("5")) // Query

	mod := m.(Model)
	for i := 0; i < 5; i++ {
		mod.pushHistory(histEntry{mode: "check", vals: [3]string{"user:anne", "viewer", "document:roadmap"}, ok: true, ms: int64(i)})
	}
	mod.hasResult = true
	mod.result = queryResultMsg{title: "Check", lines: []string{"user:anne viewer document:roadmap"}, ok: true, badge: true, ms: 12}

	view := stripANSIView(mod.viewString())
	lines := strings.Split(view, "\n")
	if len(lines) != 12 {
		t.Fatalf("frame has %d lines, want 12 (height)", len(lines))
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "q") {
		t.Errorf("status bar keycap missing from the final frame line at height 12: %q", last)
	}
}

func TestQueryBodyShowsModeChipAndResult(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5")) // Query
	m, _ = m.Update(queryResultMsg{title: "Check", lines: []string{"user:anne viewer document:roadmap"}, ok: true, badge: true})
	plain := stripANSIView(m.(Model).viewString())
	if !strings.Contains(plain, "check") {
		t.Error("query body should show the mode chip")
	}
	if !strings.Contains(plain, "ALLOWED") {
		t.Error("query body should show the check result above the input")
	}
}

// TestQueryBodyUsesSectionHeaders verifies the query body's result and
// history blocks sit under flat "Result"/"Recent" header rules instead of
// the old bordered result card (Task 3's de-boxed query body).
func TestQueryBodyUsesSectionHeaders(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5")) // Query
	m, _ = m.Update(queryResultMsg{title: "Check", lines: []string{"user:anne viewer document:roadmap"}, ok: true, badge: true})
	plain := stripANSIView(m.(Model).queryBody())
	if !strings.Contains(plain, "Result ─") {
		t.Fatal("query result must sit under a Result header rule")
	}
	if !strings.Contains(plain, "Recent ─") {
		t.Fatal("history must sit under a Recent header rule")
	}
	if strings.Contains(plain, "╭") {
		t.Fatal("query body must not contain boxes")
	}
}

// TestVerdictFlashGatedOnBadge verifies the verdict flash tint (green/red) is
// only applied to badge results. With correct gating, flash only changes the tint
// when badge=true; flash must not affect non-badge renders. The test compares
// unstripped queryBody() output to detect ANSI color differences.
func TestVerdictFlashGatedOnBadge(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5")) // Query
	mod := m.(Model)

	// Case 1: non-badge result with flash on/off must render identically.
	// With correct gating, flash only applies tint when badge=true, so a non-badge
	// result should ignore flash and produce the same string both ways.
	mod.hasResult = true
	mod.result = queryResultMsg{badge: false, title: "objects", lines: []string{"document:a"}}

	mod.flash = true
	a := mod.queryBody()

	mod.flash = false
	b := mod.queryBody()

	if a != b {
		t.Fatalf("non-badge result must be identical with and without flash\nwith flash=true:\n%s\n\nwith flash=false:\n%s", a, b)
	}

	// Case 2: badge denied result with flash on/off must render differently.
	// With correct gating, flash=true applies red tint to denied badge results,
	// changing the ANSI codes in the Result header, so the strings must differ.
	mod.result = queryResultMsg{badge: true, ok: false, title: "Check", lines: []string{}}

	mod.flash = true
	c := mod.queryBody()

	mod.flash = false
	d := mod.queryBody()

	if c == d {
		t.Fatalf("badge denied result must differ with and without flash; both produced:\n%s", c)
	}
}

// TestGraphViewportScrollOffsetsPreservedOnResize verifies that when the viewport
// is resized, the scroll offset is preserved via SetWidth/SetHeight instead of
// recreating the viewport.
func TestGraphViewportScrollOffsetsPreservedOnResize(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("2")) // Model section (graph view)

	mod := m.(Model)

	// Ensure we have tall enough content to scroll. If the diagram is short,
	// extend it so we can test scrolling.
	if mod.graphVP.TotalLineCount() < 50 {
		currentContent := mod.graphVP.GetContent()
		// Add enough lines to make scrolling meaningful.
		extendedContent := currentContent + strings.Repeat("extension line\n", 100)
		mod.graphVP.SetContent(extendedContent)
	}

	// Scroll to a nonzero offset.
	mod.graphVP.ScrollDown(10)
	originalOffset := mod.graphVP.YOffset()
	if originalOffset == 0 {
		t.Skipf("viewport height >= total lines, cannot test offset preservation")
	}

	// Resize the terminal. The resize should use SetWidth/SetHeight to preserve
	// the scroll offset, not recreate the viewport.
	var m2 tea.Model = mod
	m2, _ = m2.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	// Verify the scroll offset is preserved.
	resized := m2.(Model)
	if resized.graphVP.YOffset() != originalOffset {
		t.Errorf("YOffset after resize = %d, want %d (width check should have taken SetWidth/SetHeight path)",
			resized.graphVP.YOffset(), originalOffset)
	}
}

// TestSectionFadingTransition verifies that switching sections via key press
// sets the fading flag and that the fadeMsg clears it, without re-arming.
func TestSectionFadingTransition(t *testing.T) {
	m := newTestModel()

	// Start at Stores section
	m, _ = m.Update(key("1"))

	// Press tab to switch sections; fading should be set.
	m, cmd := m.Update(key("tab"))
	mod := m.(Model)
	if !mod.fading {
		t.Fatal("section switch should set fading=true")
	}
	if mod.section != secModel {
		t.Errorf("section should be secModel after tab; got %v", mod.section)
	}

	// Verify that a command was returned (the fade ticker).
	if cmd == nil {
		t.Fatal("section switch should return a command (fade ticker)")
	}

	// Send a direct fadeMsg; fading should clear and no command should be returned.
	m, cmd = mod.Update(fadeMsg{})
	final := m.(Model)
	if final.fading {
		t.Error("fadeMsg should clear fading flag")
	}
	if cmd != nil {
		t.Error("fadeMsg should return no command (ticker does not re-arm)")
	}
}

// TestQueryHistoryRecordsMessageMode verifies that the query history entry
// records the mode from the completed message, not the live UI state. This
// regression test catches mode-mismatch bugs where pressing "m" (mode cycle)
// while a query is in flight causes the history entry to record the wrong mode.
func TestQueryHistoryRecordsMessageMode(t *testing.T) {
	m := newTestModel()
	mod := m.(Model)

	// Set the model's qmode to 1 (list-objects).
	mod.qmode = 1

	// Deliver a check result (mode "check") while qmode is 1 (list-objects).
	// The history entry should record mode: "check" from the message, not the live qmode.
	m, _ = mod.Update(queryResultMsg{
		badge: true,
		ok:    true,
		mode:  "check",
		vals:  [3]string{"user:anne", "viewer", "document:roadmap"},
		ms:    42,
	})

	final := m.(Model)
	if len(final.history) != 1 {
		t.Fatalf("history len = %d, want 1", len(final.history))
	}
	if final.history[0].mode != "check" {
		t.Errorf("history[0].mode = %q, want \"check\" (should come from message, not live qmode %d)", final.history[0].mode, final.qmode)
	}
	if final.history[0].ok != true {
		t.Errorf("history[0].ok = %v, want true", final.history[0].ok)
	}
	if final.history[0].ms != 42 {
		t.Errorf("history[0].ms = %d, want 42", final.history[0].ms)
	}
}

// TestMasterDetailSplitsWidth verifies masterDetail joins the list and the
// preview card into a single row that fills the requested width.
func TestMasterDetailSplitsWidth(t *testing.T) {
	out := ansi.Strip(masterDetail("L", "Title", "R", 100, 10))
	first := strings.Split(out, "\n")[0]
	if lipgloss.Width(first) < 90 {
		t.Fatalf("master-detail should fill width, got %d", lipgloss.Width(first))
	}
}

// TestMasterDetailRealListContentFits verifies that a real list rendered at
// the split's list-pane width (splitListWidth) fits the box masterDetail
// wraps it in. If the list were instead sized to the full section width (as
// resize() used to do), its rows would already be padded to that full
// width; masterDetail's narrower Width(lw) box would then word-wrap those
// over-wide rows, mangling titles mid-word and pushing the total line count
// past the requested height (lipgloss's Height() pads short content but
// does not truncate content that's already too tall).
func TestMasterDetailRealListContentFits(t *testing.T) {
	l := uilist.New()
	l.SetItems([]uilist.Item{
		{TitleText: "alpha", DescText: "first store"},
		{TitleText: "a very long store name that just keeps going and going", DescText: "second store"},
		{TitleText: "gamma", DescText: "third store"},
	})
	l.SetSize(splitListWidth(100), 10)

	out := ansi.Strip(masterDetail(l.View(), "Title", "PREVIEW", 100, 10))
	lines := strings.Split(out, "\n")
	if len(lines) > 10 {
		t.Fatalf("masterDetail output has %d lines, want <= 10 (height budget overflow): %d", len(lines), len(lines))
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > 100 {
			t.Errorf("line %d width = %d, want <= 100: %q", i, w, line)
		}
	}
}

// TestEmptyStateIsInline verifies an empty stores section renders its hint
// inline under the section header rather than centered with lipgloss.Place
// (which padded the hint with many blank rows).
func TestEmptyStateIsInline(t *testing.T) {
	mod := newTestModel().(Model)
	mod.stores = nil
	body := stripANSIView(mod.sectionBody())
	if strings.Contains(body, "\n\n\n\n\n\n\n\n") {
		t.Fatal("empty state must be inline under the header, not centered")
	}
	if !strings.Contains(body, "No stores yet") {
		t.Fatal("empty state copy missing")
	}
}

// TestArrowKeysSwitchSections verifies that plain left/right arrows cycle
// sections the same way tab/shift+tab do, wrapping at both ends.
func TestArrowKeysSwitchSections(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("right"))
	if got := m.(Model).section; got != secModel {
		t.Fatalf("right arrow: section = %v, want secModel", got)
	}
	m, _ = m.Update(key("left"))
	if got := m.(Model).section; got != secStores {
		t.Fatalf("left arrow: section = %v, want secStores", got)
	}
	// wraps backward
	m, _ = m.Update(key("left"))
	if got := m.(Model).section; got != secAssertions {
		t.Fatalf("left arrow wrap: section = %v, want secAssertions", got)
	}
}

// TestArrowKeysSwitchSectionsFromModel verifies left/right arrows cycle
// sections from a section other than Stores too, matching
// TestArrowKeysSwitchSections's coverage of the default start section.
func TestArrowKeysSwitchSectionsFromModel(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("2")) // Model
	if got := m.(Model).section; got != secModel {
		t.Fatalf("digit 2: section = %v, want secModel", got)
	}
	m, _ = m.Update(key("right"))
	if got := m.(Model).section; got != secTuples {
		t.Fatalf("right arrow from secModel: section = %v, want secTuples", got)
	}
}

// TestArrowsDoNotSwitchSectionsDuringTakeoverForm verifies that left/right
// arrows are captured by an open takeover form (create-store) instead of
// switching sections, mirroring TestArrowsStayCursorMovementWhileEditing's
// coverage of the query form.
func TestArrowsDoNotSwitchSectionsDuringTakeoverForm(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("1"))     // Stores
	m, _ = m.Update(key("enter")) // descend into the panel
	m, _ = m.Update(key("n"))     // create store form -> takeover
	m2, _ := m.Update(key("left"))
	if m2.(Model).section != secStores {
		t.Fatal("left arrow while a takeover form is open must not switch sections")
	}
}

// TestArrowsStayCursorMovementWhileEditing verifies that once the query form
// is in editing mode, left/right move the field cursor instead of switching
// sections (the query-form guard in handleKey returns before the global
// switch that owns arrow-key section navigation).
func TestArrowsStayCursorMovementWhileEditing(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5"))     // Query section
	m, _ = m.Update(key("enter")) // descend into the panel
	m = pump(t, m, key("i"))      // begin editing
	if !m.(Model).editing {
		t.Fatal("expected the query form to be in editing mode")
	}
	m2, _ := m.Update(key("left"))
	if m2.(Model).section != secQuery {
		t.Fatal("left arrow while editing must not switch sections")
	}
}

// TestShiftArrowsPanModelGraph verifies that shift+down scrolls the Model
// graph viewport rather than switching sections. The scroll is spring
// animated, so the change is only observable after pumping graph ticks
// (see TestGraphSpringScrollSettles for the same pattern).
func TestShiftArrowsPanModelGraph(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("2"))     // Model section (graph view)
	m, _ = m.Update(key("enter")) // descend into the panel

	mod := m.(Model)
	mod.graphVP.SetContent(strings.Repeat("relation line\n", 200))
	before := mod.graphVP.YOffset()

	var m2 tea.Model = mod
	m2 = pump(t, m2, key("shift+down"))

	final := m2.(Model)
	if got := final.graphVP.YOffset(); got == before {
		t.Fatal("shift+down must pan the graph")
	}
}
