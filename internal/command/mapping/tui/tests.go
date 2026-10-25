package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// TestResults is how the tests a save writes fare against the document as it
// is now: each outcome by test name. Compiled is false when the mapping does
// not compile, and then nothing ran.
type TestResults struct {
	Compiled bool
	Outcomes map[string]mapping.TestOutcome
}

// RunSavedTests runs the tests a save writes the way `ofga mappings test` runs
// the saved file: rendered, loaded and run through mapper, so a verdict here is
// the verdict there. A mapping-sized run is small and in memory, so the screens
// run it on every change, each evaluation capped at mapping.EvalTimeout like
// the preview's rather than at the CLI's longer timeout.
func RunSavedTests(ctx context.Context, d *mapping.Document) TestResults {
	return runTests(ctx, d, mapping.SavedTests(ctx, d))
}

func runTests(ctx context.Context, d *mapping.Document, tests []mapping.TestCase) TestResults {
	doc := *d
	doc.Tests = tests
	src, err := mapping.Marshal(&doc)
	if err != nil {
		return TestResults{}
	}
	return runSource(ctx, src)
}

// runSource runs the tests of a marshalled mapping. It touches nothing but the
// bytes, so it is safe on a goroutine while the document keeps changing.
func runSource(ctx context.Context, src []byte) TestResults {
	f, err := mapping.LoadBytes("mapping.yaml", src)
	if err != nil {
		return TestResults{}
	}
	run, err := mapping.RunTestsWithTimeout(ctx, f, "", false, mapping.EvalTimeout)
	if err != nil {
		return TestResults{}
	}
	out := TestResults{Compiled: true, Outcomes: make(map[string]mapping.TestOutcome, len(run.Tests))}
	for _, o := range run.Tests {
		out.Outcomes[o.Name] = o
	}
	return out
}

// mark is a test's verdict as the rules hub marks a rule: ✓ passed, ✗ failed,
// – not run because the mapping does not compile.
func (r TestResults) mark(name string) string {
	if !r.Compiled {
		return "–"
	}
	if o, ok := r.Outcomes[name]; ok && o.Passed {
		return icons.I().Check
	}
	return icons.I().Cross
}

// notRun is the heading of a test screen whose mapping does not compile.
const notRun = "The mapping must compile before tests run."

// maxVerdictLines is how many reasons a verdict lists before it says how many
// more there are.
const maxVerdictLines = 8

// verdict is one test's result for a heading, already safe for a terminal. A
// failure lists every reason, one per line.
func (r TestResults) verdict(name string) string {
	if !r.Compiled {
		return notRun
	}
	o, ok := r.Outcomes[name]
	switch {
	case !ok:
		return "Not run."
	case o.Passed:
		return "Passes."
	}
	reasons := failReasons(o)
	for i, line := range reasons {
		reasons[i] = style.SanitizeTerminal(line)
	}
	reasons[0] = "Fails: " + reasons[0]
	// An uncapped verdict squeezes the editor's rows out of the pane.
	if len(reasons) > maxVerdictLines {
		more := len(reasons) - maxVerdictLines
		reasons = append(reasons[:maxVerdictLines], fmt.Sprintf("… and %d more", more))
	}
	return strings.Join(reasons, "\n")
}

// FailReason is the first reason a test failed: an evaluation error first,
// since it is why everything else is missing, then the first tuple or filter
// missing, unexpected or not covered.
func FailReason(o mapping.TestOutcome) string {
	switch {
	case o.Err != nil:
		msg, _, _ := strings.Cut(o.Err.Error(), "\n")
		return "error: " + msg
	case len(o.Missing) > 0:
		return "missing: " + mapping.TupleText(o.Missing[0], nil)
	case len(o.Unexpected) > 0:
		return "unexpected: " + mapping.TupleText(o.Unexpected[0], nil)
	case len(o.MissingFilters) > 0:
		return "missing filter: " + expectedFilterText(o.MissingFilters[0])
	case len(o.UnexpectedFilters) > 0:
		return "unexpected filter: " + expectedFilterText(o.UnexpectedFilters[0])
	case len(o.Uncovered) > 0:
		return "not covered by a filter: " + mapping.TupleText(o.Uncovered[0], nil)
	}
	return "fails"
}

// failReasons is every reason a test failed, the way the CLI prints them:
// FailReason first, then each other tuple or filter missing, unexpected or not
// covered, then the rules that fired (or that none matched). The lines carry the
// test's own values, so a caller sanitizes them.
func failReasons(o mapping.TestOutcome) []string {
	var out []string
	if o.Err != nil {
		out = append(out, FailReason(o))
	}
	for _, t := range o.Missing {
		out = append(out, "missing: "+mapping.TupleText(t, nil))
	}
	for _, t := range o.Unexpected {
		out = append(out, "unexpected: "+mapping.TupleText(t, nil))
	}
	for _, f := range o.MissingFilters {
		out = append(out, "missing filter: "+expectedFilterText(f))
	}
	for _, f := range o.UnexpectedFilters {
		out = append(out, "unexpected filter: "+expectedFilterText(f))
	}
	for _, t := range o.Uncovered {
		out = append(out, "not covered by a filter: "+mapping.TupleText(t, nil))
	}
	if len(out) == 0 {
		out = append(out, FailReason(o))
	}
	switch {
	case len(o.Fired) > 0:
		out = append(out, "fired: "+strings.Join(o.Fired, ", "))
	case o.Err == nil:
		out = append(out, "no rule matched")
	}
	return out
}

// expectedFilterText is the fields a filter sets, then its action.
func expectedFilterText(f language.TupleFilter) string {
	s := filterSummary(f)
	if f.Action != "" {
		s += " " + string(f.Action)
	}
	return strings.TrimSpace(s)
}

// expectSummary says what an own test expects, in a few words.
func expectSummary(tc mapping.TestCase) string {
	if len(tc.ExpectTuples) == 0 && len(tc.ExpectTupleFilters) == 0 && !tc.AssertWritesCoveredByFilter {
		return "expects nothing"
	}
	parts := []string{mapping.Plural(len(tc.ExpectTuples), "tuple")}
	if n := len(tc.ExpectTupleFilters); n > 0 {
		parts = append(parts, mapping.Plural(n, "filter"))
	}
	if tc.AssertWritesCoveredByFilter {
		parts = append(parts, "coverage")
	}
	return strings.Join(parts, ", ")
}

// testList lists every test a save writes: the user's own, then the ones
// generated from the rules' samples, each run against the mapping as it is.
type testList struct {
	s    *Session
	at   Focus
	list *uilist.List
	// saved is what SavedTests returned at the last sync. The user's own tests
	// come first, so row i is own test i while i < len(Doc.Tests).
	saved []mapping.TestCase
	from  map[string]int
	res   TestResults
}

// OpenTests opens the test list on its own, for a host that shows the tests
// apart from the rules hub. at is the rule the preview follows.
func OpenTests(s *Session, at Focus) *Stack { return newStack(s, newTestList(s, at)) }

func newTestList(s *Session, at Focus) *testList {
	c := &testList{s: s, at: at, list: uilist.New()}
	c.sync()
	return c
}

// sync re-derives the saved tests and runs them. SetItems re-applies an active
// filter to the new rows.
func (c *testList) sync() {
	c.saved, c.from = mapping.SavedTestsAndOrigins(c.s.Ctx, c.s.Doc)
	c.res = runTests(c.s.Ctx, c.s.Doc, c.saved)
	items := make([]uilist.Item, 0, len(c.saved))
	for i, tc := range c.saved {
		// Names, events and reasons all come from the user's own payloads.
		title := style.SanitizeTerminal(Glyph(c.res.mark(tc.Name), tc.Name+c.took(tc.Name)))
		desc := style.SanitizeTerminal(c.describe(i, tc))
		items = append(items, uilist.Item{
			TitleText: title,
			DescText:  desc,
			Filter:    title + " " + desc,
			ID:        testID(i, c.own(i)),
			Index:     i,
		})
	}
	c.list.SetItems(items)
}

func testID(i int, own bool) string {
	if own {
		return fmt.Sprintf("own-%d", i)
	}
	return fmt.Sprintf("generated-%d", i)
}

// took is the run time to show after a test's name: nothing until it has run.
func (c *testList) took(name string) string {
	o, ok := c.res.Outcomes[name]
	if !c.res.Compiled || !ok {
		return ""
	}
	if o.Duration < time.Millisecond {
		return " (<1ms)"
	}
	return fmt.Sprintf(" (%s)", o.Duration.Round(time.Millisecond))
}

func (c *testList) own(i int) bool { return i < len(c.s.Doc.Tests) }

func (c *testList) describe(i int, tc mapping.TestCase) string {
	if !c.own(i) {
		return fmt.Sprintf("from %s's sample · follows the mapping", c.ruleName(tc.Name))
	}
	desc := expectSummary(tc)
	if o, ok := c.res.Outcomes[tc.Name]; ok && !o.Passed {
		desc += " · " + FailReason(o)
	}
	return desc
}

// jumpToRule leaves for the rule behind test i: the one its sample made it
// from, or for the user's own test the first rule that fired for its event.
func (c *testList) jumpToRule(i int) {
	tc := c.saved[i]
	rule, ok := c.from[tc.Name]
	if c.own(i) {
		ok = false
		fired := c.res.Outcomes[tc.Name].Fired
		for j, r := range c.s.Doc.Rules {
			if slices.Contains(fired, r.Name) {
				rule, ok = j, true
				break
			}
		}
	}
	if !ok {
		c.s.post(toastMsg{text: "no rule fired for this test"})
		return
	}
	c.s.post(doneMsg{result: Jump{Rule: rule}})
}

// ruleName is the rule a generated test came from.
func (c *testList) ruleName(test string) string {
	if r := c.s.rule(c.from[test]); r != nil {
		return r.Name
	}
	return "its rule"
}

func (c *testList) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case closedMsg:
		if p, ok := msg.result.(Picked); ok {
			c.add(p)
			return c, nil
		}
		c.sync()
		return c, nil
	case tea.KeyPressMsg:
		return c, c.key(msg)
	}
	return c, c.list.Update(msg)
}

func (c *testList) key(k tea.KeyPressMsg) tea.Cmd {
	if c.list.SettingFilter() {
		cmd := c.list.Update(k)
		c.list.ResyncFilter() // A2: enter and d act on the rows the user sees
		return cmd
	}
	switch k.String() {
	case "a":
		if c.s.ReadOnly {
			return nil
		}
		c.s.post(openMsg{c: newEventPick(c.s, c.at, false)})
	case "enter":
		if c.s.ReadOnly {
			return nil
		}
		if it, ok := c.list.Selected(); ok && it.Index < len(c.saved) {
			c.open(it.Index)
		}
	case "d":
		if c.s.ReadOnly {
			return nil
		}
		if it, ok := c.list.Selected(); ok && it.Index < len(c.saved) {
			c.remove(it.Index)
		}
	case "r":
		c.sync()
		c.s.post(toastMsg{text: "ran " + mapping.Plural(len(c.saved), "test")})
	case "e":
		if it, ok := c.list.Selected(); ok && it.Index < len(c.saved) {
			c.jumpToRule(it.Index)
		}
	case "esc":
		c.s.post(doneMsg{})
	default:
		return c.list.Update(k)
	}
	return nil
}

// add makes the picked event an own test expecting what the mapping produces
// for it now. A test the mapping fails on is still added, expecting nothing,
// so the user sees it fail and why.
func (c *testList) add(p Picked) {
	tc, err := mapping.NewTest(c.s.Ctx, c.s.Doc, c.nameFor(p), p.Event)
	c.s.Doc.Tests = append(c.s.Doc.Tests, tc)
	c.s.post(docChangedMsg{})
	if err != nil {
		c.s.post(toastMsg{text: style.SanitizeTerminal("the new test expects nothing: " + err.Error()), err: true})
	}
	c.sync()
	c.s.post(openMsg{c: newTestEditor(c.s, c.at, len(c.s.Doc.Tests)-1)})
}

// nameFor names a new test after its event, unique among what a save writes
// once it is in: a generated test it replaces gives up its name.
func (c *testList) nameFor(p Picked) string {
	d := *c.s.Doc
	d.Tests = append(slices.Clone(d.Tests), mapping.TestCase{Input: p.Event})
	return mapping.UniqueTestName(mapping.SavedTests(c.s.Ctx, &d), p.Label)
}

// open edits test i. A generated test is copied in first: it follows the
// mapping until the user takes it over, and from then on keeps the
// expectations it has.
func (c *testList) open(i int) {
	if !c.own(i) {
		c.s.Doc.Tests = append(c.s.Doc.Tests, c.saved[i])
		c.s.post(docChangedMsg{})
		c.s.post(toastMsg{text: "now your own test: it keeps these expectations when the mapping changes"})
		i = len(c.s.Doc.Tests) - 1
		c.sync()
		c.list.SelectID(testID(i, true))
	}
	c.s.post(openMsg{c: newTestEditor(c.s, c.at, i)})
}

// remove asks before deleting an own test. A generated test is not the
// user's to delete: it is its rule's sample, and goes with it.
func (c *testList) remove(i int) {
	if c.own(i) {
		c.s.post(openMsg{c: newConfirmTestDelete(c.s, i)})
		return
	}
	c.s.post(toastMsg{text: style.SanitizeTerminal(fmt.Sprintf(
		"this test follows %s's sample, and goes when that sample does", c.ruleName(c.saved[i].Name)))})
}

func (c *testList) View(width, height int) string {
	c.list.SetSize(width, height)
	return listOrEmpty(c.list, c.s.ReadOnly, "No tests yet.", "a", "add one")
}

func (c *testList) Keys() []string {
	if c.s.ReadOnly {
		if len(c.saved) == 0 {
			return []string{"esc back", "? help"}
		}
		return append(c.runKeys(), "/ filter", "esc back", "? help")
	}
	if len(c.saved) == 0 {
		return []string{"a add", "esc back", "? help"}
	}
	return append(append([]string{"a add", "↵ open", "d delete"}, c.runKeys()...), "/ filter", "esc back", "? help")
}

// runKeys are the keys that act on the rows without editing them.
func (c *testList) runKeys() []string {
	return []string{"r run", "e rule"}
}

func (c *testList) heading() (string, string) {
	title, sub := c.headingText()
	if names := c.unsampled(); len(names) > 0 {
		sub += "\n\nNo sample yet: " + style.SanitizeTerminal(strings.Join(names, ", ")) +
			". Open the rule's trigger and press ctrl+e to pick one."
	}
	return title, sub
}

// unsampled names the rules that have no sample, so no generated test.
func (c *testList) unsampled() []string {
	var names []string
	for i, r := range c.s.Doc.Rules {
		if hasSample(r) {
			continue
		}
		if r.Name == "" {
			names = append(names, fmt.Sprintf("rule %d", i+1))
			continue
		}
		names = append(names, r.Name)
	}
	return names
}

func (c *testList) headingText() (string, string) {
	switch {
	case !c.res.Compiled && slices.ContainsFunc(c.s.Doc.Rules, hasSample):
		// A sample makes its test only once it evaluates, so the generated
		// tests are gone from the list; say they are coming back.
		return "Tests", notRun + " Generated tests return once it compiles."
	case !c.res.Compiled:
		return "Tests", notRun
	case len(c.saved) == 0:
		return "Tests", "Each test runs one event through the whole mapping."
	}
	passed := 0
	for _, o := range c.res.Outcomes {
		if o.Passed {
			passed++
		}
	}
	return "Tests", fmt.Sprintf("%d of %d pass against the mapping as it is now.", passed, len(c.saved))
}

// hasSample reports whether r has a sample to generate a test from.
func hasSample(r mapping.Rule) bool { return r.Sample != nil && len(r.Sample.Event) > 0 }

func (c *testList) help() (string, string) {
	return "Tests", "A test is one event, and exactly the tuples the mapping must produce for " +
		"it: a tuple missing or extra fails it. Its filters are checked the same way, but only " +
		"when the test lists at least one.\n\n" +
		"Each rule's sample makes a generated test that follows the mapping as you edit it. " +
		"Open one, or add one from any event, and it becomes your own: it keeps its expectations " +
		"until you change them. A save writes both, and `ofga mappings test` runs them.\n\n" +
		"r runs the tests again, and e opens the rule behind the test: the one its sample came from, or the first that fired."
}

func (c *testList) focus() Focus    { return c.at }
func (c *testList) filtering() bool { return c.list.SettingFilter() }

// confirmTestDelete asks before one of the user's own tests is deleted. There
// is no undo.
type confirmTestDelete struct {
	s    *Session
	test int
}

func newConfirmTestDelete(s *Session, i int) *confirmTestDelete {
	return &confirmTestDelete{s: s, test: i}
}

func (c *confirmTestDelete) Update(msg tea.Msg) (component, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	switch k.String() {
	case "y":
		if c.test >= 0 && c.test < len(c.s.Doc.Tests) {
			c.s.Doc.Tests = slices.Delete(c.s.Doc.Tests, c.test, c.test+1)
			c.s.post(docChangedMsg{})
		}
		c.s.post(doneMsg{})
	case "n", "esc", "enter":
		// enter cancels, for the reason confirmDelete gives. Only y deletes.
		c.s.post(doneMsg{})
	}
	return c, nil
}

func (c *confirmTestDelete) View(width, height int) string {
	name := ""
	if c.test >= 0 && c.test < len(c.s.Doc.Tests) {
		name = c.s.Doc.Tests[c.test].Name
	}
	return windowAround(strings.Split(lipgloss.NewStyle().Width(width).Render(
		style.SanitizeTerminal(fmt.Sprintf("Delete test %q?", name))), "\n"), 0, height)
}

func (c *confirmTestDelete) Keys() []string { return []string{"y delete", "n / esc / enter cancel"} }

func (c *confirmTestDelete) heading() (string, string) { return "Delete test", "" }

func (c *confirmTestDelete) isDialog() {}
