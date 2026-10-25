package tui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

func recipeFor(t *testing.T, typ string) auth0.Event {
	t.Helper()
	e, ok := auth0.Lookup(typ)
	if !ok {
		t.Fatalf("no catalog event %q", typ)
	}
	return e
}

func openRecipe(typ string, t *testing.T) func(s *Session) component {
	e := recipeFor(t, typ)
	return func(s *Session) component { return newRecipeView(s, e) }
}

// firstUnmappedEvent returns a catalog entry whose recipe maps to nothing. It
// is found by asking Maps() rather than by naming an event, so the tests below
// keep testing the right thing when the catalog gains another explain-only
// entry, or when one of today's grows a mapping.
func firstUnmappedEvent(t *testing.T) auth0.Event {
	t.Helper()
	for _, e := range auth0.Catalog() {
		if !e.Recipe.Maps() {
			return e
		}
	}
	t.Fatal("no explain-only event in the catalog")
	return auth0.Event{}
}

// wholeRecipe returns everything the view shows, scrolling to reach what does
// not fit. A recipe with a long explanation, a warning and a mapping outgrows
// a short pane, and the arrows are how the view says to read the rest, so "the
// view shows X" means X is reachable, not that it is on the first row.
func wholeRecipe(h *harness, w, rows int) string {
	var b strings.Builder
	for range 60 {
		b.WriteString(plain(h.view(w, rows)))
		h.press("down")
	}
	return b.String()
}

// recipeLines returns the mapping block of typ's recipe as plain lines,
// trimmed, so a test can read which value sits under which heading.
func recipeLines(t *testing.T, typ string) []string {
	t.Helper()
	c := newRecipeView(NewSession(context.Background(), &mapping.Document{}, nil), recipeFor(t, typ))
	var out []string
	for _, l := range strings.Split(plain(c.recipeMappingBlock(96)), "\n") {
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

// section returns the lines after the heading that starts with head, up to the
// next blank line.
func section(lines []string, head string) []string {
	for i, l := range lines {
		if strings.HasPrefix(l, head) {
			var out []string
			for _, s := range lines[i+1:] {
				if s == "" {
					break
				}
				out = append(out, s)
			}
			return out
		}
	}
	return nil
}

// The view earns its place by showing the resolved value next to the path it
// came from. Templates only click when you see both.
func TestRecipeShowsResolvedValuesAndTheirPaths(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openRecipe("organization.member.added", t))
	out := plain(h.view(96, 60))
	for _, want := range []string{
		"user:auth0|",                 // the resolved user, escaped
		"data.object.user.user_id",    // where it came from
		"member",                      // the fixed relation
		"data.object.organization.id", // the object's path
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from the recipe:\n%s", want, out)
		}
	}
}

// With no model loaded there is nothing to check against, so requirements are
// stated as expectations rather than marked as failures.
func TestRecipeStatesExpectationsWithoutAModel(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openRecipe("organization.member.added", t))
	out := plain(h.view(96, 60))
	if !strings.Contains(out, "expects") {
		t.Fatalf("no expectation wording with no model loaded:\n%s", out)
	}
	if strings.Contains(out, "not in your model") {
		t.Fatalf("a missing model must not be reported as a missing type:\n%s", out)
	}
}

func TestRecipeChecksTheLoadedModel(t *testing.T) {
	m := testModel()
	m.TypeDefinitions = append(m.TypeDefinitions, openfga.TypeDefinition{
		Type:      "membership",
		Relations: map[string]openfga.Userset{"holder": {}},
		Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
			"holder": {DirectlyRelatedUserTypes: []openfga.RelationReference{{Type: "user"}}},
		}},
	})
	ix := mapping.IndexModel(m)
	h := newHarness(t, &mapping.Document{}, ix, openRecipe("organization.member.added", t))
	out := plain(h.view(96, 60))
	if !strings.Contains(out, "your model") || strings.Contains(out, "not in your model") {
		t.Fatalf("a model that has what the recipe needs was not reported as satisfying it:\n%s", out)
	}
}

// The one event that maps to nothing explains itself rather than dead-ending.
func TestAnEventWithNoMappingExplainsWhy(t *testing.T) {
	e := firstUnmappedEvent(t)
	h := newHarness(t, &mapping.Document{}, nil, func(s *Session) component { return newRecipeView(s, e) })
	if out := plain(h.view(96, 60)); !strings.Contains(out, "relationship") {
		t.Fatalf("no explanation for an event that maps to nothing:\n%s", out)
	}
}

// The complaint this answers: "it doesn't let me continue". A view whose only
// exits are backwards is a dead end however good its explanation. The
// subtitle sits directly above the paragraph explaining why there is no
// mapping, so leaving it on the mapping copy would contradict itself.
func TestAnEventWithNoMappingOffersAWayForward(t *testing.T) {
	e := firstUnmappedEvent(t)
	h := newHarness(t, &mapping.Document{}, nil, func(s *Session) component { return newRecipeView(s, e) })

	keys := strings.Join(h.st.Keys(), "|")
	if !strings.Contains(keys, "↵") {
		t.Fatalf("%s is a dead end: the keys offer no ↵, only %q", e.Type, keys)
	}
	// These recipes require nothing of the model, so offering to change it here
	// invites the reading that the missing model is why the event was given no
	// mapping. It is reachable from the hub and from the rule.
	if strings.Contains(keys, "m change model") {
		t.Errorf("%s requires no model but the keys offer to change it: %q", e.Type, keys)
	}
	if _, sub := h.st.Heading(); strings.Contains(sub, "ready-made mapping for this event") {
		t.Fatalf("%s maps to nothing but the subtitle still promises a mapping: %q", e.Type, sub)
	}
}

// The mapping recipes keep offering it, so the guard above is a branch rather
// than a blanket removal.
func TestAMappingEventStillOffersTheUseKey(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openRecipe("organization.member.added", t))
	keys := strings.Join(h.st.Keys(), "|")
	for _, want := range []string{"↵ use this", "m change model", "esc back"} {
		if !strings.Contains(keys, want) {
			t.Fatalf("%q missing from the keys: %q", want, keys)
		}
	}
	if title, sub := h.st.Heading(); title != "organization.member.added" || !strings.Contains(sub, "ready-made mapping") {
		t.Fatalf("heading = %q, %q", title, sub)
	}
}

func TestSourcePathNamesWhereAValueCameFrom(t *testing.T) {
	tests := []struct{ in, want string }{
		{"user:{{ fga_escape(input.data.object.user.user_id) }}", "data.object.user.user_id"},
		{"organization:{{ input.data.object.organization.id }}", "data.object.organization.id"},
		{"member", "(fixed)"},
		{"{{ input.data.object.role.name }}", "data.object.role.name"},
		{"{{ a }}-{{ b }}", "{{ a }}-{{ b }}"},
	}
	for _, tc := range tests {
		if got := sourcePath(tc.in); got != tc.want {
			t.Errorf("sourcePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A lifecycle warning names something Auth0 will not do that the rule cannot
// make up for. The screen leads with what the rule does, so the gap follows the
// mapping, as a claim about what that mapping misses.
func TestARecipeWithAGapWarnsAfterTheMapping(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openRecipe("organization.deleted", t))
	out := plain(h.view(96, 80))
	warn := strings.Index(out, "the gap this leaves")
	if warn < 0 {
		t.Fatalf("no warning on a recipe that has one:\n%s", out)
	}
	if mapping := strings.Index(out, "deletes every existing tuple matching"); mapping < 0 || warn < mapping {
		t.Fatalf("the warning is not below the mapping:\n%s", out)
	}
}

func TestARecipeWithNoGapSaysNothing(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openRecipe("organization.member.added", t))
	if out := plain(h.view(96, 80)); strings.Contains(out, "the gap this leaves") {
		t.Fatalf("a recipe with no warning showed a heading for one:\n%s", out)
	}
}

// The tuples of a fan-out recipe live on its iterator, not on the rule. Showing
// only the rule's own list left user.updated, the one recipe whose whole point
// is the fan-out, displaying no tuples at all.
func TestAnIteratorsTuplesShowOnTheRecipe(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openRecipe("user.updated", t))
	out := wholeRecipe(h, 72, 18)
	for _, want := range []string{
		"per item in data.object.identities", // the heading says it repeats
		"identity",                           // the relation it writes
		"identity.connection",                // the path the object came from
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from a fan-out recipe:\n%s", want, out)
		}
	}
}

// A filter with no action patches, and a patch only deletes what the rule did
// not just write. Describing it as a plain delete told the user their identity
// tuples were about to be removed.
func TestAPatchFilterIsNotDescribedAsADelete(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openRecipe("user.updated", t))
	out := wholeRecipe(h, 72, 18)
	if strings.Contains(out, "deletes every existing tuple") {
		t.Fatalf("a patch filter described as a delete:\n%s", out)
	}
	if !strings.Contains(out, "any other tuple matching") {
		t.Fatalf("the reconciliation is not described:\n%s", out)
	}
}

// A recipe holds more than a short pane: explanation, warning, mapping and
// model requirements. Cutting it was the only answer, and it told the user to
// resize a terminal that was already 40 rows tall.
func TestALongRecipeScrolls(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openRecipe("user.updated", t))
	below := regexp.MustCompile(`⋯ \d+ more`)
	above := regexp.MustCompile(`⋯ \d+ above`)

	top := plain(h.view(72, 12))
	if !below.MatchString(top) {
		t.Fatalf("a recipe taller than the pane offered no way down:\n%s", top)
	}
	if above.MatchString(top) {
		t.Fatalf("the first screenful claims something is above it:\n%s", top)
	}
	// Scrolling is bounded by the body, not by how long a key is held: the view
	// measures the overflow and the key handler stops there.
	for range 200 {
		h.press("down")
		h.view(72, 12)
	}
	bottom := plain(h.view(72, 12))
	if !above.MatchString(bottom) {
		t.Fatalf("scrolled down but nothing says there is anything above:\n%s", bottom)
	}
	if below.MatchString(bottom) {
		t.Fatalf("scrolled to the end but still claims more below:\n%s", bottom)
	}
	if bottom == top {
		t.Fatalf("scrolling changed nothing:\n%s", bottom)
	}
	for range 200 {
		h.press("up")
		h.view(72, 12)
	}
	if again := plain(h.view(72, 12)); again != top {
		t.Fatalf("scrolling back up did not return to the top:\n%s", again)
	}
}

func TestEveryRecipeHonoursItsHeight(t *testing.T) {
	for _, e := range auth0.Catalog() {
		for _, sz := range []struct{ w, h int }{{34, 5}, {50, 10}, {80, 21}} {
			h := newHarness(t, &mapping.Document{}, nil, func(s *Session) component { return newRecipeView(s, e) })
			out := h.view(sz.w, sz.h)
			if n := len(strings.Split(out, "\n")); n > sz.h {
				t.Errorf("%s at %dx%d: %d lines", e.Type, sz.w, sz.h, n)
			}
		}
	}
}

// mapper emits an iterator's tuples before the rule's own. Pairing templates
// with one shared result by position put the first client tuple under the
// tenant template, so the view showed a tuple the rule never writes.
func TestTheRulesOwnTupleShowsItsOwnValues(t *testing.T) {
	lines := recipeLines(t, "connection.created")
	own := strings.Join(section(lines, "writes this tuple"), "\n")
	if !strings.Contains(own, "tenant:my-tenant") || strings.Contains(own, "client:") {
		t.Fatalf("the tenant tuple shows another tuple's values:\n%s", own)
	}
	perItem := strings.Join(section(lines, "writes one tuple per item"), "\n")
	if !strings.Contains(perItem, "client:QG4F6eABIgDzPvcKCMKUjo8c9iet2Skc") {
		t.Fatalf("the iterator's tuple lost its values:\n%s", perItem)
	}
}

// A tuple whose when is false for the sample produces nothing. It must say so,
// rather than borrow the next tuple's values or print its template as one.
func TestAGatedTupleTheSampleSkipsSaysSo(t *testing.T) {
	lines := section(recipeLines(t, "organization.connection.updated"), "writes or deletes these 2 tuples")
	if lines == nil {
		t.Fatalf("a rule that writes one tuple and deletes another is not headed as both:\n%s",
			strings.Join(recipeLines(t, "organization.connection.updated"), "\n"))
	}
	text := strings.Join(lines, "\n")
	// The sample has is_enabled false: the delete fires, the write is skipped.
	del := strings.Index(text, "action   delete")
	write := strings.Index(text, "action   write")
	skip := strings.Index(text, "false for this sample: skipped")
	if del < 0 || write < 0 || skip < 0 || del > write || write > skip {
		t.Fatalf("want the delete first and the write after it, marked skipped:\n%s", text)
	}
	if !strings.Contains(text[del:write], "organization:org_1234567890abcdef") {
		t.Fatalf("the delete lost its values:\n%s", text)
	}
	if strings.Contains(text[write:], "org_1234567890abcdef") || !strings.Contains(text[write:], "{{") {
		t.Fatalf("the skipped write should show its template, not a value:\n%s", text)
	}
}

// Actions only get their own row when a block mixes them; a rule that only
// writes says so in its heading.
func TestASingleActionBlockHasNoActionRows(t *testing.T) {
	text := strings.Join(recipeLines(t, "organization.member.added"), "\n")
	if strings.Contains(text, "action") {
		t.Fatalf("a write-only rule grew action rows:\n%s", text)
	}
}

// A gated tuple shows its when and the sample's verdict on it, so the view says
// which branch of a union fires and why the other does not.
func TestAGatedTupleShowsItsWhenAndTheVerdict(t *testing.T) {
	text := strings.Join(section(recipeLines(t, "group.member.added"), "writes these 2 tuples"), "\n")
	user := strings.Index(text, `when     input.data.object.member.member_type == "user"`)
	conn := strings.Index(text, `when     input.data.object.member.member_type == "connection"`)
	if user < 0 || conn < 0 {
		t.Fatalf("a gated tuple does not show its when:\n%s", text)
	}
	if !strings.Contains(text[user:conn], "true for this sample") ||
		!strings.Contains(text[conn:], "false for this sample: skipped") {
		t.Fatalf("the sample's verdict on each gate is missing:\n%s", text)
	}
}

func TestRecipeKeysScrollEscClosesAndMAsksForTheModel(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openRecipe("organization.member.added", t))
	h.press("m")
	if len(h.requests) != 1 || h.requests[0] != RequestModel {
		t.Fatalf("requests = %v, want the model source", h.requests)
	}
	// Asking for the model leaves the recipe where it is.
	if h.closed {
		t.Fatal("m closed the recipe")
	}
	h.press("esc")
	if !h.closed || h.result != nil {
		t.Fatalf("esc: closed = %v, result = %v, want a plain close", h.closed, h.result)
	}
	if len(h.s.Doc.Rules) != 0 || h.changes != 0 {
		t.Fatal("backing out edited the document")
	}
}

func TestEnterOnARecipeAppendsItsRuleWithTheSample(t *testing.T) {
	e := recipeFor(t, "organization.member.added")
	h := newHarness(t, &mapping.Document{Rules: []mapping.Rule{{Name: "first"}}}, nil, openRecipe(e.Type, t))
	h.press("enter")
	if !h.closed || h.result != (RuleAdded{Index: 1}) {
		t.Fatalf("closed = %v, result = %v, want RuleAdded{1}", h.closed, h.result)
	}
	if h.changes != 1 || len(h.s.Doc.Rules) != 2 {
		t.Fatalf("changes = %d, rules = %d", h.changes, len(h.s.Doc.Rules))
	}
	r := h.s.Doc.Rules[1]
	if r.Name != e.Recipe.Rule.Name || len(r.Tuples) != len(e.Recipe.Rule.Tuples) {
		t.Fatalf("rule = %+v, want the recipe's", r)
	}
	if r.Sample == nil || r.Sample.Label != e.Type {
		t.Fatalf("sample = %+v, want the event's", r.Sample)
	}
	// The recipe filled Name and When, so they count as auto-filled and a later
	// ctrl+e moves them with the event.
	if r.AutoName != r.Name || r.AutoWhen != r.When {
		t.Fatalf("auto-fill not recorded: %+v", r)
	}
}

// An explain-only recipe has nothing to append. Appending its zero Rule would
// drop a rule with no name, no trigger and a blocking problem the user never
// asked for. What it appends instead is the rule a pasted payload would have
// produced: named and triggered off the event, carrying its sample, and empty
// of tuples for the user to fill.
func TestEnterOnAnExplainOnlyRecipeStartsARuleWithoutTuples(t *testing.T) {
	e := firstUnmappedEvent(t)
	h := newHarness(t, &mapping.Document{}, nil, func(s *Session) component { return newRecipeView(s, e) })
	h.press("enter")
	if !h.closed || h.result != (RuleAdded{Index: 0}) {
		t.Fatalf("closed = %v, result = %v", h.closed, h.result)
	}
	r := h.s.Doc.Rules[0]
	if r.Sample == nil || r.Sample.Label != e.Type {
		t.Fatalf("the event's payload did not come with it: sample = %+v", r.Sample)
	}
	if r.Name == "" || r.When == "" {
		t.Fatalf("the trigger was left blank: name = %q, when = %q", r.Name, r.When)
	}
	if len(r.Tuples) != 0 {
		t.Fatalf("%s maps to nothing but %d tuples appeared", e.Type, len(r.Tuples))
	}
}

func TestARecipeAppendsNothingInAReadOnlySession(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, openRecipe("organization.member.added", t))
	h.s.ReadOnly = true
	h.press("enter")
	if h.closed || len(h.s.Doc.Rules) != 0 {
		t.Fatalf("closed = %v, rules = %d", h.closed, len(h.s.Doc.Rules))
	}
	if strings.Contains(strings.Join(h.st.Keys(), "|"), "use this") {
		t.Fatalf("a read-only session offers ↵: %v", h.st.Keys())
	}
}

// Everything the view draws from the payload, the recipe or the model passes
// through the sanitizer: a hostile sample or template must not reach the
// terminal as an escape sequence.
func TestRecipeRowsCannotDriveTheTerminal(t *testing.T) {
	const hostile = "\x1b[2J\u009b\u202e"
	e := recipeFor(t, "organization.member.added")

	// Deep-copy the sample so the catalog's own is left alone.
	raw, err := json.Marshal(e.Sample)
	if err != nil {
		t.Fatal(err)
	}
	var sample map[string]any
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatal(err)
	}
	sample["data"].(map[string]any)["object"].(map[string]any)["organization"] = map[string]any{"id": "org" + hostile}
	e.Sample = sample
	e.Type = "evil" + hostile
	e.Recipe.Explain = "explain" + hostile
	e.Recipe.Warn = "warn" + hostile
	r := e.Recipe.Rule
	r.Tuples = append([]mapping.Tuple(nil), r.Tuples...)
	r.Tuples[0].When = "true" + hostile
	r.Tuples[0].Relation = "member" + hostile
	e.Recipe.Rule = r
	e.Recipe.Requires = []mapping.Requirement{{Type: "organization" + hostile, Relation: "member", DSL: "type organization" + hostile}}

	for name, ix := range map[string]*mapping.ModelIndex{"no model": nil, "model": mapping.IndexModel(testModel())} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, &mapping.Document{}, ix, func(s *Session) component { return newRecipeView(s, e) })
			title, sub := h.st.Heading()
			out := wholeRecipe(h, 120, 30) + title + sub
			for _, bad := range []string{"\x1b[2J", "\u009b", "\u202e"} {
				if strings.Contains(out, bad) {
					t.Errorf("%q reached the screen:\n%q", bad, out)
				}
			}
		})
	}
}

// The screen leads with what it does: the tuples and the model status come
// before any prose, and the prose is not in the body at all.
func TestTheRecipeLeadsWithItsTuples(t *testing.T) {
	e := recipeFor(t, "user.created")
	h := newHarness(t, &mapping.Document{}, nil, func(s *Session) component { return newRecipeView(s, e) })
	out := wholeRecipe(h, 80, 24)
	tuples := strings.Index(out, "writes these 2 tuples")
	status := strings.Index(out, "expects")
	if tuples < 0 || status < 0 {
		t.Fatalf("tuples or model status missing:\n%s", out)
	}
	if status < tuples {
		t.Fatalf("the model status shows before the tuples:\n%s", out)
	}
	if strings.Contains(strings.Join(strings.Fields(out), " "), strings.Join(strings.Fields(e.Recipe.Explain)[:6], " ")) {
		t.Fatalf("the explanation is still in the body:\n%s", out)
	}
	if !strings.Contains(out, "? why this mapping") {
		t.Fatalf("no pointer to the explanation:\n%s", out)
	}
	if e.Recipe.Warn != "" {
		if warn := strings.Index(out, "the gap this leaves"); warn < status {
			t.Fatalf("the gap shows before the model status:\n%s", out)
		}
	}
}

func TestTheRecipeExplainsBehindHelp(t *testing.T) {
	e := recipeFor(t, "user.created")
	h := newHarness(t, &mapping.Document{}, nil, func(s *Session) component { return newRecipeView(s, e) })
	_, body, ok := h.st.Help()
	if !ok || !strings.Contains(body, strings.Join(strings.Fields(clean(e.Recipe.Explain))[:6], " ")) {
		t.Fatalf("help does not carry the explanation (ok=%v):\n%s", ok, body)
	}
}

func TestAnExplainOnlyRecipeKeepsItsText(t *testing.T) {
	e := firstUnmappedEvent(t)
	h := newHarness(t, &mapping.Document{}, nil, func(s *Session) component { return newRecipeView(s, e) })
	out := wholeRecipe(h, 96, 40)
	if !strings.Contains(out, strings.Join(strings.Fields(clean(e.Recipe.Explain))[:4], " ")) {
		t.Fatalf("an explain-only recipe lost its text:\n%s", out)
	}
	if strings.Contains(out, "? why this mapping") {
		t.Fatalf("an explain-only recipe points at help for text it already shows:\n%s", out)
	}
}

// The recipe is a wide table of values and paths; a half-width card would wrap
// it. The shell keeps its sidebar (30 columns), so 140 columns leave 108 and the
// body takes at least 100 of them.
func TestTheRecipeUsesTheWidth(t *testing.T) {
	h := newWsHarness(t, opened{path: filepath.Join(t.TempDir(), "mapping.yaml"), doc: &mapping.Document{}}, Options{})
	h.resize(140, 40)
	h.press("a")
	pickEvent(h, "user.created")
	if _, ok := h.topOf(paneRules).(*recipeView); !ok {
		t.Fatalf("top is %T, want the recipe", h.topOf(paneRules))
	}
	mw, mh := h.w.mainSize()
	if mw < 100 {
		t.Fatalf("the host gives the recipe %d columns at 140, want at least 100", mw)
	}
	// The recipe's own rows, not the frame around them: the sidebar and the
	// header would reach 100 without the recipe contributing anything.
	widest := 0
	for _, l := range strings.Split(plain(h.topOf(paneRules).View(mw, mh+40)), "\n") {
		widest = max(widest, len([]rune(strings.TrimRight(l, " "))))
	}
	if widest < mw-4 {
		t.Fatalf("the recipe's widest row is %d columns of the %d it is given", widest, mw)
	}
}

// The catalog is shared, so a rule built from a recipe must own its slices and
// its sample: editing it in the document cannot reach the next pick.
func TestEditingAnAcceptedRecipeLeavesTheCatalogAlone(t *testing.T) {
	e := recipeFor(t, "organization.member.added")
	wantUser := e.Recipe.Rule.Tuples[0].User
	h := newHarness(t, &mapping.Document{}, nil, openRecipe(e.Type, t))
	h.press("enter")
	r := &h.s.Doc.Rules[0]
	r.Tuples[0].User = "edited"
	r.Sample.Event["type"] = "edited"

	again := recipeFor(t, e.Type)
	if got := again.Recipe.Rule.Tuples[0].User; got != wantUser {
		t.Fatalf("catalog tuple user = %q, want %q", got, wantUser)
	}
	if got := again.Sample["type"]; got != e.Type {
		t.Fatalf("catalog sample type = %v, want %q", got, e.Type)
	}
}
