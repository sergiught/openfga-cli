package tui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

const wantGuidance = "add a tuple or a filter so this rule does something"

func TestANewRuleShowsGuidanceNotAnError(t *testing.T) {
	// A rule made by the add-rule fork is new.
	e := recipeFor(t, "organization.member.added")
	f := newHarness(t, &mapping.Document{}, nil, openFork(false))
	selectCatalogEvent(t, f, e.Type)
	f.press("enter")
	if len(f.s.Doc.Rules) != 1 || !f.s.Doc.Rules[0].New {
		t.Fatalf("a rule from the fork is not new: %+v", f.s.Doc.Rules)
	}

	doc := &mapping.Document{Rules: []mapping.Rule{{Name: "a", New: true}}}
	h := newHarness(t, doc, nil, func(s *Session) component { return newRuleSpoke(s, Focus{}) })
	v := plain(h.view(80, 20))
	if !strings.Contains(v, wantGuidance) || strings.Contains(v, "rule has no tuples yet") {
		t.Fatalf("a new rule shows an error, not guidance:\n%s", v)
	}
	// The preview speaks the same way, and keeps mapper's message for later.
	pv := plain(previewOn(doc, Focus{}).View(80, 20))
	if !strings.Contains(pv, wantGuidance) || strings.Contains(pv, "must contain at least one tuple") {
		t.Fatalf("the preview shows mapper's error for a new rule:\n%s", pv)
	}
	// Check still lists the finding.
	if fs := mapping.Lint(doc, nil); len(fs) == 0 || !slices.ContainsFunc(fs, func(p mapping.Problem) bool { return p.Section == "tuple" }) {
		t.Fatalf("lint dropped the finding: %+v", fs)
	}

	// Leaving the rule once ends the guidance for good.
	h.press("esc")
	if doc.Rules[0].New {
		t.Fatal("leaving the rule left it new")
	}
	h = newHarness(t, doc, nil, func(s *Session) component { return newRuleSpoke(s, Focus{}) })
	v = plain(h.view(80, 20))
	if strings.Contains(v, wantGuidance) || !strings.Contains(v, "rule has no tuples yet") {
		t.Fatalf("a rule that was left still gets guidance:\n%s", v)
	}
	pv = plain(previewOn(doc, Focus{}).View(80, 20))
	if strings.Contains(pv, wantGuidance) || !strings.Contains(pv, "must contain at least one tuple") {
		t.Fatalf("the preview lost mapper's error after the rule was left:\n%s", pv)
	}
}

func TestFgaEscapeHelpShowsTheEncoding(t *testing.T) {
	// The expected text is whatever mapper really makes of the example.
	r := mapping.Rule{Name: "p", When: "true", Tuples: []mapping.Tuple{
		{User: `user:{{ fga_escape("jane@acme.com") }}`, Relation: "member", Object: "organization:1"}}}
	p := mapping.Evaluate(context.Background(), &mapping.Document{Rules: []mapping.Rule{r}}, map[string]any{"type": "x"})
	if len(p.Tuples) != 1 {
		t.Fatalf("probe: %+v", p)
	}
	got := strings.TrimPrefix(p.Tuples[0].User, "user:")
	want := `fga_escape("jane@acme.com") is ` + got + `: ids can't hold @ # : * % or spaces, so they are percent-encoded`
	if !strings.Contains(expressionHelp, want) {
		t.Fatalf("expression help lacks %q", want)
	}
}

func TestHelpSaysTheTupleLimitIsADefault(t *testing.T) {
	s := NewSession(context.Background(), freshRule(), nil)
	_, help := newIterHub(s, Focus{}).help()
	if !strings.Contains(help, "40 tuples by default") || !strings.Contains(help, "the host can change it") {
		t.Fatalf("the iterator help does not call the limit a default:\n%s", help)
	}
	if strings.Contains(help, "at most 40 tuples") {
		t.Fatalf("the iterator help still says at most 40 tuples:\n%s", help)
	}
}

func TestAnIteratorOnlyRuleIsNotCalledEmpty(t *testing.T) {
	doc := &mapping.Document{Rules: []mapping.Rule{{
		Name: "a", New: true,
		Iterator: &mapping.Iterator{Source: "input.ids", As: "it", Tuples: []mapping.Tuple{
			{User: "user:{{ it.id }}", Relation: "member", Object: "organization:1"}}},
	}}}
	h := newHarness(t, doc, nil, func(s *Session) component { return newRuleSpoke(s, Focus{}) })
	v := plain(h.view(80, 20))
	if strings.Contains(v, wantGuidance) || strings.Contains(v, "no tuples yet") {
		t.Fatalf("an iterator-only rule is called empty:\n%s", v)
	}
	if !strings.Contains(v, "none of its own · 1 in the iterator") {
		t.Fatalf("the tuples row hides the iterator's tuples:\n%s", v)
	}
	if pv := plain(previewOn(doc, Focus{}).View(80, 20)); strings.Contains(pv, wantGuidance) {
		t.Fatalf("the preview calls an iterator-only rule empty:\n%s", pv)
	}
	_, help := newRuleSpoke(h.s, Focus{}).help()
	if !strings.Contains(help, "or an iterator's tuples") || strings.Contains(help, "filters or both") {
		t.Fatalf("the rule help misstates what a rule needs:\n%s", help)
	}
}

func TestARuleNamedAfterItsEventSaysItOnce(t *testing.T) {
	doc := &mapping.Document{Rules: []mapping.Rule{{
		Name: "organization.member.added",
		Sample: &mapping.Sample{Label: "organization.member.added",
			Event: map[string]any{"type": "organization.member.added"}},
	}, {
		Name:   "welcome",
		Sample: &mapping.Sample{Label: "organization.member.added", Event: map[string]any{"type": "x"}},
	}}}
	h := newHarness(t, doc, nil, func(s *Session) component { return newRulesHub(s) })
	v := plain(h.view(100, 20))
	if n := strings.Count(v, "organization.member.added"); n != 2 {
		t.Fatalf("the event appears %d times, want 2 (once as a name, once as welcome's event):\n%s", n, v)
	}
}

func TestEventHelpDoesNotSuggestSeveralEventsPerTest(t *testing.T) {
	_, help := (&eventPick{}).help()
	if strings.Contains(help, "the rest explain") || strings.Contains(help, "the other explains") {
		t.Fatalf("the events help still has the old wording:\n%s", help)
	}
}

// Each row is a dialog and its expected way out. A dialog added later adds a row.
func TestEveryDialogNamesItsCancel(t *testing.T) {
	type row struct {
		name string
		want string
		c    component
	}
	s := NewSession(context.Background(), freshRule(), nil)
	rows := []row{
		{"modelDialog", "esc keep current", newModelDialog(s, "", "")},
		{"quitPrompt", "esc cancel", newQuitPrompt(s, "mapping.yaml")},
		{"confirm", "n / esc / enter cancel", newConfirm(s, "t", "q", "go")},
		{"confirmDelete", "n / esc / enter cancel", newConfirmDelete(s, 0)},
		{"confirmTestDelete", "n / esc / enter cancel", newConfirmTestDelete(s, 0)},
		{"saveConflict", "esc cancel", newSaveConflict(s, "mapping.yaml", false)},
		{"recipeView", "esc back", newRecipeView(s, recipeFor(t, "organization.member.added"))},
	}
	for _, r := range rows {
		if !slices.Contains(r.c.Keys(), r.want) {
			t.Errorf("%s keys = %v, want %q", r.name, r.c.Keys(), r.want)
		}
	}

	// The table must cover every dialog in the package.
	impl := regexp.MustCompile(`func \(c \*(\w+)\) isDialog\(\)`)
	files, _ := filepath.Glob("*.go")
	var found []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range impl.FindAllStringSubmatch(string(src), -1) {
			found = append(found, m[1])
		}
	}
	for _, name := range found {
		if !slices.ContainsFunc(rows, func(r row) bool { return r.name == name }) {
			t.Errorf("dialog %s is not in the table", name)
		}
	}
	if len(found) != len(rows) {
		t.Errorf("table has %d rows, package has %d dialogs: %v", len(rows), len(found), found)
	}
}

func TestEnterCancelsADelete(t *testing.T) {
	doc := &mapping.Document{Rules: []mapping.Rule{{Name: "a"}}, Tests: []mapping.TestCase{{Name: "t"}}}
	for name, open := range map[string]func(s *Session) component{
		"rule": func(s *Session) component { return newConfirmDelete(s, 0) },
		"test": func(s *Session) component { return newConfirmTestDelete(s, 0) },
	} {
		h := newHarness(t, doc, nil, open)
		h.press("enter")
		if !h.closed || len(doc.Rules) != 1 || len(doc.Tests) != 1 {
			t.Errorf("%s: closed = %v, rules = %d, tests = %d; enter must cancel and delete nothing",
				name, h.closed, len(doc.Rules), len(doc.Tests))
		}
	}
}
