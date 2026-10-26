package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func openFork(paste bool) func(s *Session) component {
	return func(s *Session) component { return newRuleFork(s, paste) }
}

// selectCatalogEvent filters the pick to one event and opens it.
func selectCatalogEvent(t *testing.T, h *harness, typ string) {
	t.Helper()
	h.press("/")
	h.typeText(typ)
	h.press("enter", "enter") // accept the filter, open the highlighted event
	if _, ok := h.top().(*recipeView); !ok {
		t.Fatalf("top is %T, want the recipe", h.top())
	}
}

func TestForkOpensOnThePickOrThePasteBox(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openFork(false))
	if _, ok := h.top().(*eventPick); !ok {
		t.Fatalf("top is %T, want the event pick", h.top())
	}
	h = newHarness(t, &mapping.Document{}, nil, openFork(true))
	if _, ok := h.top().(*eventPaste); !ok {
		t.Fatalf("top is %T, want the paste box", h.top())
	}
}

func TestForkAppendsTheRecipesRulesWithTheirSample(t *testing.T) {
	e := recipeFor(t, "organization.member.added")
	h := newHarness(t, &mapping.Document{Rules: []mapping.Rule{{Name: "existing"}}}, nil, openFork(false))
	selectCatalogEvent(t, h, e.Type)
	if len(h.s.Doc.Rules) != 1 {
		t.Fatal("the recipe was shown with the rule already appended")
	}
	h.press("enter")

	if !h.closed || h.result != (RuleAdded{Index: 1}) {
		t.Fatalf("closed = %v, result = %v, want RuleAdded{1}", h.closed, h.result)
	}
	if h.changes != 1 || len(h.s.Doc.Rules) != 2 {
		t.Fatalf("changes = %d, rules = %d", h.changes, len(h.s.Doc.Rules))
	}
	r := h.s.Doc.Rules[1]
	if r.Name != e.Type || len(r.Tuples) != len(e.Recipe.Rule.Tuples) {
		t.Fatalf("rule = %+v, want the recipe's", r)
	}
	if r.Sample == nil || r.Sample.Label != e.Type {
		t.Fatalf("sample = %+v, want the event's", r.Sample)
	}
}

func TestForkPasteAppendsOneRule(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openFork(true))
	h.typeText(`{"type": "thing.happened", "data": {}}`)
	h.press("ctrl+d")

	if !h.closed || h.result != (RuleAdded{Index: 0}) {
		t.Fatalf("closed = %v, result = %v, want RuleAdded{0}", h.closed, h.result)
	}
	if len(h.s.Doc.Rules) != 1 || h.changes != 1 {
		t.Fatalf("rules = %d, changes = %d, want 1 and 1", len(h.s.Doc.Rules), h.changes)
	}
	r := h.s.Doc.Rules[0]
	if r.Sample == nil || r.Sample.Label != "thing.happened" {
		t.Fatalf("sample = %+v", r.Sample)
	}
	if r.Name != "thing.happened" || r.When != `input.type == "thing.happened"` {
		t.Fatalf("trigger = %q, %q, want it filled from the event type", r.Name, r.When)
	}
	if len(r.Tuples) != 0 {
		t.Fatalf("tuples = %v, want none", r.Tuples)
	}
}

// A payload picked through the pick's own paste row lands the same way.
func TestForkPickedPasteAppendsOneRule(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openFork(false))
	h.press("/")
	h.typeText("paste")
	h.press("enter", "enter")
	h.typeText(`{"type": "thing.happened"}`)
	h.press("ctrl+d")
	if !h.closed || h.result != (RuleAdded{Index: 0}) || len(h.s.Doc.Rules) != 1 {
		t.Fatalf("closed = %v, result = %v, rules = %d", h.closed, h.result, len(h.s.Doc.Rules))
	}
}

func TestForkCancelChangesNothing(t *testing.T) {
	for name, run := range map[string]func(t *testing.T) *harness{
		"esc on the pick": func(t *testing.T) *harness {
			h := newHarness(t, &mapping.Document{}, nil, openFork(false))
			h.press("esc")
			return h
		},
		"esc on the paste box": func(t *testing.T) *harness {
			h := newHarness(t, &mapping.Document{}, nil, openFork(true))
			h.press("esc")
			return h
		},
		"esc on a recipe, then on the pick": func(t *testing.T) *harness {
			h := newHarness(t, &mapping.Document{}, nil, openFork(false))
			selectCatalogEvent(t, h, "organization.member.added")
			h.press("esc") // back to the pick
			if _, ok := h.top().(*eventPick); !ok {
				t.Fatalf("top is %T, want back on the pick", h.top())
			}
			h.press("esc")
			return h
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := run(t)
			if !h.closed || h.result != nil {
				t.Fatalf("closed = %v, result = %v, want a plain close", h.closed, h.result)
			}
			if h.changes != 0 || len(h.s.Doc.Rules) != 0 {
				t.Fatalf("changes = %d, rules = %d, want nothing edited", h.changes, len(h.s.Doc.Rules))
			}
		})
	}
}

// esc on a recipe comes back to the pick with the cursor where it was left, so
// browsing the catalog is not a restart each time.
func TestEscOnARecipeReturnsToThePickWhereItWas(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openFork(false))
	selectCatalogEvent(t, h, "organization.member.added")
	h.press("esc")
	p := topAs[*eventPick](t, h)
	if it, ok := p.list.Selected(); !ok || it.ID != "organization.member.added" {
		t.Fatalf("selected = %+v, want the event just viewed", it)
	}
	// Opening it again shows a fresh recipe, at its first line.
	h.press("enter")
	if c := topAs[*recipeView](t, h); c.off != 0 {
		t.Fatalf("offset = %d, want a recipe opened at its top", c.off)
	}
}

func TestForkBreadcrumbNamesThePickWhileARecipeIsOpen(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openFork(false))
	selectCatalogEvent(t, h, "organization.member.added")
	titles := h.st.Titles()
	if len(titles) < 2 || titles[len(titles)-2] != "Pick an event" {
		t.Fatalf("titles = %q, want the pick named before the recipe", titles)
	}
}

// A read-only session never offers the fork, but a fork that reaches one
// appends nothing, as the recipe refuses to.
func TestForkPasteAppendsNothingInAReadOnlySession(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openFork(true))
	h.s.ReadOnly = true
	h.typeText(`{"type": "thing.happened", "data": {}}`)
	h.press("ctrl+d")

	if !h.closed || h.result != nil {
		t.Fatalf("closed = %v, result = %v, want a plain close", h.closed, h.result)
	}
	if len(h.s.Doc.Rules) != 0 || h.changes != 0 {
		t.Fatalf("rules = %d, changes = %d, want none", len(h.s.Doc.Rules), h.changes)
	}
}

// An event loaded from a file through the fork appends a rule of its own and
// leaves the rules already there as they were.
func TestForkLoadedFileAppendsARuleAndLeavesTheOthersAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, []byte(`{"type": "from.file", "data": {"object": {}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d := sampleDoc(memberAddedEvent())
	existing := d.Rules[0]
	h := newHarness(t, d, nil, openFork(false))
	topAs[*eventPick](t, h).list.SelectID(fileID)
	h.press("enter")
	topAs[*eventFile](t, h)
	h.typeText(path)
	h.press("enter")

	if !h.closed || h.result != (RuleAdded{Index: 1}) {
		t.Fatalf("closed = %v, result = %v, want RuleAdded{1}", h.closed, h.result)
	}
	if len(h.s.Doc.Rules) != 2 || !reflect.DeepEqual(h.s.Doc.Rules[0], existing) {
		t.Fatalf("rules = %+v, want the existing rule untouched and one more", h.s.Doc.Rules)
	}
	if s := h.s.Doc.Rules[1].Sample; s == nil || s.Label != "from.file" {
		t.Fatalf("sample = %+v, want the file's event on the new rule", s)
	}
}
