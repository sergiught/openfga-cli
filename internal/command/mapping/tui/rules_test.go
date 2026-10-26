package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func openHub(s *Session) component { return newRulesHub(s) }

func threeRules() *mapping.Document {
	return &mapping.Document{Rules: []mapping.Rule{
		{Name: "first", When: `input.type == "a"`},
		{Name: "second", When: `input.type == "b"`},
		{Name: "third", When: `input.type == "c"`},
	}}
}

func ruleNames(d *mapping.Document) string {
	var out []string
	for _, r := range d.Rules {
		out = append(out, r.Name)
	}
	return strings.Join(out, ",")
}

func TestRulesHubListsRulesWithStatus(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "member-added", When: `input.type == "organization.member.added"`,
		Tuples: []mapping.Tuple{
			{User: "user:1", Relation: "member", Object: "organization:1"},
			{User: "user:1", Relation: "member", Object: "organization:1"},
		},
	}}}
	h := newHarness(t, d, nil, openHub)
	v := plain(h.view(72, 12))
	if !strings.Contains(v, "member-added") || !strings.Contains(v, "2 tuples") {
		t.Fatalf("the hub does not list the rule with its count:\n%s", v)
	}
}

func TestTheEmptyHubInvitesTheFirstRule(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openHub)
	if v := plain(h.view(72, 12)); !strings.Contains(v, "No rules yet.") {
		t.Fatalf("the empty state is missing:\n%s", v)
	}
	if keys := strings.Join(topAs[*rulesHub](t, h).Keys(), "  "); strings.Contains(keys, "delete") || strings.Contains(keys, "open") {
		t.Fatalf("the empty hub offers keys with nothing to act on: %s", keys)
	}
}

func TestDeleteRuleConfirms(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openHub)
	h.press("d")
	topAs[*confirmDelete](t, h)
	h.press("n")
	if len(h.s.Doc.Rules) != 1 {
		t.Fatalf("cancelling did not keep the rule: %d", len(h.s.Doc.Rules))
	}
	topAs[*rulesHub](t, h)
	h.press("d", "y")
	if len(h.s.Doc.Rules) != 0 || h.changes == 0 {
		t.Fatalf("rules = %d, changes = %d", len(h.s.Doc.Rules), h.changes)
	}
	topAs[*rulesHub](t, h)
}

// Every other screen trains enter as "proceed", so a user who taps d to find
// out what it does and then enter out of habit must not lose a rule.
func TestEnterDoesNotConfirmADelete(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openHub)
	h.press("d", "enter")
	topAs[*rulesHub](t, h)
	if len(h.s.Doc.Rules) != 1 {
		t.Fatal("enter confirmed a delete")
	}
	h.press("d", "y")
	if len(h.s.Doc.Rules) != 0 {
		t.Fatal("y did not delete")
	}
}

func TestDeletingTheLastRuleLandsOnTheNewLastOne(t *testing.T) {
	h := newHarness(t, threeRules(), nil, openHub)
	c := topAs[*rulesHub](t, h)
	c.list.SelectID("rule-2")
	h.press("d", "y")
	if got := ruleNames(h.s.Doc); got != "first,second" {
		t.Fatalf("rules = %s", got)
	}
	if f := c.focus(); f.Rule != 1 {
		t.Fatalf("focus = %+v, want the new last rule", f)
	}
}

// A2: the selection stayed on the first row while the filter showed another.
func TestAFilteredRuleDeleteRemovesTheRowShown(t *testing.T) {
	h := newHarness(t, threeRules(), nil, openHub)
	h.press("/")
	h.typeText("third")
	h.press("enter", "d", "y")
	if got := ruleNames(h.s.Doc); got != "first,second" {
		t.Fatalf("rules = %s, want third deleted", got)
	}
}

func TestEnterOpensTheRuleUnderTheCursor(t *testing.T) {
	h := newHarness(t, threeRules(), nil, openHub)
	h.press("down", "enter")
	if f := topAs[*ruleSpoke](t, h).focus(); f.Rule != 1 {
		t.Fatalf("spoke focus = %+v, want rule 1", f)
	}
	h.press("esc")
	topAs[*rulesHub](t, h)
}

// The preview follows the highlighted rule.
func TestTheHubsFocusIsTheHighlightedRule(t *testing.T) {
	h := newHarness(t, threeRules(), nil, openHub)
	h.press("down", "down")
	if f := topAs[*rulesHub](t, h).focus(); f.Rule != 2 {
		t.Fatalf("focus = %+v", f)
	}
}

func TestTheHubAsksItsHostForWhatItCannotDo(t *testing.T) {
	for key, want := range map[string]RulesRequest{
		"a": RequestAdd, "m": RequestModel, "s": RequestSave, "esc": RequestQuit, "q": RequestQuit,
	} {
		h := newHarness(t, freshRule(), nil, openHub)
		h.press(key)
		if !h.closed || h.result != want {
			t.Fatalf("%s: closed = %v, result = %v, want %v", key, h.closed, h.result, want)
		}
	}
}

func TestShowRuleOpensTheNewRulesSpoke(t *testing.T) {
	d := threeRules()
	s := NewSession(t.Context(), d, nil)
	st := OpenRules(s)
	d.Rules = append(d.Rules, mapping.Rule{Name: "fourth"})
	ShowRule(st, 3)
	if st.Depth() != 2 {
		t.Fatalf("depth = %d, want the hub and the spoke", st.Depth())
	}
	if f, _ := st.Focus(); f.Rule != 3 {
		t.Fatalf("focus = %+v", f)
	}
	st.Update(key("esc"))
	if f, _ := st.Focus(); f.Rule != 3 {
		t.Fatalf("back on the hub, focus = %+v, want the new rule highlighted", f)
	}
}

// A model warning is advisory: the row is neither passed nor failed.
func TestAModelWarningIsNeitherAPassNorAnError(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "organization.member.added", When: `input.type == "organization.member.added"`,
		Tuples: []mapping.Tuple{{User: "user:1", Relation: "owner", Object: "organization:1"}},
	}}}
	ix := mapping.IndexModel(testModel())
	if len(mapping.Blocking(mapping.Lint(d, ix))) != 0 {
		t.Fatal("this rule is supposed to be savable")
	}
	h := newHarness(t, d, ix, openHub)
	it, ok := topAs[*rulesHub](t, h).list.Selected()
	if !ok {
		t.Fatal("the hub has no rows")
	}
	if strings.HasPrefix(it.TitleText, "✓") || strings.HasPrefix(it.TitleText, "✗") {
		t.Fatalf("the rules list passes or fails an advisory warning: %q", it.TitleText)
	}
}

// A4/A5: a sample that makes no test is a fault on the list, not only in the
// save dialog. Lint passes this rule: the object's missing type is only
// visible once the sample is evaluated.
func TestARuleWhoseSampleMakesNoTestIsMarked(t *testing.T) {
	d := sampleDoc(memberAddedEvent())
	d.Rules[0].Tuples = []mapping.Tuple{{User: "user:1", Relation: "member", Object: "acme"}}
	if len(mapping.Blocking(mapping.Lint(d, nil))) != 0 {
		t.Fatal("this rule is supposed to pass lint")
	}
	h := newHarness(t, d, nil, openHub)
	if it, _ := topAs[*rulesHub](t, h).list.Selected(); !strings.HasPrefix(it.TitleText, "✗") {
		t.Fatalf("row = %q, want the sample fault marked", it.TitleText)
	}
}

// A rule whose when misses its own sample makes no test. That is a warning on
// the list: the row is neither passed nor failed.
func TestARuleWhoseSampleMakesNothingIsWarned(t *testing.T) {
	d := sampleDoc(memberAddedEvent())
	d.Rules[0].When = `input.type == "user.deleted"`
	d.Rules[0].Tuples = []mapping.Tuple{{User: "user:1", Relation: "member", Object: "organization:1"}}
	h := newHarness(t, d, nil, openHub)
	if it, _ := topAs[*rulesHub](t, h).list.Selected(); !strings.HasPrefix(it.TitleText, "!") {
		t.Fatalf("row = %q, want the sample that makes no test warned", it.TitleText)
	}
}

func TestRuleNamesCannotDriveTheTerminal(t *testing.T) {
	d := freshRule()
	d.Rules[0].Name = "\x1b[2J\x1b]0;pwned\arule"
	h := newHarness(t, d, nil, openHub)
	if v := h.view(72, 12); strings.Contains(v, "\x1b[2J") || strings.Contains(v, "\x1b]") || strings.Contains(v, "\a") {
		t.Fatalf("the hub carries terminal controls: %q", v)
	}
}

func TestRuleAddedLandsOnTheNewRule(t *testing.T) {
	h := newHarness(t, threeRules(), nil, openHub)
	h.st.push(&scripted{s: h.s, onKey: map[string][]tea.Msg{"enter": {doneMsg{result: RuleAdded{Index: 2}}}}})
	h.press("enter")
	if c := topAs[*ruleSpoke](t, h); c.at.Rule != 2 {
		t.Fatalf("spoke is on rule %d, want 2", c.at.Rule)
	}
	h.press("esc")
	if f := topAs[*rulesHub](t, h).focus(); f.Rule != 2 {
		t.Fatalf("the hub is on rule %d after closing the spoke, want 2", f.Rule)
	}
}

// m is how the hub reaches the model, so the hub says so, with rules or none.
func TestTheHubAdvertisesTheModelKey(t *testing.T) {
	for name, d := range map[string]*mapping.Document{"empty": {}, "with rules": threeRules()} {
		h := newHarness(t, d, nil, openHub)
		if keys := strings.Join(h.st.Keys(), "  "); !strings.Contains(keys, "m model") {
			t.Fatalf("%s: the hub does not offer m: %s", name, keys)
		}
	}
}

// A rule's own tuples and its iterator's are counted together, one per template.
func TestRuleTuplesCountsEveryKind(t *testing.T) {
	tup := mapping.Tuple{User: "u", Relation: "r", Object: "o"}
	cases := []struct {
		name string
		rule mapping.Rule
		want int
	}{
		{"empty", mapping.Rule{}, 0},
		{"own only", mapping.Rule{Tuples: []mapping.Tuple{tup, tup}}, 2},
		{"iterator only", mapping.Rule{Iterator: &mapping.Iterator{Tuples: []mapping.Tuple{tup}}}, 1},
		{"both", mapping.Rule{Tuples: []mapping.Tuple{tup}, Iterator: &mapping.Iterator{Tuples: []mapping.Tuple{tup, tup}}}, 3},
		{"iterator without tuples", mapping.Rule{Iterator: &mapping.Iterator{}}, 0},
	}
	for _, c := range cases {
		if got := ruleTuples(c.rule); got != c.want {
			t.Errorf("%s: ruleTuples = %d, want %d", c.name, got, c.want)
		}
	}
}
