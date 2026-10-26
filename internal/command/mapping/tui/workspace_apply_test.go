package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// addedTwice is twoRules with a second test of the first one's event.
const addedTwice = twoRules + `  - name: added again
    input: {type: organization.member.added, data: {object: {organization: {id: o1}, user: {user_id: u1}}}}
    expect_tuples:
      - user: "user:u1"
        relation: member
        object: "organization:o1"
        action: write
`

// strayEvent is one no rule matches, so a sample of it makes no test.
func strayEvent() map[string]any { return map[string]any{"type": "organization.updated"} }

// applyBody is the Apply pane as it is drawn, with its wrapping undone.
func (h *wsHarness) applyBody() string {
	mw, mh := h.w.mainSize()
	return flat(plain(h.w.paneBody(paneApply, mw, mh)))
}

// eventLabels is every row the Apply pane lists.
func (h *wsHarness) eventLabels() []string { return h.w.apply.labels() }

func TestApplyIsTheFourthPane(t *testing.T) {
	t.Cleanup(func() { icons.Apply(icons.ModeNerdFont) })
	icons.Apply(icons.ModeUnicode)
	h := applyWs(t, twoRules, &fakeStore{})
	if v := h.view(); !regexp.MustCompile(`\[4\].*` + icons.I().Change + ` Apply\s+0\b`).MatchString(v) {
		t.Fatalf("no Apply in the sidebar:\n%s", v)
	}
	h.press("4")
	if h.w.active != paneApply || h.w.focus != shell.FocusPanel {
		t.Fatalf("4: active = %d, focus = %d", h.w.active, h.w.focus)
	}
	if !strings.Contains(h.view(), "test added") {
		t.Fatalf("the pane does not list the events:\n%s", h.view())
	}
	for _, step := range []struct {
		key  string
		want pane
	}{{"tab", paneRules}, {"shift+tab", paneApply}, {"shift+tab", paneTests}, {"tab", paneApply}} {
		h.press(step.key)
		if h.w.active != step.want {
			t.Fatalf("%s: active = %d, want %d", step.key, h.w.active, step.want)
		}
	}
	// The sidebar walks through it too.
	h.press("esc", "down")
	if h.w.active != paneRules || h.w.focus != shell.FocusSidebar {
		t.Fatalf("down from Apply in the sidebar: active = %d, focus = %d", h.w.active, h.w.focus)
	}
	h.press("up")
	if h.w.active != paneApply {
		t.Fatalf("up from Rules in the sidebar: active = %d", h.w.active)
	}
	// The badge counts what is selected.
	h.press("enter", "space")
	if v := h.view(); !regexp.MustCompile(`\[4\].*Apply\s+1\b`).MatchString(v) {
		t.Fatalf("the badge does not count the selection:\n%s", v)
	}
	var help string
	for _, k := range globalKeys {
		help += k[0] + " " + k[1] + "\n"
	}
	if !strings.Contains(help, "1 2 3 4") || !strings.Contains(help, "Apply") {
		t.Fatalf("the help does not name 4:\n%s", help)
	}
}

func TestApplyWithoutAStoreSaysHowToSetOne(t *testing.T) {
	h := loadedWs(t, twoRules)
	if v := h.view(); !regexp.MustCompile(`\[4\].*Apply\s+–`).MatchString(v) {
		t.Fatalf("the badge is not a dash:\n%s", v)
	}
	h.press("4")
	want := "No store is set. Apply writes to the active profile's store: set one with ofga profiles " +
		"set store_id <id>, or start ofga mappings with --store-id."
	if got := h.applyBody(); got != flat(want) {
		t.Fatalf("body = %q, want %q", got, flat(want))
	}
	h.press("v", "o")
	if d := h.w.stacks[paneApply].Depth(); d != 1 || len(h.w.apply.events) != 0 {
		t.Fatalf("a pane with no store took keys: depth %d, events %v", d, h.eventLabels())
	}
	if keys := h.w.keys(); slices.Contains(keys, "v paste") {
		t.Fatalf("keys = %v", keys)
	}
}

func TestApplyWithAnUnusableStoreSaysWhy(t *testing.T) {
	h := loadedWsWith(t, twoRules, Options{
		Store:    "profile default, store my-store",
		ApplyErr: "store ID \"my-store\x1b]0;pwned\x07\" (from profile) is not valid: it must be a ULID",
	})
	if v := h.view(); !regexp.MustCompile(`\[4\].*Apply\s+–`).MatchString(v) {
		t.Fatalf("the badge is not a dash:\n%s", v)
	}
	h.press("4")
	want := `The store cannot be used: store ID "my-store" (from profile) is not valid: it must be a ULID`
	if got := h.applyBody(); got != flat(want) {
		t.Fatalf("body = %q, want %q", got, flat(want))
	}
	if v := h.w.View(); strings.Contains(v, "\x1b]") || strings.Contains(v, "\x07") {
		t.Fatalf("the reason reaches the terminal raw: %q", v)
	}
	h.press("v", "o")
	if d := h.w.stacks[paneApply].Depth(); d != 1 || len(h.w.apply.events) != 0 {
		t.Fatalf("a pane with an unusable store took keys: depth %d, events %v", d, h.eventLabels())
	}
	// A file that does not load still comes first.
	b := brokenWs(t)
	b.w.opts.ApplyErr = "store ID \"my-store\" is not valid"
	b.press("4")
	if got := b.applyBody(); got != flat(applyMustLoad) {
		t.Fatalf("body = %q, want %q", got, flat(applyMustLoad))
	}
}

func TestApplyNeedsASavedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapping.yaml")
	h := newWsHarness(t, opened{path: path, doc: recipeDoc(t, "organization.member.added")},
		Options{Apply: applyTarget(&fakeStore{})})
	h.press("4")
	if got, want := h.applyBody(), flat("Apply uses the saved file. Save it first with ctrl+s."); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	h.press("v")
	if d := h.w.stacks[paneApply].Depth(); d != 1 {
		t.Fatalf("an unsaved file's pane opened the paste box: depth %d", d)
	}
	h.press("ctrl+s")
	if h.w.fresh {
		t.Fatalf("not saved: %s", h.toastText())
	}
	body := h.applyBody()
	if !strings.Contains(body, flat("store "+applyStoreID+" · profile default")) || len(h.w.apply.events) == 0 {
		t.Fatalf("after the save the pane shows %q, events %v", body, h.eventLabels())
	}
}

func TestApplyWithUnsavedEditsOffersSave(t *testing.T) {
	h := applyWs(t, twoRules, &fakeStore{})
	h.rename(1, "removed")
	h.press("4")
	note := flat("Apply uses the saved file, and you have unsaved edits. ctrl+s saves them.")
	body := h.applyBody()
	for _, want := range []string{flat("store " + applyStoreID + " · profile default"), "testadded", note} {
		if !strings.Contains(body, want) {
			t.Fatalf("body lacks %q:\n%s", want, body)
		}
	}
	h.press("p")
	h.run()
	if got := h.toastText(); !strings.Contains(got, note) {
		t.Fatalf("p on unsaved edits toasts %q", got)
	}
	h.press("ctrl+s")
	if h.w.dirty || strings.Contains(h.applyBody(), note) {
		t.Fatalf("after the save: dirty = %v, body = %s", h.w.dirty, h.applyBody())
	}
}

func TestApplyOnABrokenFileSaysItMustLoad(t *testing.T) {
	t.Setenv("VISUAL", "true")
	h := brokenWs(t)
	h.w.opts.Apply = applyTarget(&fakeStore{})
	h.press("4")
	if h.w.active != paneApply {
		t.Fatalf("4 on a broken file: active = %d", h.w.active)
	}
	if got, want := h.applyBody(), flat("Apply needs the file to load first. e opens it in your editor."); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	h.press("v", "/")
	if st := h.w.stacks[paneApply]; st.Depth() != 1 || st.Typing() {
		t.Fatalf("the hidden Apply pane took keys: depth %d, typing %v", st.Depth(), st.Typing())
	}
	if keys := h.w.keys(); !slices.Equal(keys, []string{"e editor", "? help"}) {
		t.Fatalf("keys = %v", keys)
	}
	if cmd := h.w.key(wsKey("e")); cmd == nil {
		t.Fatal("e did not open the editor")
	}
}

func TestApplyListsTestInputsAndSamplesOnce(t *testing.T) {
	h := applyWs(t, addedTwice, &fakeStore{})
	// A sample picked this session that makes no test is an event of its own.
	h.w.doc.Rules[1].Sample = &mapping.Sample{Label: "organization.updated", Event: strayEvent()}
	h.press("4")
	want := []string{"test added", "rule member-removed sample"}
	if got := h.eventLabels(); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	body := h.applyBody()
	for _, w := range []string{"testadded", "rulemember-removedsample", "0of2eventsselected"} {
		if !strings.Contains(body, w) {
			t.Fatalf("body lacks %q:\n%s", w, body)
		}
	}
	if strings.Contains(body, "addedagain") {
		t.Fatalf("a repeated test input is listed twice:\n%s", body)
	}
}

func TestReadOnlyFilesListEvents(t *testing.T) {
	src := anchored + "tests:\n  - name: t\n    input: {type: team.joined}\n"
	h := applyWs(t, src, &fakeStore{})
	if !h.w.sess.ReadOnly {
		t.Fatal("fixture: the file is editable")
	}
	h.press("4")
	if got := h.eventLabels(); !slices.Equal(got, []string{"test t"}) {
		t.Fatalf("events = %v", got)
	}
	h.press("space", "v")
	h.send(tea.PasteMsg{Content: `{"type": "a"}`})
	h.press("ctrl+d")
	if got := h.w.apply.selected(); got != 2 {
		t.Fatalf("selected %d of %v, want both", got, h.eventLabels())
	}
	if v := h.view(); !strings.Contains(v, "Read-only:") || !strings.Contains(v, "2 of 2 events selected") {
		t.Fatalf("view:\n%s", v)
	}
}

func TestTheStoreNameArrivesLater(t *testing.T) {
	h := applyWs(t, twoRules, &fakeStore{})
	calls := 0
	h.w.opts.Apply.Name = func(context.Context) string {
		calls++
		return "acme\x1b]0;pwned\x07"
	}
	h.press("2", "3")
	h.run()
	if calls != 0 {
		t.Fatal("the store name was looked up before Apply was shown")
	}
	h.press("4")
	if calls != 0 {
		t.Fatal("the store name was looked up on the key's goroutine")
	}
	if got := h.applyBody(); !strings.Contains(got, flat("store "+applyStoreID+" · profile default")) {
		t.Fatalf("before the name: %q", got)
	}
	h.run()
	if calls != 1 {
		t.Fatalf("the name was looked up %d times", calls)
	}
	if got := h.applyBody(); !strings.Contains(got, flat("store acme ("+applyStoreID+") · profile default")) {
		t.Fatalf("after the name: %q", got)
	}
	if v := h.w.View(); strings.Contains(v, "\x1b]") || strings.Contains(v, "\x07") {
		t.Fatalf("the store name reaches the terminal raw: %q", v)
	}
	h.press("1", "4")
	h.run()
	if calls != 1 {
		t.Fatalf("showing Apply again looked the name up again: %d calls", calls)
	}
}

func TestEventsCannotDriveTheTerminal(t *testing.T) {
	const hostile = "x\x1b]0;pwned\x07\u009b31m\u202eevil"
	h := applyWs(t, twoRules, &fakeStore{})
	h.w.doc.Tests[0].Name = hostile
	h.press("4")
	if got := h.eventLabels(); len(got) != 1 || got[0] != "test "+hostile {
		t.Fatalf("fixture: events = %q", got)
	}
	h.press("v")
	h.send(tea.PasteMsg{Content: `{"type": "y\u001b]0;pwned\u0007\u009b31m\u202eevil"}`})
	h.press("ctrl+d")
	if len(h.w.apply.events) != 2 {
		t.Fatalf("fixture: the paste added %q", h.eventLabels())
	}
	v := h.w.View()
	for _, bad := range []string{"\x1b]", "\x07", "\u009b", "\u202e"} {
		if strings.Contains(v, bad) {
			t.Fatalf("a row label reaches the terminal with %q:\n%q", bad, v)
		}
	}
	if shown := style.SanitizeTerminal(hostile); !strings.Contains(plain(v), shown) {
		t.Fatalf("the label is not shown as %q:\n%s", shown, plain(v))
	}
}

// teamRules adds a member to a team one event at a time, or sets a team's
// whole roster, which reads the store first.
const teamRules = `version: "1"
rules:
  - name: added
    when: input.type == "added"
    tuples:
      - user: "user:{{ input.user }}"
        relation: member
        object: "team:{{ input.team }}"
  - name: roster
    when: input.type == "roster"
    tuple_filters:
      - relation: member
        object: "team:{{ input.team }}"
    iterator:
      source: input.users
      as: u
      tuples:
        - user: "user:{{ u }}"
          relation: member
          object: "team:{{ input.team }}"
`

// added is n events, each adding a member to team a: one write apiece.
func added(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "{\"type\": \"added\", \"user\": \"%d\", \"team\": \"a\"}\n", i)
	}
	return b.String()
}

// member is user u in team a, as the store keeps it.
func member(u string) language.Tuple {
	return language.Tuple{User: "user:" + u, Relation: "member", Object: "team:a"}
}

// teamWs opens teamRules on store, shows Apply and pastes events into it.
func teamWs(t *testing.T, store *fakeStore, events string) *wsHarness {
	t.Helper()
	h := applyWs(t, teamRules, store)
	h.press("4")
	h.pasteEvents(events)
	return h
}

// pasteEvents pastes events into the Apply pane.
func (h *wsHarness) pasteEvents(events string) {
	h.t.Helper()
	h.press("v")
	h.send(tea.PasteMsg{Content: events})
	h.press("ctrl+d")
	if _, ok := h.topOf(paneApply).(*applyView); !ok {
		h.t.Fatalf("the paste did not close: %s", h.toastText())
	}
}

// loadEvents loads the events in path into the Apply pane, typing the path.
func (h *wsHarness) loadEvents(path string) {
	h.t.Helper()
	h.press("o", "ctrl+p")
	h.typeText(path)
	h.press("enter")
	if _, ok := h.topOf(paneApply).(*applyView); !ok {
		h.t.Fatalf("the load did not close: %s", h.toastText())
	}
}

// settleApply runs commands until no plan or apply is in flight.
func (h *wsHarness) settleApply() {
	h.t.Helper()
	for range 20 {
		h.run()
		if h.w.applyCancel == nil {
			return
		}
	}
	h.t.Fatal("the plan or the apply never finished")
}

// planned presses p and returns the plan once it is on screen.
func (h *wsHarness) planned() *mapping.Plan {
	h.t.Helper()
	h.press("p")
	h.settleApply()
	if h.w.apply.plan == nil {
		h.t.Fatalf("no plan: %s / %s", h.applyBody(), h.toastText())
	}
	return h.w.apply.plan.plan
}

// confirming is the question open over everything, or nil.
func (h *wsHarness) confirming() *confirm {
	if h.w.overlay == nil {
		return nil
	}
	c, _ := h.w.overlay.top().(*confirm)
	return c
}

// confirmApply presses a, checks the question opened, and answers y.
func (h *wsHarness) confirmApply() {
	h.t.Helper()
	h.press("a")
	h.run()
	if h.confirming() == nil {
		h.t.Fatalf("a opened no question: %s / %s", h.applyBody(), h.toastText())
	}
	h.press("y")
}

// runAside runs the queued commands on a goroutine of their own, for a test
// that acts while one of them waits on a held write. The returned wait feeds
// their messages to the workspace once they have all returned.
func (h *wsHarness) runAside() (wait func()) {
	cmds := h.cmds
	h.cmds = nil
	done := make(chan []tea.Msg, 1)
	go func() { done <- collect(context.Background(), cmds) }()
	return func() {
		h.t.Helper()
		select {
		case msgs := <-done:
			for _, m := range msgs {
				h.send(m)
			}
		case <-time.After(10 * time.Second):
			h.t.Fatal("the apply never returned")
		}
	}
}

// planWrites is the requests Execute sends for p, whose events are atomic.
func planWrites(p *mapping.Plan) []fakeWrite {
	var out []fakeWrite
	for _, ep := range p.Events {
		if len(ep.Changes) == 0 {
			continue
		}
		var w fakeWrite
		for _, c := range ep.Changes {
			if c.Action == language.ActionDelete {
				w.deletes = append(w.deletes, c)
			} else {
				w.writes = append(w.writes, c)
			}
		}
		out = append(out, w)
	}
	return out
}

// sameWrites compares requests by what they carry.
func sameWrites(a, b []fakeWrite) bool {
	return slices.EqualFunc(a, b, func(x, y fakeWrite) bool {
		return reflect.DeepEqual(x.writes, y.writes) && reflect.DeepEqual(x.deletes, y.deletes)
	})
}

// applyBadgeIs fails unless the sidebar shows Apply's badge as want.
func (h *wsHarness) applyBadgeIs(want string) {
	h.t.Helper()
	if v := h.view(); !regexp.MustCompile(`Apply\s+` + regexp.QuoteMeta(want) + `(\s|$)`).MatchString(v) {
		h.t.Fatalf("the Apply badge is not %q:\n%s", want, v)
	}
}

func TestNoWriteBeforeConfirm(t *testing.T) {
	store := &fakeStore{}
	h := applyWs(t, teamRules, store)
	h.w.opts.Apply.Name = func(context.Context) string { return "acme\x1b]0;pwned\x07" }
	h.press("4")
	h.pasteEvents(added(1) + `{"type": "added", "user": "2\u001b]0;pwned\u0007\u202eevil", "team": "a"}`)
	shown := h.planned()
	if store.written() != 0 {
		t.Fatalf("planning wrote %d requests", store.written())
	}
	body := h.applyBody()
	for _, want := range []string{"event 1 · added", "+ user:1 member team:a", "event 2 · added",
		"+ user:2evil member team:a", mapping.PlanSummary(shown)} {
		if !strings.Contains(body, flat(want)) {
			t.Fatalf("the plan lacks %q:\n%s", want, body)
		}
	}
	if v := h.w.View(); strings.Contains(v, "\x1b]") || strings.Contains(v, "\x07") || strings.Contains(v, "\u202e") {
		t.Fatalf("an event value reaches the terminal raw: %q", v)
	}
	h.applyBadgeIs("+2 −0")

	h.press("a")
	h.run()
	c := h.confirming()
	if c == nil {
		t.Fatalf("a opened no question: %s", h.toastText())
	}
	if want := "Apply 2 writes to store acme (" + applyStoreID + ")?"; c.question != want {
		t.Fatalf("question = %q, want %q", c.question, want)
	}
	if keys := c.Keys(); !slices.Equal(keys, []string{"y apply", "n / esc / enter cancel"}) {
		t.Fatalf("keys = %v", keys)
	}
	h.run()
	if store.written() != 0 {
		t.Fatalf("the question wrote %d requests before an answer", store.written())
	}
}

func TestEnterEscAndNDoNotConfirm(t *testing.T) {
	store := &fakeStore{}
	h := teamWs(t, store, added(2))
	h.planned()
	for _, k := range []string{"enter", "esc", "n", "x"} {
		h.press("a")
		h.run()
		if h.confirming() == nil {
			t.Fatalf("%s: a opened no question", k)
		}
		h.press(k)
		h.run()
		if k == "x" {
			// Any other key leaves the question open: only y answers yes.
			if h.confirming() == nil {
				t.Fatal("x closed the question")
			}
			h.press("esc")
			h.run()
		}
		if h.w.overlay != nil || store.written() != 0 || h.w.applying {
			t.Fatalf("%s: overlay %v, %d writes, applying %v", k, h.w.overlay != nil, store.written(), h.w.applying)
		}
		if h.w.apply.plan == nil {
			t.Fatalf("%s: cancelling dropped the plan", k)
		}
	}
}

func TestYAppliesTheShownPlan(t *testing.T) {
	store := &fakeStore{}
	h := teamWs(t, store, added(2))
	shown := h.planned()
	h.confirmApply()
	if !h.w.applying || !strings.Contains(h.applyBody(), "applyingevent1of2") {
		t.Fatalf("y did not start applying: %s", h.applyBody())
	}
	h.settleApply()
	if got, want := store.sent(), planWrites(shown); !sameWrites(got, want) {
		t.Fatalf("sent %+v, want %+v", got, want)
	}
	if want := flat("sent 2 writes and 0 deletes across 2 events to store acme"); !strings.Contains(h.toastText(), want) {
		t.Fatalf("toast = %q, want %q", h.toastText(), want)
	}
	if h.w.apply.plan != nil || h.w.applying || strings.Contains(h.applyBody(), "applying") {
		t.Fatalf("after the apply: plan %v, applying %v, body %s", h.w.apply.plan != nil, h.w.applying, h.applyBody())
	}
	h.applyBadgeIs("✓")
}

func TestTheExecutedPlanIsTheShownPlan(t *testing.T) {
	store := &fakeStore{tuples: []language.Tuple{member("9")}}
	h := teamWs(t, store, `{"type": "roster", "team": "a", "users": ["1", "2"]}`)
	shown := h.planned()
	if shown.Writes != 2 || shown.Deletes != 1 {
		t.Fatalf("fixture: plan = %+v", shown)
	}
	// Planned again now, the roster would delete user 8 instead of user 9.
	store.tuples = []language.Tuple{member("8")}
	h.confirmApply()
	h.settleApply()
	if got, want := store.sent(), planWrites(shown); !sameWrites(got, want) {
		t.Fatalf("sent %+v, want the shown plan's %+v", got, want)
	}
	if n := store.readCount(); n != 1 {
		t.Fatalf("the store was read %d times: applying planned again", n)
	}
}

func TestChangingTheSelectionDropsThePlan(t *testing.T) {
	h := teamWs(t, &fakeStore{}, added(2))
	dropped := func(why string) {
		t.Helper()
		if h.w.apply.plan != nil || h.w.applyCancel != nil {
			t.Fatalf("%s: the plan stayed (planning %v)", why, h.w.applyCancel != nil)
		}
		if want := flat("the plan was dropped: " + why); !strings.Contains(h.toastText(), want) {
			t.Fatalf("toast = %q, want %q", h.toastText(), want)
		}
		h.run() // the toast's timer
	}
	h.planned()
	h.press("space")
	dropped("the selection changed")

	h.press("space")
	h.planned()
	h.pasteEvents(added(1))
	dropped("events were pasted")

	h.planned()
	h.press("d") // the pasted row, which the paste put the cursor on
	dropped("an event was removed")

	h.planned()
	events := filepath.Join(t.TempDir(), "events.jsonl")
	writeFile(t, events, added(1))
	h.loadEvents(events)
	dropped("events were loaded")

	// A plan still being made is dropped too, and never lands.
	h.press("p")
	h.press("space")
	dropped("the selection changed")
	h.run()
	h.run()
	if h.w.apply.plan != nil {
		t.Fatal("the dropped plan landed")
	}

	// Whatever changes the selection, a and y look again before they go on.
	h.press("space")
	h.planned()
	h.w.apply.events[0].selected = false
	h.press("a")
	dropped("the events or the file changed")
	if h.w.overlay != nil {
		t.Fatal("the question opened on a plan of other events")
	}
	h.w.apply.events[0].selected = true
	h.planned()
	h.press("a")
	h.run()
	h.w.apply.events[0].selected = false
	h.press("y")
	dropped("the events or the file changed")
	if h.w.applying || h.w.overlay != nil {
		t.Fatalf("y ran a plan of other events: applying %v", h.w.applying)
	}
}

func TestSavingDropsThePlan(t *testing.T) {
	h := teamWs(t, &fakeStore{}, added(1))
	dropped := func(why string) {
		t.Helper()
		if h.w.apply.plan != nil {
			t.Fatalf("%s: the plan stayed", why)
		}
		if want := flat("the plan was dropped: " + why); !strings.Contains(h.toastText(), want) {
			t.Fatalf("toast = %q, want %q", h.toastText(), want)
		}
		h.run()
	}
	h.planned()
	h.rename(0, "renamed")
	h.press("ctrl+s")
	dropped("the file was saved")
	h.settleDebounce()

	// The changed-on-disk dialog's reload and save over it drop it too.
	h.planned()
	writeFile(t, h.w.path, teamRules+"# edited outside\n")
	h.press("ctrl+s", "r", "y")
	dropped("the file was reloaded")

	h.planned()
	writeFile(t, h.w.path, teamRules+"# edited again\n")
	h.rename(0, "again")
	h.press("ctrl+s", "o", "y")
	dropped("the file was saved")
}

func TestAnOutsideEditBlocksPlanning(t *testing.T) {
	h := teamWs(t, &fakeStore{}, added(1))
	writeFile(t, h.w.path, teamRules+"# edited outside\n")
	h.press("p")
	h.run()
	changed := flat("the file changed on disk: ctrl+s reloads it or saves over it")
	if h.w.applyCancel != nil || h.w.apply.plan != nil || !strings.Contains(h.toastText(), changed) {
		t.Fatalf("p on a file changed outside: planning %v, plan %v, toast %q",
			h.w.applyCancel != nil, h.w.apply.plan != nil, h.toastText())
	}

	// A plan made before the edit is dropped when a asks for it.
	writeFile(t, h.w.path, teamRules)
	h.run()
	h.planned()
	writeFile(t, h.w.path, teamRules+"# edited outside\n")
	h.press("a")
	h.run()
	if h.w.overlay != nil || h.w.apply.plan != nil {
		t.Fatalf("a on a file changed outside: overlay %v, plan %v", h.w.overlay != nil, h.w.apply.plan != nil)
	}
	if want := flat("the plan was dropped: ") + changed; !strings.Contains(h.toastText(), want) {
		t.Fatalf("toast = %q, want %q", h.toastText(), want)
	}
}

func TestAStalePlanReplansBeforeConfirm(t *testing.T) {
	store := &fakeStore{}
	h := teamWs(t, store, `{"type": "roster", "team": "a", "users": ["1"]}`)
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	h.w.now = func() time.Time { return now }
	h.planned()
	if store.readCount() != 1 {
		t.Fatalf("fixture: planning read %d times", store.readCount())
	}

	now = now.Add(6 * time.Minute)
	h.press("a")
	if h.confirming() != nil {
		t.Fatal("the question opened on a stale plan")
	}
	if want := flat("the plan is over five minutes old: planning it again"); !strings.Contains(h.toastText(), want) {
		t.Fatalf("toast = %q, want %q", h.toastText(), want)
	}
	h.settleApply()
	if store.readCount() != 2 {
		t.Fatalf("the store was read %d times, want a second plan", store.readCount())
	}
	if h.confirming() == nil || !h.w.apply.plan.at.Equal(now) {
		t.Fatalf("no question on the new plan: %s / %s", h.applyBody(), h.toastText())
	}

	// A plan younger than planMaxAge goes straight to the question.
	h.press("esc")
	h.run()
	now = now.Add(planMaxAge)
	h.press("a")
	if h.confirming() == nil || store.readCount() != 2 {
		t.Fatalf("a fresh plan: question %v, %d reads", h.confirming() != nil, store.readCount())
	}
}

// stepClock is a clock the store's reads move on, safe to read from the UI
// while planning moves it.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// slowStore is a store each read of which takes six minutes by clock.
type slowStore struct {
	*fakeStore
	clock *stepClock
}

func (s slowStore) ReadTuples(ctx context.Context, f language.TupleFilter) ([]language.Tuple, error) {
	s.clock.add(6 * time.Minute)
	return s.fakeStore.ReadTuples(ctx, f)
}

func TestAPlanSlowerThanTheAgeLimitStillOpensTheQuestion(t *testing.T) {
	store := &fakeStore{}
	h := teamWs(t, store, `{"type": "roster", "team": "a", "users": ["1"]}`)
	clock := &stepClock{now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	h.w.now = clock.Now
	h.w.opts.Apply.Reader = slowStore{fakeStore: store, clock: clock}

	// The plan takes six minutes to make: its age starts when it lands.
	h.planned()
	h.press("a")
	h.run()
	if h.confirming() == nil || store.readCount() != 1 {
		t.Fatalf("a on a plan just made: question %v, %d reads, want 1: %s", h.confirming() != nil,
			store.readCount(), h.toastText())
	}

	// A stale plan is made again once, slowly, and the question opens on it.
	h.press("esc")
	h.run()
	clock.add(6 * time.Minute)
	h.press("a")
	h.settleApply()
	if h.confirming() == nil || store.readCount() != 2 {
		t.Fatalf("after one slow re-plan: question %v, %d reads, want 2", h.confirming() != nil, store.readCount())
	}
}

func TestAPlanThatGoesStaleUnderTheQuestionIsMadeAgain(t *testing.T) {
	store := &fakeStore{tuples: []language.Tuple{member("9")}}
	h := teamWs(t, store, `{"type": "roster", "team": "a", "users": ["1"]}`)
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	h.w.now = func() time.Time { return now }
	shown := h.planned()
	h.press("a")
	h.run()
	if h.confirming() == nil {
		t.Fatalf("a opened no question: %s", h.toastText())
	}

	// The question stays open past the age limit, and the store moves on.
	now = now.Add(6 * time.Minute)
	store.tuples = []language.Tuple{member("8")}
	h.press("y")
	if h.w.applying || store.written() != 0 {
		t.Fatalf("y on a stale plan: applying %v, %d writes", h.w.applying, store.written())
	}
	if want := flat("the plan is over five minutes old: planning it again"); !strings.Contains(h.toastText(), want) {
		t.Fatalf("toast = %q, want %q", h.toastText(), want)
	}
	h.settleApply()
	if store.written() != 0 || store.readCount() != 2 {
		t.Fatalf("after y on a stale plan: %d writes, %d reads; want none written and a second plan",
			store.written(), store.readCount())
	}
	if h.confirming() == nil || h.w.apply.plan == nil || h.w.apply.plan.plan == shown {
		t.Fatalf("the question did not open again on a new plan: %s", h.applyBody())
	}
	if !strings.Contains(h.applyBody(), flat("- user:8 member team:a")) {
		t.Fatalf("the new plan is not the store as it is now:\n%s", h.applyBody())
	}
}

func TestNothingToChangeRefusesApply(t *testing.T) {
	h := teamWs(t, &fakeStore{}, `{"type": "other"}`)
	h.planned()
	body := h.applyBody()
	for _, want := range []string{"1 event: 0 writes, 0 deletes, 1 matched no rule", "nothing to change"} {
		if !strings.Contains(body, flat(want)) {
			t.Fatalf("body lacks %q:\n%s", want, body)
		}
	}
	h.press("a")
	h.run()
	if h.w.overlay != nil || !strings.Contains(h.toastText(), "nothingtochange") {
		t.Fatalf("a with nothing to change: overlay %v, toast %q", h.w.overlay != nil, h.toastText())
	}
}

// directRules writes a conditioned tuple with no tuple_filter.
const directRules = `version: "1"
rules:
  - name: grant
    when: input.type == "grant"
    tuples:
      - user: "user:{{ input.user }}"
        relation: viewer
        object: "doc:{{ input.doc }}"
        condition: before
        context:
          limit: "{{ input.limit }}"
`

func TestAPlanningErrorShowsItsFix(t *testing.T) {
	h := applyWs(t, directRules, &fakeStore{})
	h.press("4")
	h.pasteEvents(`{"type": "grant", "doc": "d", "user": "u", "limit": "1"}
{"type": "other"}
{"type": "grant", "doc": "d", "user": "u", "limit": "2"}`)
	h.press("p")
	h.settleApply()
	body := h.applyBody()
	for _, want := range []string{
		"event 3 (grant): writes user:u viewer doc:d with a different condition or context than event 1;",
		"give the rule a tuple_filter so it replaces the tuple",
	} {
		if !strings.Contains(body, flat(want)) {
			t.Fatalf("body lacks %q:\n%s", want, body)
		}
	}
	h.press("a")
	h.run()
	if h.w.apply.plan != nil || h.w.overlay != nil {
		t.Fatalf("a planning error left a plan to apply: plan %v, overlay %v", h.w.apply.plan != nil, h.w.overlay != nil)
	}
}

func TestALatePlanIsDropped(t *testing.T) {
	h := teamWs(t, &fakeStore{}, added(1))
	h.run()
	h.press("p")
	// The first plan finishes, but its result is held back...
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	late := collect(ctx, h.cmds)
	cancel()
	h.cmds = nil
	// ...until a second plan has replaced it.
	h.press("p")
	for _, m := range late {
		h.send(m)
	}
	if h.w.apply.plan != nil || h.w.applyCancel == nil {
		t.Fatalf("the late plan landed: plan %v, planning %v", h.w.apply.plan != nil, h.w.applyCancel != nil)
	}
	h.settleApply()
	if h.w.apply.plan == nil {
		t.Fatal("the second plan did not land")
	}
	h.run()
	if h.w.busy != 0 {
		t.Fatalf("busy = %d after both plans", h.w.busy)
	}
}

func TestEscCancelsPlanning(t *testing.T) {
	h := teamWs(t, &fakeStore{}, added(1))
	h.press("p")
	if !strings.Contains(h.applyBody(), "planningevent1of1") || !slices.Contains(h.w.keys(), "esc cancel") {
		t.Fatalf("planning shows %s, keys %v", h.applyBody(), h.w.keys())
	}
	h.press("esc")
	if h.w.active != paneApply || h.w.focus != shell.FocusPanel {
		t.Fatalf("esc while planning left the pane: active %d, focus %d", h.w.active, h.w.focus)
	}
	if h.w.applyCancel != nil || strings.Contains(h.applyBody(), "planning") {
		t.Fatalf("esc did not cancel the planning: %s", h.applyBody())
	}
	h.run()
	h.run()
	if h.w.apply.plan != nil {
		t.Fatal("the cancelled plan landed")
	}
	h.press("esc")
	if h.w.focus != shell.FocusSidebar {
		t.Fatal("esc with nothing planning did not go to the sidebar")
	}

	// ctrl+c while planning cancels it, then quits as ever.
	h.press("enter", "p", "ctrl+c")
	if h.w.applyCancel != nil {
		t.Fatal("ctrl+c did not cancel the planning")
	}
	h.run()
	if !h.quit {
		t.Fatal("ctrl+c while planning did not quit")
	}
}

func TestAFailedApplyOffersResume(t *testing.T) {
	store := &fakeStore{failAt: 3}
	h := teamWs(t, store, added(4))
	h.w.opts.Apply.Hints = func(error) []string { return []string{"check \x1b]0;pwned\x07the store"} }
	h.planned()
	h.confirmApply()
	h.settleApply()
	// The command's rows end in " \" continuations, which a shell drops.
	body := strings.ReplaceAll(h.applyBody(), `\`, "")
	for _, want := range []string{
		"applied events 1-2; event 3 failed: the store refused the write",
		"check the store",
		"r resume from event 3",
		"ofga mappings apply " + h.w.path + " --events  --from-event 3",
	} {
		if !strings.Contains(body, strings.ReplaceAll(flat(want), `\`, "")) {
			t.Fatalf("the report lacks %q:\n%s", want, body)
		}
	}
	if v := h.w.View(); strings.Contains(v, "\x1b]") || strings.Contains(v, "\x07") {
		t.Fatalf("a hint reaches the terminal raw: %q", v)
	}
	h.applyBadgeIs("✗")
	if !slices.Contains(h.w.keys(), "r resume") {
		t.Fatalf("keys = %v", h.w.keys())
	}

	h.press("r")
	h.settleApply()
	c := h.confirming()
	if c == nil {
		t.Fatalf("r opened no question: %s / %s", h.applyBody(), h.toastText())
	}
	if want := "Apply 2 writes to store acme (" + applyStoreID + ")?"; c.question != want {
		t.Fatalf("question = %q, want %q", c.question, want)
	}
	h.press("y")
	h.settleApply()
	sent := store.sent()
	want := []fakeWrite{{writes: []language.Tuple{member("3")}}, {writes: []language.Tuple{member("4")}}}
	for i := range want {
		want[i].writes[0].Action = language.ActionWrite
	}
	if len(sent) != 5 || !sameWrites(sent[3:], want) {
		t.Fatalf("sent %+v, want events 3 and 4 after the 3 before", sent)
	}
	h.applyBadgeIs("✓")
}

// The resume command wraps with " \" continuations: a copy of its first rows
// alone cannot run, which would apply every event again.
func TestTheResumeCommandWrapsWithContinuations(t *testing.T) {
	store := &fakeStore{failAt: 2}
	h := teamWs(t, store, added(3))
	command := "ofga mappings apply /home/someone/projects/acme/mappings/membership.yaml --events " +
		"/home/someone/projects/acme/events/exported.jsonl --from-event 2 --store-id " + applyStoreID +
		" --profile production"
	h.w.opts.Apply.Resume = func(string, string, int) string { return command }
	h.planned()
	h.confirmApply()
	h.settleApply()
	mw, mh := h.w.mainSize()
	lines := strings.Split(plain(h.w.paneBody(paneApply, mw, mh)), "\n")
	first := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "ofga mappings apply") })
	last := slices.IndexFunc(lines, func(l string) bool { return strings.HasSuffix(strings.TrimRight(l, " "), "production") })
	if first < 0 || last <= first {
		t.Fatalf("the command is not shown across rows %d-%d:\n%s", first, last, strings.Join(lines, "\n"))
	}
	var joined []string
	for i, l := range lines[first : last+1] {
		l = strings.TrimRight(l, " ")
		if i < last-first && !strings.HasSuffix(l, ` \`) {
			t.Errorf("row %d of the command does not end with a continuation: %q", i+1, l)
		}
		joined = append(joined, strings.Fields(strings.TrimSuffix(l, `\`))...)
	}
	if !slices.Equal(joined, strings.Fields(command)) {
		t.Fatalf("the rows hold %q, want %q", joined, strings.Fields(command))
	}
}

func TestTheResumeCommandNamesTheEventsFile(t *testing.T) {
	store := &fakeStore{failAt: 2}
	h := applyWs(t, teamRules, store)
	var gotEvents string
	gotFrom := 0
	h.w.opts.Apply.Resume = func(_, eventsPath string, from int) string {
		gotEvents, gotFrom = eventsPath, from
		return "resume with " + eventsPath
	}
	events := filepath.Join(t.TempDir(), "events.jsonl")
	writeFile(t, events, added(3))
	h.press("4")
	h.loadEvents(events)
	h.planned()
	h.confirmApply()
	h.settleApply()
	if gotEvents != events || gotFrom != 2 {
		t.Fatalf("resume names %q from %d, want %q from 2", gotEvents, gotFrom, events)
	}
	// A long path wraps with "\" continuations, which the shell drops.
	if body := h.applyBody(); !strings.Contains(strings.ReplaceAll(body, `\`, ""), flat("resume with "+events)) || strings.Contains(body, "putthe") {
		t.Fatalf("the report:\n%s", body)
	}

	// Without its first event, the file's numbers are not the plan's.
	h.press("space")
	store.failAt = 4
	h.planned()
	h.confirmApply()
	h.settleApply()
	if gotEvents != "" || gotFrom != 2 {
		t.Fatalf("resume names %q from %d, want no file from 2", gotEvents, gotFrom)
	}
	if body := h.applyBody(); !strings.Contains(body, flat("put the selected events in a file first")) {
		t.Fatalf("the report:\n%s", body)
	}
}

func TestCtrlCDuringApplyStopsBetweenRequests(t *testing.T) {
	store := &fakeStore{}
	h := teamWs(t, store, added(3))
	shown := h.planned()
	h.run()
	store.block = make(chan struct{})
	h.confirmApply()
	wait := h.runAside()
	<-store.block // event 1's request is in flight
	h.press("ctrl+c")
	if h.w.overlay != nil || !h.w.applying {
		t.Fatalf("ctrl+c while applying: overlay %v, applying %v", h.w.overlay != nil, h.w.applying)
	}
	store.block <- struct{}{} // and finishes
	wait()
	h.run()
	sent := store.sent()
	if len(sent) != 1 || len(sent) >= len(planWrites(shown)) {
		t.Fatalf("sent %d requests of %d", len(sent), len(planWrites(shown)))
	}
	if sent[0].ctxErr != nil {
		t.Fatalf("the stop cancelled the request in flight: %v", sent[0].ctxErr)
	}
	body := h.applyBody()
	for _, want := range []string{"applied event 1; stopped before event 2 finished", "r resume from event 2"} {
		if !strings.Contains(body, flat(want)) {
			t.Fatalf("the report lacks %q:\n%s", want, body)
		}
	}
	if h.w.applying || h.w.overlay != nil || h.quit {
		t.Fatalf("after the stop: applying %v, overlay %v, quit %v", h.w.applying, h.w.overlay != nil, h.quit)
	}
	h.applyBadgeIs("✗")
}

func TestQuitWhileApplyingStopsFirst(t *testing.T) {
	store := &fakeStore{}
	h := teamWs(t, store, added(3))
	h.planned()
	h.run()
	store.block = make(chan struct{})
	h.confirmApply()
	wait := h.runAside()
	<-store.block
	h.rename(0, "renamed") // unsaved edits: a quit would ask
	h.press("q")
	h.press("ctrl+c") // nothing quits until the stop is reported
	if h.w.overlay != nil || !h.w.applying {
		t.Fatalf("q while applying: overlay %v, applying %v", h.w.overlay != nil, h.w.applying)
	}
	store.block <- struct{}{}
	wait()
	h.run()
	if h.quit || h.w.applying || !strings.Contains(h.applyBody(), "stoppedbeforeevent2finished") {
		t.Fatalf("after the stop: quit %v, applying %v, body %s", h.quit, h.w.applying, h.applyBody())
	}
	h.press("ctrl+c")
	if _, ok := h.confirmingQuit(); !ok {
		t.Fatal("ctrl+c after the stop did not take the quit path")
	}
}

func TestACancelledQuitKeepsThePlanInFlight(t *testing.T) {
	h := teamWs(t, &fakeStore{}, added(1))
	h.press("p") // its command is queued, not run: the plan is in flight
	h.rename(0, "renamed")
	for _, k := range []string{"q", "ctrl+c"} {
		h.press(k)
		if _, ok := h.confirmingQuit(); !ok {
			t.Fatalf("%s with unsaved edits did not ask", k)
		}
		if h.w.pending == nil || h.w.applyCancel == nil {
			t.Fatalf("%s dropped the plan in flight before the quit was answered", k)
		}
		h.press("esc")
		if h.w.overlay != nil || h.w.pending == nil {
			t.Fatalf("%s then esc: overlay %v, planning %v", k, h.w.overlay != nil, h.w.pending != nil)
		}
	}
	h.settleApply()
	if h.w.apply.plan == nil || h.quit {
		t.Fatalf("after the cancelled quits: plan %v, quit %v", h.w.apply.plan != nil, h.quit)
	}

	// A quit that goes ahead stops the planning.
	d := teamWs(t, &fakeStore{}, added(1))
	d.press("p")
	d.rename(0, "renamed")
	d.press("q", "d")
	if d.w.pending != nil || d.w.applyCancel != nil {
		t.Fatal("quitting without saving left the plan running")
	}
	d.run()
	if !d.quit {
		t.Fatal("d did not quit")
	}
}

// confirmingQuit is the quit prompt, when it is open.
func (h *wsHarness) confirmingQuit() (*quitPrompt, bool) {
	if h.w.overlay == nil {
		return nil, false
	}
	q, ok := h.w.overlay.top().(*quitPrompt)
	return q, ok
}

func TestSaveIsRefusedWhileApplying(t *testing.T) {
	store := &fakeStore{}
	h := teamWs(t, store, added(2))
	h.planned()
	h.run()
	before := readFile(t, h.w.path)
	store.block = make(chan struct{})
	h.confirmApply()
	wait := h.runAside()
	<-store.block
	h.rename(0, "renamed")
	h.press("ctrl+s")
	if !strings.Contains(h.toastText(), flat(applyLocksFile)) {
		t.Fatalf("toast = %q", h.toastText())
	}
	// The changed-on-disk dialog's ways out are refused the same way.
	h.cmds = append(h.cmds, h.w.overwrite(), h.w.reload())
	if got := readFile(t, h.w.path); got != before || h.w.doc.Rules[0].Name != "renamed" {
		t.Fatalf("the file changed under the apply: rule %q, file:\n%s", h.w.doc.Rules[0].Name, got)
	}
	store.block <- struct{}{}
	<-store.block
	store.block <- struct{}{}
	wait()
	h.run()
	if h.w.applying {
		t.Fatal("still applying")
	}
	h.press("ctrl+s")
	if got := readFile(t, h.w.path); !strings.Contains(got, "name: renamed") {
		t.Fatalf("the save after the apply did not write:\n%s", got)
	}
}

// failedAtThree is teamWs with four events whose third request fails, applied:
// the report offers r from event 3.
func failedAtThree(t *testing.T, store *fakeStore, events string) *wsHarness {
	t.Helper()
	store.failAt = 3
	h := teamWs(t, store, events)
	h.planned()
	h.confirmApply()
	h.settleApply()
	if h.w.apply.resumeAt != 3 {
		t.Fatalf("fixture: resume from %d: %s", h.w.apply.resumeAt, h.applyBody())
	}
	return h
}

// keepsTheResumePoint fails unless the failure report and r from event 3 are
// still on offer.
func (h *wsHarness) keepsTheResumePoint() {
	h.t.Helper()
	body := h.applyBody()
	for _, want := range []string{"applied events 1-2; event 3 failed: the store refused the write",
		"r resume from event 3", "--from-event 3"} {
		if !strings.Contains(body, flat(want)) {
			h.t.Fatalf("the report lacks %q:\n%s", want, body)
		}
	}
	h.applyBadgeIs("✗")
	if !slices.Contains(h.w.keys(), "r resume") {
		h.t.Fatalf("keys = %v", h.w.keys())
	}
}

func TestAReplanDoesNotAskWhileTyping(t *testing.T) {
	store := &fakeStore{}
	h := failedAtThree(t, store, added(4))
	h.press("r")
	h.press("v")
	h.typeText(`{"y"`)
	h.settleApply()
	if h.confirming() != nil {
		t.Fatal("the question opened over the paste box")
	}
	if !strings.Contains(h.toastText(), flat("the plan is ready: a applies it")) {
		t.Fatalf("toast = %q", h.toastText())
	}
	h.typeText("y")
	h.run()
	if store.written() != 3 || h.w.applying {
		t.Fatalf("typing y applied: %d writes, applying %v", store.written(), h.w.applying)
	}
	if _, ok := h.topOf(paneApply).(*applyPaste); !ok {
		t.Fatalf("the paste box closed: top is %T", h.topOf(paneApply))
	}
}

func TestAReplanOnAnotherPaneDoesNotAsk(t *testing.T) {
	store := &fakeStore{}
	h := failedAtThree(t, store, added(4))
	h.press("r", "1")
	h.settleApply()
	if h.confirming() != nil {
		t.Fatal("the question opened over the Rules pane")
	}
	if !strings.Contains(h.toastText(), flat("the plan is ready: a applies it")) {
		t.Fatalf("toast = %q", h.toastText())
	}
	h.press("4")
	if h.w.apply.plan == nil {
		t.Fatalf("the plan is not on Apply: %s", h.applyBody())
	}
	h.press("a")
	h.run()
	if h.confirming() == nil {
		t.Fatal("a on the waiting plan opened no question")
	}
	h.press("y")
	h.settleApply()
	if store.written() != 5 {
		t.Fatalf("%d writes, want events 3 and 4 after the 3 before", store.written())
	}
}

func TestNothingJoinsTheListWhileApplying(t *testing.T) {
	store := &fakeStore{}
	h := teamWs(t, store, added(2))
	h.planned()
	h.run()
	store.block = make(chan struct{})
	h.confirmApply()
	wait := h.runAside()
	<-store.block
	// A paste box left open on the pane, finished while the apply runs.
	h.cmds = append(h.cmds, h.w.pushOn(paneApply, newApplyPaste(h.w.sess)))
	h.send(tea.PasteMsg{Content: added(1)})
	h.press("ctrl+d")
	// A file chosen in a browser left open the same way.
	events := filepath.Join(t.TempDir(), "events.jsonl")
	writeFile(t, events, added(1))
	h.send(paneMsg{p: paneApply, epoch: h.w.paneEpoch, msg: closedMsg{result: events}})
	h.press("space", "d")
	if got := h.eventLabels(); !slices.Equal(got, []string{"paste 1", "paste 2"}) || h.w.apply.selected() != 2 {
		t.Fatalf("the list changed under the apply: %v, %d selected", got, h.w.apply.selected())
	}
	if !strings.Contains(h.toastText(), flat(applyRunning)) {
		t.Fatalf("toast = %q", h.toastText())
	}
	store.block <- struct{}{}
	<-store.block
	store.block <- struct{}{}
	wait()
	h.run()
	if h.w.applying || store.written() != 2 {
		t.Fatalf("applying %v, %d writes", h.w.applying, store.written())
	}
}

func TestEscDuringAResumeKeepsTheResumePoint(t *testing.T) {
	h := failedAtThree(t, &fakeStore{}, added(4))
	h.press("r")
	if !strings.Contains(h.applyBody(), "planningevent1of2") {
		t.Fatalf("r is not planning: %s", h.applyBody())
	}
	h.press("esc")
	h.run()
	h.keepsTheResumePoint()

	// So is answering n to the question on the new plan, then dropping it.
	h.press("r")
	h.settleApply()
	h.press("n")
	h.run()
	h.w.apply.events[0].selected = false // behind the hooks' back: a drops the plan
	h.press("a")
	h.w.apply.events[0].selected = true
	h.run()
	h.keepsTheResumePoint()
}

func TestAPlanningErrorDuringAResumeKeepsTheResumePoint(t *testing.T) {
	store := &fakeStore{}
	h := failedAtThree(t, store, added(2)+`{"type": "roster", "team": "a", "users": ["3"]}`+"\n"+added(1))
	store.readErr = errors.New("the store is down")
	h.press("r")
	h.settleApply()
	if body := h.applyBody(); !strings.Contains(body, flat("the store is down")) {
		t.Fatalf("the planning error is not shown:\n%s", body)
	}
	h.keepsTheResumePoint()
}

func TestASaveKeepsTheResumePoint(t *testing.T) {
	store := &fakeStore{}
	h := failedAtThree(t, store, added(4))
	h.rename(0, "fixed")
	h.press("ctrl+s")
	h.run()
	h.keepsTheResumePoint()

	// A reload from the changed-on-disk dialog keeps it too.
	writeFile(t, h.w.path, readFile(t, h.w.path)+"# edited outside\n")
	h.press("ctrl+s", "r", "y")
	h.run()
	h.keepsTheResumePoint()

	h.press("r")
	h.settleApply()
	if h.confirming() == nil {
		t.Fatalf("r after the save opened no question: %s / %s", h.applyBody(), h.toastText())
	}
	h.press("y")
	h.settleApply()
	sent := store.sent()
	want := []fakeWrite{{writes: []language.Tuple{member("3")}}, {writes: []language.Tuple{member("4")}}}
	for i := range want {
		want[i].writes[0].Action = language.ActionWrite
	}
	if len(sent) != 5 || !sameWrites(sent[3:], want) {
		t.Fatalf("sent %+v, want events 3 and 4 after the 3 before", sent)
	}
}

func TestChangingTheSelectionClearsTheResumePoint(t *testing.T) {
	h := failedAtThree(t, &fakeStore{}, added(4))
	h.press("space")
	h.run()
	body := h.applyBody()
	if strings.Contains(body, "event3failed") || strings.Contains(body, "resumefrom") || h.w.apply.resumeAt != 0 {
		t.Fatalf("the resume point outlived a selection change:\n%s", body)
	}
	h.applyBadgeIs("3")
	if slices.Contains(h.w.keys(), "r resume") {
		t.Fatalf("keys = %v", h.w.keys())
	}
	h.press("r")
	if h.w.applyCancel != nil {
		t.Fatal("r planned a resume of another selection")
	}
}

func TestAPlanFromPClearsTheResumePoint(t *testing.T) {
	h := failedAtThree(t, &fakeStore{}, added(4))
	// Cancelled, p leaves the report as it was...
	h.press("p", "esc")
	h.run()
	h.keepsTheResumePoint()
	// ...but once its plan lands, the plan replaces the report for good.
	h.planned()
	h.press("ctrl+s") // nothing to save, so the plan stays
	h.rename(0, "renamed")
	h.press("ctrl+s")
	h.run()
	body := h.applyBody()
	if strings.Contains(body, "event3failed") || h.w.apply.resumeAt != 0 {
		t.Fatalf("the resume point outlived a plan from p:\n%s", body)
	}
	h.applyBadgeIs("4")
}
