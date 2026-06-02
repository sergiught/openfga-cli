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

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
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

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func newTestModel() tea.Model {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := app.New(log.New(io.Discard), config.New(), "test")
	var m tea.Model = newModel(context.Background(), a, cl, "store-1")
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
	if strings.TrimSpace(m.View()) == "" {
		t.Fatalf("empty view: %s", ctx)
	}
}

func TestSections(t *testing.T) {
	m := newTestModel()
	for _, k := range []string{"1", "2", "3", "4", "5", "6", "7"} {
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
	m, _ = m.Update(key("1"))      // Stores
	m, _ = m.Update(key("down"))   // move to store-2
	m, _ = m.Update(key("enter"))  // select
	render(t, m, "store selected")

	m, _ = m.Update(key("2"))      // Model
	m, _ = m.Update(key("m"))      // open model picker
	render(t, m, "model picking")
	m, _ = m.Update(key("esc"))    // cancel
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
// messages we care about (huh navigation, query results, graph frames) arrive
// near-instantly; the long timer-based cursor-blink commands we'd discard anyway
// block for ~half a second each, so abandoning them keeps the pump fast.
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
// messages, mimicking the Bubble Tea runtime so huh's internal navigation
// messages (nextFieldMsg, nextGroupMsg, …) actually flow back into the model.
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

// TestQueryFormTabNavigationRunsCheck is the regression test for the dropped
// huh navigation messages: it types into all three check fields, advancing with
// tab, and asserts the form completed and dispatched a check carrying every
// typed value. Before the fix, tab never moved focus so the form never filled
// or completed.
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
	var m tea.Model = newModel(context.Background(), a, cl, "store-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = m.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-1", Name: "demo"}}})

	m, _ = m.Update(key("5"))  // Query section (default mode: check)
	m = pump(t, m, key("i"))   // begin editing
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
	if final.graphVP.YOffset != target {
		t.Errorf("YOffset = %d, want %d", final.graphVP.YOffset, target)
	}
}

// TestSettingsPreview moves the theme cursor (live preview) but does NOT press
// enter, so it never writes to the user's real config on disk.
func TestSettingsPreview(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("7"))
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key("down"))
	render(t, m, "settings preview")
}

func TestSplashShownThenDismissed(t *testing.T) {
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := app.New(log.New(io.Discard), config.New(), "test")
	var m tea.Model = newModel(context.Background(), a, cl, "store-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})

	if !strings.Contains(m.View(), "playground") {
		t.Error("splash should be visible before stores load")
	}
	if !m.(Model).splash {
		t.Fatal("model should start on the splash")
	}
	m, _ = m.Update(storesLoadedMsg{stores: []openfga.Store{{ID: "store-1", Name: "demo"}}})
	if m.(Model).splash {
		t.Error("splash should dismiss once stores load")
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

func TestCreateStoreRendersAsOverlay(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("1")) // Stores
	m, _ = m.Update(key("n")) // create form -> overlay
	plain := stripANSIView(m.View())
	if !strings.Contains(plain, "Create Store") {
		t.Error("overlay should show the dialog title")
	}
	if !strings.Contains(plain, "Stores") {
		t.Error("the shell (sidebar nav) should still be visible behind the dialog")
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
