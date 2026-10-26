package tui

import (
	"context"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// testsMsgs runs cmd (and the batches in it) and returns the badge results it
// produced; timers such as the spinner's are dropped at the deadline.
func testsMsgs(cmd tea.Cmd) []testsRunMsg {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out []testsRunMsg
	for _, m := range collect(ctx, []tea.Cmd{cmd}) {
		if r, ok := m.(testsRunMsg); ok {
			out = append(out, r)
		}
	}
	return out
}

// namedRules is three rules over one event: only the last two fire.
func namedRules() *mapping.Document {
	tup := mapping.Tuple{User: "user:u", Relation: "member", Object: "organization:o"}
	return &mapping.Document{Rules: []mapping.Rule{
		{Name: "never", When: "false", Tuples: []mapping.Tuple{tup}},
		{Name: "second", When: "true", Tuples: []mapping.Tuple{tup}},
		{Name: "third", When: "true", Tuples: []mapping.Tuple{tup}},
	}}
}

func TestTheTestsPaneRunsOnEnter(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{ownTest(t, d, "added", memberAddedEvent())}
	h := freshWs(t, d)
	h.press("3")
	if v := h.view(); !strings.Contains(v, icons.I().Check+" added") {
		t.Fatalf("a test the mapping satisfies should pass:\n%s", v)
	}

	// The pane is not told of this edit: showing it again has to re-run.
	h.w.doc.Rules[0].Tuples[0].Relation = "admin"
	h.press("1", "3")
	if v := h.view(); !strings.Contains(v, icons.I().Cross+" added") {
		t.Fatalf("entering the pane did not re-run the tests:\n%s", v)
	}
}

func TestTheBadgeRunsInTheBackgroundAndDropsStaleRuns(t *testing.T) {
	h := freshWs(t, testsDoc())
	if got := h.w.testsBadge(); got != "–" {
		t.Fatalf("badge before a run = %q", got)
	}

	first := h.w.startTestsRun()
	if h.w.busy != 1 || h.w.tests.Compiled {
		t.Fatalf("busy = %d, tests = %+v: the run should be pending", h.w.busy, h.w.tests)
	}
	second := h.w.startTestsRun()
	if h.w.busy != 2 {
		t.Fatalf("busy = %d, want 2", h.w.busy)
	}
	stale, current := testsMsgs(first), testsMsgs(second)
	if len(stale) != 1 || len(current) != 1 {
		t.Fatalf("results: %d stale, %d current", len(stale), len(current))
	}

	// The superseded run lands last-but-one: it must change nothing.
	h.send(stale[0])
	if h.w.busy != 1 || h.w.tests.Compiled {
		t.Fatalf("stale result applied: busy = %d, tests = %+v", h.w.busy, h.w.tests)
	}
	h.send(current[0])
	if h.w.busy != 0 || h.w.testsBadge() != "1/1" {
		t.Fatalf("busy = %d, badge = %q after the current run", h.w.busy, h.w.testsBadge())
	}
}

// The run is on a goroutine while the UI keeps editing: under -race this fails
// if the cmd holds anything the document shares.
func TestTheBadgeRunSharesNothingWithTheDocument(t *testing.T) {
	h := freshWs(t, testsDoc())
	cmd := h.w.startTestsRun()
	var wg sync.WaitGroup
	wg.Add(1)
	var got []testsRunMsg
	go func() {
		defer wg.Done()
		got = testsMsgs(cmd)
	}()
	r := &h.w.doc.Rules[0]
	for i := range 200 {
		r.Sample.Event["type"] = "x"
		r.Tuples[0].Relation = "admin"
		r.Name = strings.Repeat("n", i%5+1)
	}
	wg.Wait()
	if len(got) != 1 {
		t.Fatalf("got %d results", len(got))
	}
}

func TestEJumpsToTheGeneratedTestsRule(t *testing.T) {
	d := namedRules()
	d.Rules[2].Sample = &mapping.Sample{Label: "s", Event: memberAddedEvent()}
	h := freshWs(t, d)
	h.press("3")
	if !strings.Contains(h.view(), "e rule") {
		t.Fatalf("the workspace should offer e:\n%s", h.view())
	}
	h.press("e")
	if h.w.active != paneRules || h.w.focusRule().Rule != 2 {
		t.Fatalf("active = %v, rule = %d; want the rules pane on rule 2", h.w.active, h.w.focusRule().Rule)
	}
}

func TestEJumpsToTheFirstFiredRule(t *testing.T) {
	d := namedRules()
	d.Tests = []mapping.TestCase{ownTest(t, d, "mine", memberAddedEvent())}
	h := freshWs(t, d)
	h.press("3", "e")
	if h.w.active != paneRules || h.w.focusRule().Rule != 1 {
		t.Fatalf("active = %v, rule = %d; want rule 1, the first to fire", h.w.active, h.w.focusRule().Rule)
	}
}

func TestEWithNoFiredRuleSaysSo(t *testing.T) {
	d := namedRules()
	d.Rules[1].When, d.Rules[2].When = "false", "false"
	d.Tests = []mapping.TestCase{{Name: "idle", Input: memberAddedEvent()}}
	h := freshWs(t, d)
	h.press("3", "e")
	if h.w.active != paneTests || !strings.Contains(h.toastText(), flat("no rule fired for this test")) {
		t.Fatalf("active = %v, toast = %q", h.w.active, h.toastText())
	}
}

func TestRulesWithoutASampleAreListed(t *testing.T) {
	d := testsDoc()
	d.Rules = append(d.Rules, mapping.Rule{Name: "bare", When: "true"}, mapping.Rule{When: "true"})
	h := freshWs(t, d)
	h.press("3")
	want := flat("No sample yet: bare, rule 3. Open the rule's trigger and press ctrl+e to pick one.")
	if v := flat(h.view()); !strings.Contains(v, want) {
		t.Fatalf("no hint in:\n%s", h.view())
	}
}

func TestReloadWhileATestIsOpenRebuildsTheTestsPane(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("3", "enter")
	if _, ok := h.topOf(paneTests).(*testEditor); !ok {
		t.Fatalf("no test editor open: %T\n%s", h.topOf(paneTests), h.view())
	}

	h.rename(0, "mine")
	outside, _, _ := strings.Cut(twoRules, "tests:")
	writeFile(t, h.w.path, outside)
	h.press("ctrl+s", "r", "y")
	if h.w.busy != 1 {
		t.Fatalf("busy = %d after the reload: it should start a badge run", h.w.busy)
	}
	h.run()
	if h.w.busy != 0 || h.w.testsBadge() != "0/0" {
		t.Fatalf("busy = %d, badge = %q after the run", h.w.busy, h.w.testsBadge())
	}

	if _, ok := h.topOf(paneTests).(*testList); !ok || h.w.stacks[paneTests].Depth() != 1 {
		t.Fatalf("the open editor survived the reload: %T at depth %d", h.topOf(paneTests), h.w.stacks[paneTests].Depth())
	}
	if len(h.w.doc.Tests) != 0 {
		t.Fatalf("doc still has %d tests", len(h.w.doc.Tests))
	}
	h.press("enter", "d")
	_ = h.view()
}

var durationRe = regexp.MustCompile(`\((<1|\d+)ms\)`)

func TestRRunsTheTestsAndRowsShowDurations(t *testing.T) {
	d := testsDoc()
	h := newHarness(t, d, nil, openTests)
	h.press("r")
	if len(h.toasts) != 1 || h.toasts[0].Text != "ran 1 test" {
		t.Fatalf("toasts = %+v, want ran 1 test", h.toasts)
	}
	if v := plain(h.view(100, 20)); !strings.Contains(v, "member-added") || !durationRe.MatchString(v) {
		t.Fatalf("rows should show a duration:\n%s", v)
	}
}

func TestEClosesWithAJumpWhereTheHostTakesIt(t *testing.T) {
	d := testsDoc()
	h := newHarness(t, d, nil, openTests)
	h.press("e")
	if j, ok := h.result.(Jump); !h.closed || !ok || j.Rule != 0 {
		t.Fatalf("closed = %v, result = %#v", h.closed, h.result)
	}
}

func TestTheEditorListsEveryReason(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{ownTest(t, d, "added", memberAddedEvent())}
	h := newHarness(t, d, nil, func(s *Session) component { return newTestEditor(s, Focus{}, 0) })
	c := topAs[*testEditor](t, h)

	c.res = TestResults{Compiled: true, Outcomes: map[string]mapping.TestOutcome{"added": {
		Name:              "added",
		Missing:           []language.Tuple{{User: "user:u1", Relation: "member", Object: "organization:\x1b[31mo1"}},
		UnexpectedFilters: []language.TupleFilter{{Relation: "member", Action: "delete"}},
		Uncovered:         []language.Tuple{{User: "user:u2", Relation: "member", Object: "organization:o2"}},
		Fired:             []string{"member-added", "other"},
	}}}
	_, sub := c.heading()
	for _, want := range []string{
		"Fails: missing: user:u1",
		"unexpected filter:",
		"not covered by a filter: user:u2",
		"fired: member-added, other",
	} {
		if !strings.Contains(sub, want) {
			t.Errorf("verdict lacks %q:\n%s", want, sub)
		}
	}
	if strings.Contains(sub, "\x1b") || strings.Count(sub, "\n") != 3 {
		t.Errorf("verdict not one sanitized line per reason:\n%q", sub)
	}

	c.res.Outcomes["added"] = mapping.TestOutcome{Name: "added"}
	if _, sub := c.heading(); !strings.HasSuffix(sub, "no rule matched") {
		t.Errorf("verdict = %q, want it to end with no rule matched", sub)
	}
}

func TestFailReasonStaysTheFirstReason(t *testing.T) {
	tup := language.Tuple{User: "user:a", Relation: "r", Object: "o:1"}
	for name, o := range map[string]mapping.TestOutcome{
		"error":   {Err: os.ErrClosed, Missing: []language.Tuple{tup}},
		"missing": {Missing: []language.Tuple{tup, tup}, Unexpected: []language.Tuple{tup}},
		"nothing": {},
	} {
		if got := failReasons(o)[0]; got != FailReason(o) {
			t.Errorf("%s: first reason %q, FailReason %q", name, got, FailReason(o))
		}
	}
	if got := failReasons(mapping.TestOutcome{Err: os.ErrClosed}); len(got) != 1 {
		t.Errorf("an errored test says %q; the error is why nothing matched", got)
	}
}

func TestTheBadgeTestsTheBytesTheRenderBuilt(t *testing.T) {
	d := testsDoc()
	d.Tests = []mapping.TestCase{ownTest(t, d, "added", memberAddedEvent())}
	h := freshWs(t, d)
	src := h.w.render().src
	if src == nil {
		t.Fatal("render kept no bytes")
	}
	// The document moves on after the render; the badge must test the render,
	// as it would if it were handed the bytes and asked for nothing else.
	h.w.doc.Rules[0].Tuples[0].Relation = "admin"
	for _, m := range testsMsgs(h.w.startTestsRunFrom(src)) {
		h.send(m)
	}
	if got := h.w.testsBadge(); got != "1/1" {
		t.Fatalf("badge = %q: the run read the live document, not the render", got)
	}
}

func TestAVerdictCapsItsLines(t *testing.T) {
	var missing []language.Tuple
	for i := range 12 {
		missing = append(missing, language.Tuple{User: "user:u", Relation: "member", Object: "o:" + string(rune('a'+i))})
	}
	r := TestResults{Compiled: true, Outcomes: map[string]mapping.TestOutcome{"x": {Name: "x", Missing: missing}}}
	lines := strings.Split(r.verdict("x"), "\n")
	if len(lines) != maxVerdictLines+1 || lines[len(lines)-1] != "… and 5 more" {
		t.Fatalf("verdict has %d lines, last %q", len(lines), lines[len(lines)-1])
	}
}

func TestTheWorkspaceStartsABadgeRunOnOpen(t *testing.T) {
	h := loadedWs(t, twoRules)
	if cmd := h.w.Init(); cmd == nil || h.w.busy != 1 {
		t.Fatalf("busy = %d after Init: no badge run started", h.w.busy)
	}
}

func TestAFixedDraftGetsItsSamplesBack(t *testing.T) {
	broken := strings.Replace(twoRules, `when: input.type == "organization.member.added"`, `when: input.type ==`, 1)
	h := loadedWs(t, broken)
	if h.w.doc.Rules[0].Sample != nil {
		t.Fatalf("a draft that does not compile got a sample: %+v", h.w.doc.Rules[0].Sample)
	}
	h.settleDebounce() // what Init's badge run reports: it does not compile
	h.run()
	h.w.doc.Rules[0].When = `input.type == "organization.member.added"`
	h.w.gen++
	h.settleDebounce()
	h.run()
	if s := h.w.doc.Rules[0].Sample; s == nil || len(s.Event) == 0 {
		t.Fatal("the fixed draft's rule has no sample")
	}
}

// t on the rules hub shows the Tests pane, with the keys on it.
func TestTOnTheRulesHubShowsTheTests(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("t")
	if h.w.active != paneTests || h.w.focus != shell.FocusPanel {
		t.Fatalf("active = %d, focus = %d; want the Tests pane with the keys", h.w.active, h.w.focus)
	}
	if _, ok := h.topOf(paneTests).(*testList); !ok {
		t.Fatalf("the Tests pane shows %T, want its list", h.topOf(paneTests))
	}
}

func TestRestoredSamplesReachTheRulesHub(t *testing.T) {
	broken := strings.Replace(twoRules, `when: input.type == "organization.member.added"`, `when: input.type ==`, 1)
	h := loadedWs(t, broken)
	h.settleDebounce()
	h.run()
	if h.w.doc.Rules[0].Sample != nil {
		t.Fatal("the rule had a sample before the fix")
	}
	h.w.doc.Rules[0].When = `input.type == "organization.member.added"`
	h.w.gen++
	h.settleDebounce()
	h.run()
	if h.w.doc.Rules[0].Sample == nil {
		t.Fatal("the sample was not restored")
	}
	if _, ok := h.topOf(paneRules).(*rulesHub); !ok {
		t.Fatalf("top is %T, want the rules hub", h.topOf(paneRules))
	}
	if !strings.Contains(h.view(), h.w.doc.Rules[0].Sample.Label) {
		t.Fatalf("the open hub does not show the restored sample %q:\n%s", h.w.doc.Rules[0].Sample.Label, h.view())
	}
}
