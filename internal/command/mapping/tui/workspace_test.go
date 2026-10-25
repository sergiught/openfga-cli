package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// twoRules is a valid mapping with a comment, two rules and one test, whose
// input becomes the first rule's sample.
const twoRules = `# Org membership sync.
version: "1"
rules:
  - name: member-added
    when: input.type == "organization.member.added"
    tuples:
      - user: "user:{{ fga_escape(input.data.object.user.user_id) }}"
        relation: member
        object: "organization:{{ input.data.object.organization.id }}"
  - name: member-removed
    when: input.type == "organization.member.deleted"
    action: delete
    tuples:
      - user: "user:{{ fga_escape(input.data.object.user.user_id) }}"
        relation: member
        object: "organization:{{ input.data.object.organization.id }}"
tests:
  - name: added
    input: {type: organization.member.added, data: {object: {organization: {id: o1}, user: {user_id: u1}}}}
    expect_tuples:
      - user: "user:u1"
        relation: member
        object: "organization:o1"
        action: write
`

// openSelectedTuples goes from the rules hub to the selected rule's tuple list.
func openSelectedTuples(h *wsHarness) {
	h.press("enter", "down", "down", "down", "down", "down", "enter")
	if _, ok := h.topOf(paneRules).(*tupleList); !ok {
		h.t.Fatalf("top is %T, want the tuple list", h.topOf(paneRules))
	}
}

func TestOpensOnRulesWithTheSidebar(t *testing.T) {
	h := loadedWs(t, twoRules)
	if h.w.active != paneRules || h.w.focus != shell.FocusPanel {
		t.Fatalf("active = %d, focus = %d; want Rules with the panel focused", h.w.active, h.w.focus)
	}
	v := h.view()
	for _, want := range []string{`\[1\].*Rules\s+2\b`, `\[2\].*Check\s+–`, `\[3\].*Tests\s+–`, "mapping.yaml", "no model", "member-added"} {
		if !regexp.MustCompile(want).MatchString(v) {
			t.Fatalf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "unsaved") {
		t.Fatalf("a file just opened reads as unsaved:\n%s", v)
	}
}

func TestNumberKeysAndTabSwitchPanes(t *testing.T) {
	h := loadedWs(t, twoRules)
	for _, step := range []struct {
		key  string
		want pane
	}{
		{"2", paneCheck}, {"3", paneTests}, {"4", paneApply}, {"1", paneRules},
		{"tab", paneCheck}, {"shift+tab", paneRules}, {"shift+tab", paneApply}, {"tab", paneRules},
	} {
		h.press(step.key)
		if h.w.active != step.want {
			t.Fatalf("%s: active = %d, want %d", step.key, h.w.active, step.want)
		}
	}
	// From the sidebar a number shows the pane and hands it the keys.
	h.press("esc")
	h.press("3")
	if h.w.active != paneTests || h.w.focus != shell.FocusPanel {
		t.Fatalf("active = %d, focus = %d; want Tests with the panel focused", h.w.active, h.w.focus)
	}
}

func TestGlobalKeysGoToTheFieldWhileTyping(t *testing.T) {
	h := loadedWs(t, twoRules)
	openSelectedTuples(h)
	h.press("a") // a new tuple, its form open on the first field
	h.typeText("q1")
	h.press("tab")
	h.typeText("?")
	if h.w.active != paneRules || h.w.overlay != nil || h.w.help {
		t.Fatalf("active = %d, overlay = %v, help = %v",
			h.w.active, h.w.overlay != nil, h.w.help)
	}
	f, ok := h.topOf(paneRules).(*tupleForm)
	if !ok {
		t.Fatalf("top is %T, want the tuple form", h.topOf(paneRules))
	}
	vals := f.form.Values()
	if vals[fieldUser] != "q1" || f.form.FocusedIndex() != int(fieldRelation) || vals[fieldRelation] != "?" {
		t.Fatalf("values = %q, focused = %d; want q1 typed, then tab to the relation holding ?",
			vals, f.form.FocusedIndex())
	}
}

func TestCtrlCAsksEvenWhileTyping(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("enter", "enter") // the first rule's trigger form
	if _, ok := h.topOf(paneRules).(*triggerForm); !ok {
		t.Fatalf("top is %T, want the trigger form", h.topOf(paneRules))
	}
	h.typeText("x")
	h.press("ctrl+c")
	h.run()
	if h.quit {
		t.Fatal("ctrl+c quit with an unsaved edit")
	}
	if h.w.overlay == nil {
		t.Fatal("ctrl+c did not ask")
	}
	if _, ok := h.w.overlay.top().(*quitPrompt); !ok {
		t.Fatalf("overlay is %T, want the quit prompt", h.w.overlay.top())
	}
	if !strings.Contains(h.view(), "Unsaved edits") {
		t.Fatalf("the prompt is not on screen:\n%s", h.view())
	}
	h.press("esc")
	if h.w.overlay != nil {
		t.Fatal("esc did not cancel the prompt")
	}
	if f, ok := h.topOf(paneRules).(*triggerForm); !ok || f.form.Values()[0] != "xmember-added" {
		t.Fatalf("after cancel the form is %T %v, want the trigger form as it was", h.topOf(paneRules), f)
	}
}

func TestEscAtTheRootFocusesTheSidebar(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("enter") // the rule's spoke
	h.press("esc")
	if h.w.focus != shell.FocusPanel || h.w.stacks[paneRules].Depth() != 1 {
		t.Fatalf("esc on the spoke: focus = %d, depth = %d; want back on the rules, panel focused",
			h.w.focus, h.w.stacks[paneRules].Depth())
	}
	if keys := strings.Join(h.w.keys(), "|"); !strings.Contains(keys, "esc sidebar") || strings.Contains(keys, "esc quit") {
		t.Fatalf("at the root the keys read %q, want esc to name the sidebar", keys)
	}
	h.press("esc")
	if h.w.focus != shell.FocusSidebar {
		t.Fatal("esc at the root left the panel focused")
	}
	h.press("down")
	if h.w.active != paneCheck || h.w.focus != shell.FocusSidebar {
		t.Fatalf("down in the sidebar: active = %d, focus = %d", h.w.active, h.w.focus)
	}
	h.press("enter")
	if h.w.focus != shell.FocusPanel || h.w.active != paneCheck {
		t.Fatalf("enter in the sidebar: active = %d, focus = %d", h.w.active, h.w.focus)
	}
}

func TestAnEditShowsTheDotAndTheDebounceRunsOnce(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("d", "y") // delete the first rule
	if !strings.Contains(h.view(), "● unsaved") {
		t.Fatalf("an edit shows no dot:\n%s", h.view())
	}
	stale := h.w.gen
	h.press("d", "y") // and the second
	if h.w.gen == stale {
		t.Fatal("the second edit scheduled no debounce of its own")
	}
	sentinel := []mapping.Finding{{Rule: -1, Message: "sentinel"}}
	h.w.findings = sentinel
	h.send(debounceMsg{gen: stale})
	if len(h.w.findings) != 1 || h.w.findings[0].Message != "sentinel" {
		t.Fatalf("a stale tick ran Check: %v", h.w.findings)
	}
	h.settleDebounce()
	if len(h.w.findings) == 1 && h.w.findings[0].Message == "sentinel" {
		t.Fatal("the current tick did not run Check")
	}
	if !h.w.dirty {
		t.Fatal("deleting every rule is not an edit a save would write")
	}
}

func TestANoOpEditLeavesNoDot(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("enter", "enter") // the first rule's trigger form
	h.typeText("x")
	h.press("backspace")
	if !strings.Contains(h.view(), "● unsaved") {
		t.Fatalf("an edit shows no dot until the debounce:\n%s", h.view())
	}
	h.settleDebounce()
	if h.w.dirty || strings.Contains(h.view(), "unsaved") {
		t.Fatalf("an edit undone still reads as unsaved:\n%s", h.view())
	}
}

func TestQuitWhenCleanQuits(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		h := loadedWs(t, twoRules)
		h.press(k)
		h.run()
		if !h.quit || h.w.overlay != nil {
			t.Fatalf("%s: quit = %v, asked = %v", k, h.quit, h.w.overlay != nil)
		}
	}
}

func TestQuitWhenDirtyAsks(t *testing.T) {
	h := freshWs(t, sampleDoc(memberAddedEvent()))
	h.press("q")
	if h.w.overlay == nil || h.quit {
		t.Fatalf("q on unsaved edits: asked = %v, quit = %v", h.w.overlay != nil, h.quit)
	}
	v := h.view()
	for _, want := range []string{"Unsaved edits", "Save them before quitting?", "s save · d discard · esc cancel"} {
		if !strings.Contains(v, want) {
			t.Fatalf("the prompt lacks %q:\n%s", want, v)
		}
	}
	h.press("enter")
	h.run()
	if h.w.overlay == nil || h.quit {
		t.Fatal("enter answered the prompt")
	}
	h.press("esc")
	if h.w.overlay != nil || h.quit {
		t.Fatalf("esc: asked = %v, quit = %v; want cancelled", h.w.overlay != nil, h.quit)
	}
	h.press("q", "d")
	h.run()
	if !h.quit {
		t.Fatal("d did not quit")
	}
}

func TestAFilterResultReachesItsPaneAfterSwitching(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.w.check.set([]mapping.Finding{
		{Rule: -1, Message: "first finding"},
		{Rule: -1, Message: "second finding"},
	}, "", "", nil)
	h.press("2", "/")
	h.typeText("first")
	h.press("enter")
	h.press("1", "/")
	h.typeText("removed")
	h.press("enter", "2")
	// The Rules filter's matches arrive while Check is on screen. Applied to
	// Check's list, they would replace its rows with rules.
	h.run()
	v := h.view()
	if !strings.Contains(v, "first finding") || strings.Contains(v, "member-removed") {
		t.Fatalf("Check shows another pane's matches:\n%s", v)
	}
	rules := h.w.stacks[paneRules].items[0].(*rulesHub)
	if got := rules.list.Model.VisibleItems(); len(got) != 1 || got[0].(interface{ Title() string }).Title() != "✓ member-removed" {
		t.Fatalf("the rules filter shows %v, want member-removed alone", got)
	}
}

func TestHelpListsGlobalAndComponentKeys(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("?")
	v := h.view()
	for _, want := range []string{"Keys", "ctrl+s", "save", "1 2 3", "q / ctrl+c", "RULES", "A rule is one kind of event"} {
		if !strings.Contains(v, want) {
			t.Fatalf("the help lacks %q:\n%s", want, v)
		}
	}
	for _, k := range []string{"?", "esc", "q", "enter"} {
		h.w.help = true
		h.press(k)
		h.run()
		if h.w.help || h.quit {
			t.Fatalf("%s: help = %v, quit = %v; want the help closed", k, h.w.help, h.quit)
		}
	}
}

func TestJumpOpensTheSection(t *testing.T) {
	d := sampleDoc(memberAddedEvent())
	d.Rules = append(d.Rules, mapping.Rule{Name: "second", When: "true"})
	for section, want := range map[string]string{
		"trigger":   "*tui.triggerForm",
		"action":    "*tui.actionPick",
		"variables": "*tui.varList",
		"iterator":  "*tui.iterHub",
		"filters":   "*tui.filterList",
		"tuple":     "*tui.tupleList",
		"":          "*tui.ruleSpoke",
	} {
		h := freshWs(t, d)
		h.w.check.set([]mapping.Finding{{Rule: 1, Section: section, Message: "x"}}, "", "", nil)
		h.press("2", "enter")
		top := h.topOf(paneRules)
		if h.w.active != paneRules || fmt.Sprintf("%T", top) != want {
			t.Fatalf("%q: active = %d, top = %T; want Rules with %s", section, h.w.active, top, want)
		}
		if f, ok := top.(focused); !ok || f.focus().Rule != 1 || h.w.preview.at.Rule != 1 {
			t.Fatalf("%q: the editor or the preview is not on rule 2", section)
		}
	}

	// A read-only file opens the rule but no editor, as its spoke would.
	f, err := mapping.LoadDraftBytes("ro.yaml", []byte("version: \"1\"\nrules:\n"+
		"  - name: r\n    tuples:\n      - &t {user: \"user:1\", relation: m, object: \"team:1\"}\n"))
	if err != nil || f.Editable {
		t.Fatalf("fixture: err = %v, editable = %v", err, f != nil && f.Editable)
	}
	h := newWsHarness(t, opened{path: "ro.yaml", file: f}, Options{})
	h.w.check.set([]mapping.Finding{{Rule: 0, Section: "tuple", Message: "x"}}, "", "", nil)
	h.press("2", "enter")
	if _, ok := h.topOf(paneRules).(*ruleSpoke); !ok {
		t.Fatalf("read-only jump: top is %T, want the rule's spoke", h.topOf(paneRules))
	}
}

func TestPreviewShowsBesideAtWideWidthsOnly(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.resize(160, 40)
	if v := h.view(); !strings.Contains(v, "preview") {
		t.Fatalf("no preview at 160 columns:\n%s", v)
	}
	h.press("d") // the delete confirm is a dialog: it has the pane
	if v := h.view(); strings.Contains(v, "preview") {
		t.Fatalf("the preview shows beside a dialog:\n%s", v)
	}
	h.press("n")
	h.resize(120, 40)
	if v := h.view(); strings.Contains(v, "preview") {
		t.Fatalf("a preview at 120 columns, where the main pane is under 100:\n%s", v)
	}
	h.resize(100, 16)
	for i, line := range strings.Split(h.w.View(), "\n") {
		if w := len([]rune(plain(line))); w > 100 {
			t.Fatalf("line %d is %d wide at 100 columns", i, w)
		}
	}
}

func TestAnEmptyFilterKeepsThePreview(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("/")
	h.typeText("removed")
	if h.w.preview.at.Rule != 1 {
		t.Fatalf("the filter is on member-removed, the preview on rule %d", h.w.preview.at.Rule)
	}
	h.typeText("zzz")
	if _, ok := h.w.stacks[paneRules].items[0].(*rulesHub).list.Selected(); ok {
		t.Fatal("fixture: the filter still matches a rule")
	}
	if h.w.preview.at.Rule != 1 || h.w.focusRule().Rule != 1 {
		t.Fatalf("a filter matching nothing moved the preview to rule %d", h.w.preview.at.Rule)
	}
}

func TestCheckFollowsTheScreenNotTheDisk(t *testing.T) {
	h := loadedWs(t, twoRules)
	if len(h.w.findings) != 0 {
		t.Fatalf("fixture: a valid file has findings %v", h.w.findings)
	}
	disk, err := os.ReadFile(h.w.path)
	if err != nil {
		t.Fatal(err)
	}
	openSelectedTuples(h)
	h.press("d") // the rule's only tuple: a rule needs tuples or filters
	h.settleDebounce()
	if len(h.w.findings) == 0 {
		t.Fatal("Check did not see the unsaved edit")
	}
	h.press("esc", "esc", "2")
	if v := h.view(); !strings.Contains(v, "member-added") || !strings.Contains(v, "✗ 1") {
		t.Fatalf("Check does not show the edit's finding:\n%s", v)
	}
	if now, _ := os.ReadFile(h.w.path); string(now) != string(disk) {
		t.Fatal("checking wrote the file")
	}
}

func TestOpenPathKeepsABrokenFileOpen(t *testing.T) {
	dir := t.TempDir()
	if _, err := openPath(context.Background(), filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("a missing file opened")
	}
	broken := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(broken, []byte("version: \"1\"\nrules: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := openPath(context.Background(), broken)
	if err != nil || len(o.loadErr) == 0 || o.file != nil {
		t.Fatalf("broken YAML: err = %v, findings = %v", err, o.loadErr)
	}
	h := newWsHarness(t, o, Options{})
	if h.w.active != paneCheck || len(h.w.findings) == 0 {
		t.Fatalf("a broken file opens on %d with findings %v, want Check with the load's", h.w.active, h.w.findings)
	}
	h.press("ctrl+s")
	if h.w.toasts.View() == "" {
		t.Fatal("ctrl+s on a broken file said nothing")
	}

	h = loadedWs(t, twoRules)
	if s := h.w.doc.Rules[0].Sample; s == nil || s.Label != "test added" {
		t.Fatalf("the first rule's sample is %+v, want the test's input", s)
	}
}

func TestConfirmOnlyYConfirms(t *testing.T) {
	open := func(s *Session) component { return newConfirm(s, "Reload", "Discard your edits?", "reload") }
	for k, want := range map[string]bool{"y": true, "n": false, "esc": false, "enter": false} {
		h := newHarness(t, &mapping.Document{}, nil, open)
		h.press(k)
		if !h.closed || h.result != want {
			t.Fatalf("%s: closed = %v, result = %v, want %v", k, h.closed, h.result, want)
		}
	}
	h := newHarness(t, &mapping.Document{}, nil, open)
	if keys := strings.Join(h.st.Keys(), " · "); keys != "y reload · n / esc / enter cancel" {
		t.Fatalf("keys = %q", keys)
	}
	if v := plain(h.view(40, 5)); !strings.Contains(v, "Discard your edits?") {
		t.Fatalf("view = %q", v)
	}
}

func TestRebuildStartsEveryPaneAfresh(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("down")
	openSelectedTuples(h) // on the second rule
	h.press("enter")      // its tuple form
	// What a reload brings: a document the open editors no longer match.
	h.w.doc.Rules = h.w.doc.Rules[:1]
	h.w.doc.Rules[0].Tuples = nil
	h.w.rebuild()
	if d := h.w.stacks[paneRules].Depth(); d != 1 {
		t.Fatalf("the Rules pane kept %d components open", d)
	}
	if h.w.sess.rule(h.w.preview.at.Rule) == nil {
		t.Fatalf("the preview is on rule %d of %d", h.w.preview.at.Rule, len(h.w.doc.Rules))
	}
	if len(h.w.findings) == 0 || h.w.stacks[paneCheck].top() != h.w.check {
		t.Fatalf("Check was not re-run into the new pane: %v", h.w.findings)
	}
	if !strings.Contains(h.view(), "member-added") {
		t.Fatalf("the rules list does not show the document:\n%s", h.view())
	}
}

// The preview's keys are not text, so a form taking text does not stop them.
func TestPreviewKeysWorkWhileTyping(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.resize(160, 16) // the file is longer than the preview's rows
	openSelectedTuples(h)
	h.press("enter") // the tuple form
	if !h.w.typing() || !h.w.previewShown() {
		t.Fatalf("fixture: typing = %v, preview shown = %v", h.w.typing(), h.w.previewShown())
	}
	before := slices.Clone(h.topOf(paneRules).(*tupleForm).form.Values())
	h.view() // the preview measures how far it scrolls as it draws
	h.press("pgdown")
	if h.w.preview.docOff == 0 {
		t.Fatal("pgdown in a form did not page the preview")
	}
	h.press("ctrl+t")
	if h.w.preview.mode != previewPayload {
		t.Fatal("ctrl+t in a form did not switch the preview to the event")
	}
	if after := h.topOf(paneRules).(*tupleForm).form.Values(); !slices.Equal(after, before) {
		t.Fatalf("a preview key reached the form: %q, was %q", after, before)
	}
}

// The status bar cuts its left side before its keys, so at 80 columns the
// dot there is cut off. The title row always has room for it.
func TestTheUnsavedDotShowsInTheTitle(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.resize(80, 24)
	titleRow := func() string { return strings.SplitN(h.view(), "\n", 2)[0] }
	h.press("enter", "enter", "end") // the end of the first rule's name
	h.typeText("x")
	h.press("esc", "esc")
	if row := titleRow(); !strings.Contains(row, "● Rules") {
		t.Fatalf("the title row of an edited file reads %q", row)
	}
	h.press("enter", "enter", "end", "backspace", "esc", "esc")
	h.settleDebounce()
	if row := titleRow(); strings.Contains(row, "●") {
		t.Fatalf("the edit is undone, yet the title row reads %q", row)
	}
}

// Whatever the overlay changed, the pane under it shows once it closes.
func TestClosingAnOverlayResumesThePane(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.send(nil) // the hub is on screen
	h.cmds = append(h.cmds, h.w.openOverlay(newStack(h.w.sess, newConfirm(h.w.sess, "Rename", "Rename?", "rename")),
		func(result any) tea.Cmd {
			if result == true {
				h.w.doc.Rules[0].Name = "renamed"
			}
			return nil
		}))
	h.press("y")
	if v := h.view(); !strings.Contains(v, "renamed") {
		t.Fatalf("the rules hub does not show what the overlay changed:\n%s", v)
	}
}

func TestALoadErrorEditsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.yaml")
	if err := os.WriteFile(path, []byte("version: \"1\"\nrules: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := openPath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	h := newWsHarness(t, o, Options{})
	h.press("3", "a", "enter")
	if len(h.w.doc.Tests) != 0 || len(h.w.doc.Rules) != 0 || h.w.dirty {
		t.Fatalf("tests = %d, rules = %d, dirty = %v; want nothing changed",
			len(h.w.doc.Tests), len(h.w.doc.Rules), h.w.dirty)
	}
	h.press("q")
	h.run()
	if h.w.overlay != nil || !h.quit {
		t.Fatalf("q on a broken file: asked = %v, quit = %v", h.w.overlay != nil, h.quit)
	}
}

func TestTaggedLetsQuitThrough(t *testing.T) {
	if _, ok := tagged(paneRules, 0, tea.Quit)().(tea.QuitMsg); !ok {
		t.Fatal("a pane's quit came back tagged, where the runtime cannot see it")
	}
	if _, ok := tagged(paneRules, 0, func() tea.Msg { return closedMsg{} })().(paneMsg); !ok {
		t.Fatal("a pane's own message came back untagged")
	}
}

// pickEvent filters the event pick on top of the Rules pane to typ and picks it.
func pickEvent(h *wsHarness, typ string) {
	h.t.Helper()
	if _, ok := h.topOf(paneRules).(*eventPick); !ok {
		h.t.Fatalf("top is %T, want the event pick", h.topOf(paneRules))
	}
	h.press("/")
	h.typeText(typ)
	h.press("enter", "enter") // accept the filter, pick the highlighted event
}

// a on the hub opens the add-rule fork. Its recipe shows no preview, since its
// rule does not exist yet; m asks for a model over it and leaves it where it
// was, whatever the answer; using it lands on the new rule with the preview;
// and ctrl+e there changes that rule's sample rather than adding a rule.
func TestAddingARuleFromTheHubLandsOnTheNewRule(t *testing.T) {
	for name, c := range map[string]struct {
		answer []string
		model  string // the model label after it
	}{
		"esc":      {[]string{"esc"}, noModel},
		"no model": {[]string{"down", "down", "enter"}, noModel},
		"store":    {[]string{"enter"}, "store model 01J0 ("},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mapping.yaml")
			writeFile(t, path, twoRules)
			o, err := openPath(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			h := newWsHarness(t, o, storeOpts(func(context.Context) (*openfga.AuthorizationModel, error) {
				return testModel(), nil
			}))
			h.resize(160, 40)
			before := slices.Clone(h.w.doc.Rules)

			h.press("a")
			pickEvent(h, "organization.member.added")
			if _, ok := h.topOf(paneRules).(*recipeView); !ok {
				t.Fatalf("top is %T, want the recipe", h.topOf(paneRules))
			}
			if h.w.previewShown() {
				t.Fatalf("a preview beside the recipe of a rule that does not exist yet:\n%s", h.view())
			}
			h.press("m")
			if h.w.overlay == nil {
				t.Fatal("m on the recipe did not ask for a model")
			}
			h.press(c.answer...)
			h.run()
			if h.w.overlay != nil || h.w.busy != 0 || !strings.HasPrefix(h.w.model.label, c.model) {
				t.Fatalf("overlay = %v, busy = %d, model = %q; want the dialog answered and %q in use",
					h.w.overlay != nil, h.w.busy, h.w.model.label, c.model)
			}
			if _, ok := h.topOf(paneRules).(*recipeView); !ok || len(h.w.doc.Rules) != 2 {
				t.Fatalf("after the model dialog the top is %T with %d rules, want the recipe, unused", h.topOf(paneRules), len(h.w.doc.Rules))
			}

			h.press("enter") // use it
			if n := len(h.w.doc.Rules); n != 3 || !slices.EqualFunc(h.w.doc.Rules[:2], before, func(a, b mapping.Rule) bool { return a.Name == b.Name }) {
				t.Fatalf("rules = %d, want the two there were and the recipe's", n)
			}
			spoke, ok := h.topOf(paneRules).(*ruleSpoke)
			if !ok || spoke.at.Rule != 2 || h.w.stacks[paneRules].Depth() != 2 {
				t.Fatalf("top is %T at depth %d, want the new rule's spoke over the hub", h.topOf(paneRules), h.w.stacks[paneRules].Depth())
			}
			if h.w.preview.at.Rule != 2 || !h.w.previewShown() || !h.w.dirty {
				t.Fatalf("preview on rule %d, shown = %v, dirty = %v; want it on the new rule", h.w.preview.at.Rule+1, h.w.previewShown(), h.w.dirty)
			}

			h.press("enter", "ctrl+e") // the trigger, then its event
			pickEvent(h, "organization.created")
			if _, ok := h.topOf(paneRules).(*triggerForm); !ok {
				t.Fatalf("top is %T, want the trigger form back", h.topOf(paneRules))
			}
			if n := len(h.w.doc.Rules); n != 3 {
				t.Fatalf("ctrl+e on the new rule left %d rules, want 3", n)
			}
			if s := h.w.doc.Rules[2].Sample; s == nil || s.Label != "organization.created" {
				t.Fatalf("sample = %+v, want the event ctrl+e picked, on the rule it was pressed on", s)
			}
			if !strings.Contains(h.w.preview.payload, `"organization.created"`) {
				t.Fatalf("the preview still shows the old sample:\n%s", h.w.preview.payload)
			}
		})
	}
}

// An edit in a form reaches the preview at once and Check after the debounce,
// with the form still open: nobody has to leave it to see what it did.
func TestTypingInAFormReachesThePreviewAndCheck(t *testing.T) {
	h := modelWs(t, twoTypeModel, Options{})
	h.press("enter") // check against the model.fga beside the mapping
	h.resize(160, 40)
	openSelectedTuples(h)
	h.press("enter") // the tuple's form, opening on its user
	if _, ok := h.topOf(paneRules).(*tupleForm); !ok {
		t.Fatalf("top is %T, want the tuple form", h.topOf(paneRules))
	}
	h.press("tab", "tab") // user, relation, then the object
	h.typeText("x")
	if pane := plain(h.w.preview.View(100, 40)); !strings.Contains(pane, "xorganization:org_1234567890abcdef") {
		t.Fatalf("the preview did not follow the keystroke:\n%s", pane)
	}
	h.settleDebounce()
	if !slices.ContainsFunc(h.w.findings, func(f mapping.Finding) bool { return strings.Contains(f.Message, "xorganization") }) {
		t.Fatalf("Check did not see the type the model lacks: %v", h.w.findings)
	}
	if _, ok := h.topOf(paneRules).(*tupleForm); !ok {
		t.Fatalf("top is %T, want the form still open", h.topOf(paneRules))
	}
}

// Inside the iterator's tuples the preview is on the iterator's item, and back
// on its hub it is on the event again.
func TestTheIteratorFocusReachesThePreview(t *testing.T) {
	h := freshWs(t, recipeDoc(t, "user.updated"))
	h.press("enter", "down", "down", "down", "enter") // the rule's Iterator row
	if _, ok := h.topOf(paneRules).(*iterHub); !ok {
		t.Fatalf("top is %T, want the iterator hub", h.topOf(paneRules))
	}
	if label, _ := h.w.preview.Switch(); label != "input payload" {
		t.Fatalf("on the iterator hub the preview offers %q, want the event's payload", label)
	}
	h.press("down", "down", "enter") // its Tuples row
	if label, _ := h.w.preview.Switch(); !h.w.preview.at.InIter || label != "identity payload" {
		t.Fatalf("in the iterator's tuples the preview is at %+v offering %q, want the item's payload", h.w.preview.at, label)
	}
	h.press("esc")
	if label, _ := h.w.preview.Switch(); h.w.preview.at.InIter || label != "input payload" {
		t.Fatalf("back on the iterator hub the preview is at %+v offering %q, want the event's", h.w.preview.at, label)
	}
}

// The help scrolls on the arrows, and an arrow does not close it.
func TestHelpScrollsOnTheArrows(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.resize(80, 24)
	h.press("?")
	first := h.view()
	if !strings.Contains(first, "more") {
		t.Fatalf("the help fits, so scrolling it proves nothing:\n%s", first)
	}
	for _, down := range []string{"down", "j"} {
		h.press(down)
		if !h.w.help || h.view() == first {
			t.Fatalf("%s: help = %v; want it open and scrolled", down, h.w.help)
		}
		up := map[string]string{"down": "up", "j": "k"}[down]
		h.press(up)
		if !h.w.help || h.view() != first {
			t.Fatalf("%s: help = %v; want it open and back at its top", up, h.w.help)
		}
	}
}

// ctrl+c quits from wherever the keys are: a dialog over the workspace, the
// help, a dialog in a pane, a list filter taking text, a picker in a form.
func TestCtrlCQuitsFromEveryLayer(t *testing.T) {
	for name, open := range map[string]func(h *wsHarness) bool{
		"the model dialog": func(h *wsHarness) bool {
			h.press("m")
			return h.w.overlay != nil
		},
		"the help": func(h *wsHarness) bool {
			h.press("?")
			return h.w.help
		},
		"a delete confirm": func(h *wsHarness) bool {
			h.press("d")
			_, ok := h.topOf(paneRules).(*confirmDelete)
			return ok
		},
		"a path filter": func(h *wsHarness) bool {
			openSelectedTuples(h)
			h.press("enter", "ctrl+p", "/")
			h.typeText("user")
			p, ok := h.topOf(paneRules).(*pathPicker)
			return ok && p.filtering() && h.w.typing()
		},
		"the form's model picker": func(h *wsHarness) bool {
			h.cmds = append(h.cmds, h.w.modelChanged(mapping.IndexModel(testModel()), "test model", ""))
			openSelectedTuples(h)
			h.press("enter", "ctrl+o")
			f, ok := h.topOf(paneRules).(*tupleForm)
			return ok && f.pick != nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := loadedWs(t, twoRules)
			if !open(h) {
				t.Fatalf("fixture: %s is not open:\n%s", name, h.view())
			}
			h.press("ctrl+c")
			h.run()
			if !h.quit {
				t.Fatalf("ctrl+c did not quit from %s", name)
			}
		})
	}
}

// What a pane's Resume returns as it comes back on screen reaches the runtime:
// a component that starts work on resume would otherwise wait for a result
// that never comes.
func TestShowingAPaneSendsWhatItsResumeReturns(t *testing.T) {
	h := loadedWs(t, twoRules)
	root := newScripted(h.w.sess, "root")
	root.ret = func() tea.Msg { return "resumed" }
	h.w.stacks[paneTests] = newStack(h.w.sess, root)
	h.press("3")
	h.run()
	if !slices.Contains(root.got, tea.Msg("resumed")) {
		t.Fatalf("the pane got %v, want what its Resume returned", root.got)
	}
}

func TestAReloadMakesAnEarlierDebounceTickStale(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("d", "y")
	stale := h.w.gen
	h.cmds = append(h.cmds, h.w.reload())
	h.run()
	sentinel := []mapping.Finding{{Rule: -1, Message: "sentinel"}}
	h.w.findings = sentinel
	h.send(debounceMsg{gen: stale})
	if len(h.w.findings) != 1 || h.w.findings[0].Message != "sentinel" {
		t.Fatalf("an edit's tick ran after the reload: %v", h.w.findings)
	}
}

// resumeSpy counts how often the stack tells its top component it is back.
type resumeSpy struct {
	component
	n *int
}

func (s resumeSpy) Update(msg tea.Msg) (component, tea.Cmd) {
	if _, ok := msg.(closedMsg); ok {
		*s.n++
	}
	c, cmd := s.component.Update(msg)
	return resumeSpy{c, s.n}, cmd
}

func TestAModelChangeResumesOnlyTheShownPane(t *testing.T) {
	h := loadedWs(t, twoRules)
	var n [paneCount]int
	for p, st := range h.w.stacks {
		top := len(st.items) - 1
		st.items[top] = resumeSpy{st.items[top], &n[p]}
	}
	reset := func() { n = [paneCount]int{} }
	// The overlay first: the dialog's rows move once a model is in use.
	for _, c := range []struct {
		name   string
		change func()
	}{
		{"an overlay choice", func() {
			h.w.openModelDialog()
			h.press("enter") // no model is preselected without a store
			if h.w.overlay != nil {
				t.Fatalf("the dialog stayed open:\n%s", h.view())
			}
		}},
		{"a direct change", func() {
			h.cmds = append(h.cmds, h.w.modelChanged(mapping.IndexModel(testModel()), "test model", ""))
		}},
	} {
		name, change := c.name, c.change
		reset()
		change()
		if n[h.w.active] != 1 {
			t.Fatalf("%s: the shown pane resumed %d times, want 1", name, n[h.w.active])
		}
		for p := range n {
			if pane(p) != h.w.active && n[p] != 0 {
				t.Fatalf("%s: pane %d resumed %d times, want 0 until shown", name, p, n[p])
			}
		}
		for p := range n {
			before := n[p]
			h.cmds = append(h.cmds, h.w.switchTo(pane(p)))
			if n[p] != before+1 {
				t.Fatalf("%s: showing pane %d did not resume it", name, p)
			}
		}
		h.w.switchTo(paneRules)
	}
}

// inbox is a component that remembers the messages it was handed.
type inbox struct{ got []tea.Msg }

func (c *inbox) Update(msg tea.Msg) (component, tea.Cmd) {
	c.got = append(c.got, msg)
	return c, nil
}
func (c *inbox) View(int, int) string { return "" }
func (c *inbox) Keys() []string       { return nil }

type staleProbe struct{}

func (c *inbox) probes() (n int) {
	for _, m := range c.got {
		if _, ok := m.(staleProbe); ok {
			n++
		}
	}
	return n
}

func probeCmd() tea.Cmd { return func() tea.Msg { return staleProbe{} } }

// closer posts doneMsg and returns a probe command on any message.
type closer struct{ s *Session }

func (c *closer) Update(tea.Msg) (component, tea.Cmd) {
	c.s.post(doneMsg{})
	return c, probeCmd()
}
func (c *closer) View(int, int) string { return "" }
func (c *closer) Keys() []string       { return nil }

func TestClosingOverlayCommandNotDeliveredToItsSuccessor(t *testing.T) {
	h := loadedWs(t, twoRules)
	rec := &inbox{}
	h.w.openOverlay(newStack(h.w.sess, &closer{s: h.w.sess}), func(any) tea.Cmd {
		return h.w.openOverlay(newStack(h.w.sess, rec), nil)
	})
	var deliver func(tea.Cmd)
	deliver = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch m := c().(type) {
		case tea.BatchMsg:
			for _, b := range m {
				deliver(b)
			}
		case paneMsg:
			h.w.Update(m)
		}
	}
	deliver(h.w.route(overlayPane, tea.KeyPressMsg{Code: 'x', Text: "x"}))
	if rec.probes() != 0 {
		t.Fatalf("the closing overlay's last message reached its successor %d times", rec.probes())
	}
}

func TestRebuildDropsMessagesIssuedBefore(t *testing.T) {
	h := loadedWs(t, twoRules)
	before := h.w.tag(paneRules, probeCmd())
	h.w.rebuild()
	after := h.w.tag(paneRules, probeCmd())
	rec := &inbox{}
	h.w.pushOn(paneRules, rec)
	h.w.Update(before())
	if rec.probes() != 0 {
		t.Fatal("a message issued before the rebuild reached the rebuilt stack")
	}
	h.w.Update(after())
	if rec.probes() != 1 {
		t.Fatalf("a message issued after the rebuild was delivered %d times, want 1", rec.probes())
	}
}

func TestReplacedOverlayDropsMessagesIssuedBefore(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.w.openOverlay(newStack(h.w.sess, &inbox{}), nil)
	before := h.w.tag(overlayPane, probeCmd())
	rec := &inbox{}
	h.w.openOverlay(newStack(h.w.sess, rec), nil)
	after := h.w.tag(overlayPane, probeCmd())
	h.w.Update(before())
	if rec.probes() != 0 {
		t.Fatal("a message for the replaced overlay reached its successor")
	}
	h.w.Update(after())
	if rec.probes() != 1 {
		t.Fatalf("a message for the current overlay was delivered %d times, want 1", rec.probes())
	}
}
