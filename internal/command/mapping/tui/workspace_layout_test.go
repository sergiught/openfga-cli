package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/ui/icons"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// longReason is a read-only reason long enough to wrap in the banner.
const longReason = "uses YAML anchors or aliases, which the editor would flatten, and this reason keeps going so that it wraps over several rows of the pane"

// layoutModes opens the three modes the frame can be in.
func layoutModes(t *testing.T) map[string]*wsHarness {
	t.Helper()
	readOnly := loadedWs(t, anchored)
	readOnly.w.file.Reason = longReason
	return map[string]*wsHarness{
		"normal":     loadedWs(t, twoRules),
		"read-only":  readOnly,
		"load-error": brokenWs(t),
	}
}

// assertFrame checks a frame fills its terminal exactly. The sidebar lists every
// pane; below its width the nav strip shows the active one alone.
func assertFrame(t *testing.T, label string, view string, width, height int, panes []string) {
	t.Helper()
	if got := lipgloss.Height(view); got != height {
		t.Errorf("%s: height = %d, want %d\n%s", label, got, height, plain(view))
	}
	lines := strings.Split(plain(view), "\n")
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("%s: line %d is %d cells, want at most %d: %q", label, i, got, width, line)
		}
	}
	if !strings.Contains(lines[len(lines)-1], "help") && !strings.Contains(lines[len(lines)-1], "close") &&
		!strings.Contains(lines[len(lines)-1], "select") {
		t.Errorf("%s: no key hints in the status bar:\n%s", label, plain(view))
	}
	for _, name := range panes {
		if !strings.Contains(plain(view), name) {
			t.Errorf("%s: no %s in the sidebar or nav strip:\n%s", label, name, plain(view))
		}
	}
}

func TestWorkspaceFrameAt80x24And120x40(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 16}} {
		for mode := range layoutModes(t) {
			for _, p := range []string{"1", "2", "3"} {
				for _, overlay := range []string{"none", "model", "help"} {
					h := layoutModes(t)[mode]
					h.resize(size[0], size[1])
					h.press(p)
					switch overlay {
					case "model":
						h.cmds = append(h.cmds, h.w.openModelDialog())
					case "help":
						h.press("?")
					}
					label := fmt.Sprintf("%dx%d %s pane %s overlay %s", size[0], size[1], mode, p, overlay)
					panes := paneNames[:]
					if size[0] < 80 {
						panes = []string{paneNames[h.w.active]}
					}
					assertFrame(t, label, h.w.View(), size[0], size[1], panes)
				}
			}
		}
	}
}

func TestTheMainPaneNeverClipsSilently(t *testing.T) {
	var src strings.Builder
	src.WriteString("version: \"1\"\nrules:\n")
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&src, "  - name: rule-number-%02d\n    when: input.type == \"t.%d\"\n    tuples:\n      - {user: \"user:1\", relation: member, object: \"team:1\"}\n", i, i)
	}
	h := loadedWs(t, src.String())
	h.resize(80, 24)
	h.press("1")
	if got := lipgloss.Height(h.w.View()); got != 24 {
		t.Fatalf("height = %d, want 24", got)
	}
	for range 59 {
		h.press("down")
	}
	v := h.view()
	if got := lipgloss.Height(h.w.View()); got != 24 {
		t.Fatalf("height after moving = %d, want 24", got)
	}
	if !strings.Contains(v, "rule-number-60") {
		t.Fatalf("the last rule is not visible:\n%s", v)
	}
}

func TestWorkspaceFrameCannotBeDrivenByTheFile(t *testing.T) {
	const hostile = "evil\x1b]0;pwned\x07\u009b31m\u202e\u2066x\x1b[2J"
	src := "version: \"1\"\nrules:\n  - name: \"" + `r\u001b]0;pwned\u0007\u009b31m\u202e` + "\"\n    tuples:\n      - {user: \"user:1\", relation: member, object: \"team:1\"}\n" +
		"tests:\n  - name: \"" + `t\u001b]0;pwned\u0007\u202e` + "\"\n    input: {type: x}\n    expect_tuples: []\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "hostile"+hostile+".yaml")
	writeFile(t, path, src)
	o, err := openPath(context.Background(), path)
	if err != nil {
		t.Skipf("the platform refuses the path: %v", err)
	}
	h := newWsHarness(t, o, Options{})
	h.run()
	h.w.findings = []mapping.Finding{{Rule: 0, Message: "bad\x1b]0;pwned\x07 \u009b31m \u202e finding"}}
	h.w.check.set(h.w.findings, "", "", nil)
	// What each pane shows of the hostile text once it is sanitized.
	for p, shown := range map[string]string{"1": "r31m", "2": "finding", "3": "t"} {
		h.press(p)
		raw := ansiSeq.ReplaceAllString(h.w.View(), "")
		if !strings.Contains(raw, shown) {
			t.Fatalf("pane %s never showed %q, so this proves nothing:\n%s", p, shown, raw)
		}
		for _, r := range raw {
			if r == '\n' {
				continue
			}
			if r == 0x1b || (r >= 0x80 && r <= 0x9f) || r < 0x20 || r == 0x7f || unicode.Is(unicode.Bidi_Control, r) {
				t.Fatalf("pane %s: %U reached the frame:\n%q", p, r, raw)
			}
		}
	}
}

func TestTooSmall(t *testing.T) {
	h := loadedWs(t, twoRules)
	for _, size := range [][2]int{{59, 24}, {80, 15}, {20, 5}} {
		h.resize(size[0], size[1])
		if v := h.view(); !strings.Contains(v, "terminal too small") {
			t.Fatalf("%dx%d: %s", size[0], size[1], v)
		}
	}
	h.resize(60, 16)
	if v := h.view(); strings.Contains(v, "terminal too small") {
		t.Fatalf("60x16 is the minimum and must draw:\n%s", v)
	}
}

// lastLine is the status bar of a frame.
func lastLine(view string) string {
	lines := strings.Split(plain(view), "\n")
	return lines[len(lines)-1]
}

func TestStatusKeysAreWholeAtMinimumWidth(t *testing.T) {
	deep := loadedWs(t, twoRules)
	deep.resize(60, 16)
	deep.press("1", "enter")
	for mode := range layoutModes(t) {
		for _, p := range []string{"1", "2", "3"} {
			h := layoutModes(t)[mode]
			h.resize(60, 16)
			h.press(p)
			assertKeysWhole(t, mode+" pane "+p, h)
			h.press("esc")
			assertKeysWhole(t, mode+" pane "+p+" sidebar", h)
		}
	}
	assertKeysWhole(t, "rule spoke", deep)

	// States where a field takes text, so "? help" is not offered, and an overlay.
	trigger := loadedWs(t, twoRules)
	trigger.resize(60, 16)
	trigger.press("1", "enter", "enter")
	rulesFilter := loadedWs(t, twoRules)
	rulesFilter.resize(60, 16)
	rulesFilter.press("1", "/")
	testsFilter := loadedWs(t, twoRules)
	testsFilter.resize(60, 16)
	testsFilter.press("3", "/")
	model := loadedWs(t, twoRules)
	model.resize(60, 16)
	model.cmds = append(model.cmds, model.w.openModelDialog())
	for label, h := range map[string]*wsHarness{
		"trigger form": trigger, "rules filter": rulesFilter, "tests filter": testsFilter, "model dialog": model,
	} {
		if label != "model dialog" && !h.w.typing() {
			t.Fatalf("%s: not typing, so the state is not the one under test", label)
		}
		assertKeysWhole(t, label, h)
	}
}

// assertKeysWhole checks every hint the workspace offers is on screen, whole,
// and that "? help" survived.
func assertKeysWhole(t *testing.T, label string, h *wsHarness) {
	t.Helper()
	keys := h.w.keys()
	line := lastLine(h.w.View())
	for _, k := range keys {
		if !strings.Contains(line, k) {
			t.Errorf("%s: hint %q is not whole in the status bar %q (keys %q)", label, k, line, keys)
		}
	}
	// While a field or an overlay has the keys, "? help" is not on offer.
	if !h.w.typing() && h.w.overlay == nil && !strings.Contains(line, "? help") {
		t.Errorf("%s: no \"? help\" in %q", label, line)
	}
}

func TestPasteBehindTheHelpIsDropped(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("1", "enter", "enter")
	if _, ok := h.topOf(paneRules).(*triggerForm); !ok {
		t.Fatalf("top is %T", h.topOf(paneRules))
	}
	before := h.w.doc.Rules[0].Name
	h.w.help = true
	h.send(tea.PasteMsg{Content: "pasted"})
	if got := h.w.doc.Rules[0].Name; got != before {
		t.Fatalf("a paste behind the help renamed the rule to %q", got)
	}
	h.w.help = false
	h.send(tea.PasteMsg{Content: "pasted"})
	if h.w.doc.Rules[0].Name == before {
		t.Fatal("the same paste with the help closed did nothing, so the test above proves nothing")
	}
}

func TestHelpHintIsWholeAtMinimumWidth(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.resize(60, 16)
	h.press("?")
	v := h.view()
	if strings.Contains(v, "c…") || !strings.Contains(v, "esc close") {
		t.Fatalf("the help hint is cut:\n%s", v)
	}
}

// The panes' glyphs come from the icon rung the config picked, so icons = off
// draws none. The panes once hardcoded their own.
func TestTheWorkspaceFollowsTheIconRung(t *testing.T) {
	t.Cleanup(func() { icons.Apply(icons.ModeNerdFont) })
	h := loadedWs(t, twoRules)
	icons.Apply(icons.ModeUnicode)
	ic := icons.I()
	glyphs := []string{ic.Tuple, ic.Query, ic.Assert, ic.Change}
	v := h.view()
	for _, g := range glyphs {
		if !strings.Contains(v, g) {
			t.Fatalf("the unicode rung's %q is not in the sidebar:\n%s", g, v)
		}
	}
	icons.Apply(icons.ModeOff)
	v = h.view()
	for _, g := range glyphs {
		if strings.Contains(v, g) {
			t.Fatalf("glyphs are off and the workspace still draws %q:\n%s", g, v)
		}
	}
}

// Where the preview sits beside the editor the frame still fills the terminal
// exactly, whichever section the preview shows, and the evaluation keeps its
// place however tall the file or the payload.
func TestTheFrameHoldsThePreview(t *testing.T) {
	for _, size := range [][2]int{{160, 24}, {160, 40}, {200, 60}} {
		for _, payload := range []bool{false, true} {
			h := freshWs(t, recipeDoc(t, "connection.created"))
			h.resize(size[0], size[1])
			if !h.w.previewShown() {
				t.Fatalf("fixture: no preview at %dx%d", size[0], size[1])
			}
			if payload {
				h.press("ctrl+t")
			}
			label := fmt.Sprintf("%dx%d payload %v", size[0], size[1], payload)
			assertFrame(t, label, h.w.View(), size[0], size[1], paneNames[:])
			if !strings.Contains(h.view(), "preview") {
				t.Errorf("%s: the evaluation was clipped away:\n%s", label, h.view())
			}
		}
	}
}

// anchorFirstTuple turns src into a file the editor cannot round-trip (a YAML
// anchor), so it opens read-only, with its rules intact.
func anchorFirstTuple(src string) string {
	return strings.Replace(src, "      - user: \"user:{{ input.user }}\"", "      - &t\n        user: \"user:{{ input.user }}\"", 1)
}

// applyStates are the Apply pane's states, each with the modes it can exist
// in. A new, unsaved file cannot be read-only, and nothing is edited in a
// read-only one, so neither has those states. A file that does not load has no
// event list, so it has only the two states that need no list.
var applyStates = []struct {
	name  string
	modes []string
}{
	{"no store", []string{"normal", "read-only", "load-error"}},
	{"store unusable", []string{"normal", "read-only"}},
	{"must load", []string{"load-error"}},
	{"never saved", []string{"normal"}},
	{"dirty", []string{"normal"}},
	{"list", []string{"normal", "read-only"}},
	{"planning", []string{"normal", "read-only"}},
	{"plan", []string{"normal", "read-only"}},
	{"nothing to change", []string{"normal", "read-only"}},
	{"planning error", []string{"normal", "read-only"}},
	{"confirm", []string{"normal", "read-only"}},
	{"applying", []string{"normal", "read-only"}},
	{"failure", []string{"normal", "read-only"}},
	{"stop", []string{"normal", "read-only"}},
}

// applyShows is a line each state with text under the list must show.
var applyShows = map[string]string{
	"plan":              "event 1",
	"nothing to change": "nothing to change",
	"planning error":    "event 3",
	"failure":           "applied",
	"stop":              "applied",
}

// applyStateWs drives the Apply pane into state, in mode, at width×height. The
// returned release lets go of anything the state holds open.
func applyStateWs(t *testing.T, mode, state string, width, height int) (h *wsHarness, release func()) {
	t.Helper()
	release = func() {}
	// reached fails unless the pane shows what the state is made of, so a
	// state cannot quietly turn into another.
	reached := func(h *wsHarness, want string) {
		t.Helper()
		if !strings.Contains(h.applyBody(), flat(want)) {
			t.Fatalf("%s %dx%d: not in state %q, the pane shows: %s", mode, width, height, state, h.applyBody())
		}
	}
	rules := func(src string) string {
		if mode == "read-only" {
			return anchorFirstTuple(src)
		}
		return src
	}
	open := func(src string, store *fakeStore) *wsHarness {
		h := applyWs(t, rules(src), store)
		if mode == "read-only" && !h.w.sess.ReadOnly {
			t.Fatal("fixture: the file is editable")
		}
		h.resize(width, height)
		return h
	}
	switch state {
	case "no store":
		switch mode {
		case "load-error":
			h = brokenWs(t)
		case "read-only":
			h = loadedWs(t, anchored)
		default:
			h = loadedWs(t, twoRules)
		}
		h.resize(width, height)
		h.press("4")
		if mode == "load-error" {
			reached(h, applyMustLoad) // a file that does not load comes first
		} else {
			reached(h, applyNoStore)
		}
		return h, release
	case "store unusable":
		src := twoRules
		if mode == "read-only" {
			src = anchored
		}
		h = loadedWsWith(t, src, Options{ApplyErr: "store ID \"my-store\" (from profile default) is not valid: " +
			"it must be a ULID, like 01ARZ3NDEKTSV4RRFFQ69G5FAV"})
		h.resize(width, height)
		h.press("4")
		reached(h, "The store cannot be used: store ID \"my-store\"")
		return h, release
	case "must load":
		h = brokenWs(t)
		h.w.opts.Apply = applyTarget(&fakeStore{})
		h.resize(width, height)
		h.press("4")
		reached(h, applyMustLoad)
		return h, release
	case "never saved":
		h = newWsHarness(t, opened{path: filepath.Join(t.TempDir(), "mapping.yaml"), doc: recipeDoc(t, "organization.member.added")},
			Options{Apply: applyTarget(&fakeStore{})})
		h.resize(width, height)
		h.press("4")
		reached(h, applyMustSave)
		return h, release
	case "dirty":
		h = open(twoRules, &fakeStore{})
		h.rename(1, "removed")
		h.press("4")
		reached(h, applyUnsaved)
		return h, release
	case "planning error":
		h = open(directRules, &fakeStore{})
		h.press("4")
		h.pasteEvents(`{"type": "grant", "doc": "d", "user": "u", "limit": "1"}
{"type": "other"}
{"type": "grant", "doc": "d", "user": "u", "limit": "2"}`)
		h.press("p")
		h.settleApply()
		return h, release
	}

	store := &fakeStore{}
	switch state {
	case "failure":
		store.failAt = 2
	}
	n := 3
	switch state {
	case "nothing to change":
		n = 0
	case "applying":
		n = 1
	}
	h = open(teamRules, store)
	h.press("4")
	events := added(n)
	if n == 0 {
		events = `{"type": "other"}`
	}
	h.pasteEvents(events)
	switch state {
	case "list":
		reached(h, fmt.Sprintf("%d of %d events selected", n, n))
	case "planning":
		h.press("p") // its command is queued, never run: the plan is in flight
		if h.w.apply.running == "" || h.w.applyCancel == nil {
			t.Fatalf("%s %dx%d: not planning: %s", mode, width, height, h.applyBody())
		}
	case "plan", "nothing to change":
		h.planned()
	case "confirm":
		h.planned()
		h.press("a")
		h.run()
		if h.confirming() == nil {
			t.Fatalf("a opened no question: %s", h.applyBody())
		}
	case "applying":
		h.planned()
		h.run()
		store.block = make(chan struct{})
		h.confirmApply()
		wait := h.runAside()
		var once sync.Once
		release = func() {
			once.Do(func() {
				store.block <- struct{}{}
				wait()
				h.run()
			})
		}
		t.Cleanup(release) // a failed check below must not leak the held write
		<-store.block      // the write is in flight
		if !h.w.applying || !h.w.apply.applying {
			t.Fatalf("%s %dx%d: not applying: %s", mode, width, height, h.applyBody())
		}
	case "failure":
		h.planned()
		h.confirmApply()
		h.settleApply()
		if !strings.Contains(h.applyBody(), flat("event 2 failed")) {
			t.Fatalf("%s %dx%d: no failure report: %s", mode, width, height, h.applyBody())
		}
	case "stop":
		h.planned()
		h.run()
		store.block = make(chan struct{})
		h.confirmApply()
		wait := h.runAside()
		<-store.block
		h.press("ctrl+c")
		store.block <- struct{}{}
		wait()
		h.run()
		if !strings.Contains(h.applyBody(), flat("stopped before")) {
			t.Fatalf("no stop report: %s", h.applyBody())
		}
	default:
		t.Fatalf("unknown state %q", state)
	}
	return h, release
}

func TestApplyFramesHoldAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{60, 16}, {80, 24}, {120, 40}} {
		for _, st := range applyStates {
			for _, mode := range st.modes {
				label := fmt.Sprintf("%dx%d %s %s", size[0], size[1], mode, st.name)
				h, release := applyStateWs(t, mode, st.name, size[0], size[1])
				if h.w.active != paneApply {
					t.Errorf("%s: active pane is %d", label, h.w.active)
				}
				panes := paneNames[:]
				if size[0] < 80 {
					panes = []string{paneNames[paneApply]}
				}
				if h.w.overlay != nil {
					// A question has its own keys: no help hint, no sidebar rule.
					v := h.w.View()
					if got := lipgloss.Height(v); got != size[1] {
						t.Errorf("%s: height = %d, want %d\n%s", label, got, size[1], plain(v))
					}
					for i, line := range strings.Split(plain(v), "\n") {
						if got := lipgloss.Width(line); got > size[0] {
							t.Errorf("%s: line %d is %d cells, want at most %d: %q", label, i, got, size[0], line)
						}
					}
				} else {
					assertFrame(t, label, h.w.View(), size[0], size[1], panes)
				}
				assertKeysWhole(t, label, h)
				// The text under the list is the point of these states: it
				// shows, not just a scroll marker.
				if want, ok := applyShows[st.name]; ok && !strings.Contains(flat(plain(h.w.View())), flat(want)) {
					t.Errorf("%s: the view lacks %q:\n%s", label, want, plain(h.w.View()))
				}
				release()
			}
		}
	}
}

func TestHelpListsTheApplyKeys(t *testing.T) {
	h := applyWs(t, twoRules, &fakeStore{})
	h.press("4")
	body := plain(h.w.helpBody(100, 200))
	for _, want := range []string{
		"1 2 3 4",
		"space selects", "v pastes", "o loads", "d removes", "p plans", "a applies",
		"r plans again", "esc cancels", "ctrl+c or q stops",
	} {
		if !strings.Contains(flat(body), flat(want)) {
			t.Errorf("the Apply help lacks %q:\n%s", want, body)
		}
	}
	h.press("?")
	if !h.w.help {
		t.Fatal("? did not open the help")
	}
}

func TestApplyRowsCannotDriveTheTerminal(t *testing.T) {
	// hostile wraps a marker in the bytes that drive a terminal. Each source has
	// its own marker, so one source rendering cannot vouch for another.
	hostile := func(marker string) string { return "\x1b]0;x\x07\x1b[31m" + marker + "\u009b31m\u202e" }
	escaped := strings.NewReplacer(`\`, `\\`, "\x1b", `\u001b`, "\x07", `\u0007`, "\u009b", `\u009b`, "\u202e", `\u202e`)
	// The view carries its own styling escapes, so name the hostile ones.
	raw := []string{"\x1b]", "\x1b[31m", "\x07", "\u009b", "\u202e"}
	check := func(label, v string, markers ...string) {
		t.Helper()
		for _, bad := range raw {
			if strings.Contains(v, bad) {
				t.Errorf("%s: %q reaches the terminal:\n%q", label, bad, v)
			}
		}
		for _, m := range markers {
			if !strings.Contains(flat(plain(v)), m) {
				t.Errorf("%s: %s is not shown, so its source proves nothing:\n%s", label, m, plain(v))
			}
		}
	}

	// An event value, a test name and a store name, in the list.
	h := applyWs(t, twoRules, &fakeStore{})
	h.w.doc.Tests[0].Name = "n" + hostile("RAWTEST")
	h.w.opts.Apply.Name = func(context.Context) string { return "s" + hostile("RAWSTORE") }
	h.press("4")
	h.run()
	h.pasteEvents(`{"type": "v` + escaped.Replace(hostile("RAWEV")) + `"}`)
	if len(h.eventLabels()) != 2 {
		t.Fatalf("fixture: labels %q", h.eventLabels())
	}
	check("list", h.w.View(), "RAWTEST", "RAWSTORE", "RAWEV")

	// A hint and the resume text, in a failure report.
	f := applyWs(t, teamRules, &fakeStore{failAt: 1})
	f.w.opts.Apply.Hints = func(error) []string { return []string{"h" + hostile("RAWHINT")} }
	f.w.opts.Apply.Resume = func(string, string, int) string { return "resume" + hostile("RAWPATH") }
	f.press("4")
	f.pasteEvents(added(1))
	f.planned()
	f.confirmApply()
	f.settleApply()
	check("failure report", f.w.View(), "RAWHINT", "RAWPATH")

	// A planning error that carries an event's value.
	e := applyWs(t, directRules, &fakeStore{})
	e.press("4")
	user := escaped.Replace(hostile("RAWERR"))
	e.pasteEvents(`{"type": "grant", "doc": "d", "user": "u` + user + `", "limit": "1"}
{"type": "grant", "doc": "d", "user": "u` + user + `", "limit": "2"}`)
	e.press("p")
	e.settleApply()
	if !strings.Contains(e.applyBody(), flat("with a different condition")) {
		t.Fatalf("fixture: no planning error: %s", e.applyBody())
	}
	check("planning error", e.w.View(), "RAWERR")
}
