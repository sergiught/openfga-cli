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
	"github.com/charmbracelet/log"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/app"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/fga"
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
	mdl.splash = false // tests exercise the shell, not the splash
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

func TestStoresSelectAndModelSwitch(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("1"))     // Stores
	m, _ = m.Update(key("down"))  // move to store-2
	m, _ = m.Update(key("enter")) // select
	render(t, m, "store selected")

	m, _ = m.Update(key("2")) // Model
	m, _ = m.Update(key("m")) // open model picker
	render(t, m, "model picking")
	m, _ = m.Update(key("esc")) // cancel
	render(t, m, "model picker cancelled")
}

func TestFiltering(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("3")) // Tuples
	m, _ = m.Update(key("/")) // start filter
	for _, r := range "anne" {
		m, _ = m.Update(key(string(r)))
	}
	render(t, m, "filtering tuples")
	m, _ = m.Update(key("esc")) // clear filter
	render(t, m, "filter cleared")
}

func TestQueryForm(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5")) // Query
	m, _ = m.Update(key("m")) // cycle mode -> list-objects
	m, _ = m.Update(key("i")) // edit
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
	m, _ = m.Update(key("1")) // Stores
	m, _ = m.Update(key("n")) // create form
	render(t, m, "create store form")
	for _, r := range "newstore" {
		m, _ = m.Update(key(string(r)))
	}
	m, _ = m.Update(key("esc")) // cancel
	render(t, m, "create store cancelled")
}

func TestWriteTupleForm(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("3")) // Tuples
	m, _ = m.Update(key("a")) // write form
	render(t, m, "write tuple form")
	m, _ = m.Update(key("esc"))
	render(t, m, "write tuple cancelled")
}

func TestAssertionsRun(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("6"))
	m, _ = m.Update(assertTestMsg{
		results: []assertResult{{label: "user:anne viewer document:roadmap", expected: true, got: true, pass: true}},
		passed:  1, total: 1,
	})
	render(t, m, "assertions results")
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
	mdl.splash = false
	var m tea.Model = mdl
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = m.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-1", Name: "demo"}}})

	m, _ = m.Update(key("5")) // Query section (default mode: check)
	m = pump(t, m, key("i"))  // begin editing
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
	mdl.splash = false
	var m tea.Model = mdl
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = m.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-1", Name: "demo"}}})

	m, _ = m.Update(key("5")) // Query section
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

// TestDigitKeyFallsThroughToSectionSwitchWithoutHistory verifies the digit
// precedence resolution the other way: with no matching history entry, "1"
// in the Query section falls through to the normal section switch.
func TestDigitKeyFallsThroughToSectionSwitchWithoutHistory(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5")) // Query, no history yet
	m, _ = m.Update(key("1"))
	mod := m.(Model)
	if mod.section != secStores {
		t.Errorf("digit with no matching history entry should switch sections; got %v", mod.section)
	}
}

// TestQueryBodyRendersNonBadgeResultInCard verifies list-objects/list-users
// results (badge=false) still render their title+bullets, now inside the
// result card frame alongside badge results.
func TestQueryBodyRendersNonBadgeResultInCard(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("5")) // Query
	m, _ = m.Update(key("m")) // cycle to list-objects
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
	m, _ = m.Update(key("2")) // Model section (graph view)

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

func TestSplashShownThenDismissed(t *testing.T) {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := app.New(log.New(io.Discard), config.New(), "test")
	var m tea.Model = newModel(context.Background(), a, cl, "store-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})

	if !strings.Contains(m.(Model).viewString(), "playground") {
		t.Error("splash should be visible before stores load")
	}
	if !m.(Model).splash {
		t.Fatal("model should start on the splash")
	}
	m, _ = m.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-1", Name: "demo"}}})
	if !m.(Model).splash {
		t.Error("splash should persist through data load (it dismisses only on keypress)")
	}
	m, _ = m.Update(key("enter"))
	if m.(Model).splash {
		t.Error("splash should dismiss on keypress")
	}
}

func TestSplashDismissedByKeypress(t *testing.T) {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := app.New(log.New(io.Discard), config.New(), "test")
	var m tea.Model = newModel(context.Background(), a, cl, "store-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	if !m.(Model).splash {
		t.Fatal("should start on splash")
	}
	m, _ = m.Update(key("enter")) // any non-quit key dismisses
	if m.(Model).splash {
		t.Error("a keypress should dismiss the splash")
	}
}

func TestSplashTickStopsAfterDismissal(t *testing.T) {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := app.New(log.New(io.Discard), config.New(), "test")
	var m tea.Model = newModel(context.Background(), a, cl, "store-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = m.Update(key("enter")) // dismiss splash
	if m.(Model).splash {
		t.Fatal("splash should be dismissed")
	}
	_, cmd := m.Update(splashTickMsg{})
	if cmd != nil {
		t.Error("splashTickMsg after dismissal should not re-arm the ticker")
	}
}

func TestSplashTickStopsWhenAnimationCompletes(t *testing.T) {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := app.New(log.New(io.Discard), config.New(), "test")
	var m tea.Model = newModel(context.Background(), a, cl, "store-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})

	var cmd tea.Cmd
	for i := 0; i < 40; i++ { // 40 * 0.04 = 1.6, comfortably past the 1.3 cutoff
		m, cmd = m.Update(splashTickMsg{})
	}
	if got := m.(Model).splashPhase; got < 1.3 {
		t.Fatalf("splashPhase = %v, want >= 1.3 after enough ticks", got)
	}
	if cmd != nil {
		t.Error("ticker should stop re-arming once splashPhase >= 1.3")
	}
}

func TestCreateStoreRendersAsOverlay(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("1")) // Stores
	m, _ = m.Update(key("n")) // create form -> overlay
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
