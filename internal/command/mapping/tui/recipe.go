package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

// recipeView shows the worked mapping for a catalog event before anything is
// committed, so the user sees what they are about to get and what their model
// must have. Only the add-rule fork opens it; ctrl+e on an existing rule
// attaches the event directly instead.
type recipeView struct {
	s *Session
	e auth0.Event
	// off is how far the body is scrolled, and maxOff how far it can go. The
	// second is measured by View, since how tall the body is depends on the
	// width it is given.
	off, maxOff int
}

func newRecipeView(s *Session, e auth0.Event) *recipeView {
	return &recipeView{s: s, e: e}
}

func (c *recipeView) Update(msg tea.Msg) (component, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	switch k.String() {
	case "up", "k":
		if c.off > 0 {
			c.off--
		}
	case "down", "j":
		if c.off < c.maxOff {
			c.off++
		}
	case "esc":
		c.s.post(doneMsg{})
	case "m":
		// The model source opens over this view rather than closing it, so the
		// user keeps their place on the recipe.
		c.s.post(requestMsg{what: RequestModel})
	case "enter":
		if c.s.ReadOnly {
			return c, nil
		}
		c.accept()
	}
	return c, nil
}

// accept appends the rule and closes with it.
func (c *recipeView) accept() {
	var r mapping.Rule
	// Maps() is the single test this view branches on.
	if c.e.Recipe.Maps() {
		r = cloneRule(c.e.Recipe.Rule)
		r.Sample = &mapping.Sample{Label: c.e.Type, Event: cloneEvent(c.e.Sample)}
		// The recipe filled Name and When, so record that as auto-fill: otherwise
		// SetSample cannot tell "the recipe wrote this" from "the user typed
		// this", and a later ctrl+e leaves the old trigger beside the new sample.
		r.AutoName, r.AutoWhen = r.Name, r.When
	} else {
		// An explain-only recipe has no rule to hand over — appending its zero
		// Rule would drop an unnamed, already blocking rule on the hub. But the
		// user picked this event for a reason, and the explanation is a reason to
		// write something different, not to go away: connection.updated says
		// outright which tuple its is_enabled field should delete. So take the
		// same path a pasted payload takes — a rule carrying this sample, its
		// trigger filled in, and no tuples yet — and leave the user in the editor
		// rather than on a screen whose only exits are backwards.
		SetSample(&r, c.e.Type, cloneEvent(c.e.Sample))
	}
	r.New = true
	c.s.Doc.Rules = append(c.s.Doc.Rules, r)
	c.s.post(docChangedMsg{})
	c.s.post(doneMsg{result: RuleAdded{Index: len(c.s.Doc.Rules) - 1}})
}

func (c *recipeView) View(width, height int) string {
	height = max(height, 1)
	// Wrap the way the host's frame would, so the rows counted here are the
	// rows drawn.
	lines := strings.Split(lipgloss.NewStyle().Width(width).Render(c.body(width)), "\n")
	c.maxOff = max(len(lines)-height, 0)
	c.off = min(c.off, c.maxOff)

	// ⋯ and its two-column indent are the playground's marker (see capLinesAt),
	// so that "there is more than fits" looks the same wherever the CLI says it.
	// The count stays: the user is deciding whether the rest is worth scrolling
	// or resizing for. The markers replace rows rather than adding them, since
	// an added row would cost the very room it accounts for.
	if c.off > 0 {
		lines = lines[c.off:]
		lines[0] = style.Faint.Render(fmt.Sprintf("  ⋯ %d above", c.off))
	}
	if len(lines) > height {
		hidden := len(lines) - height + 1
		lines = lines[:height]
		lines[height-1] = style.Faint.Render(fmt.Sprintf("  ⋯ %d more", hidden))
	}
	return strings.Join(lines, "\n")
}

func (c *recipeView) Keys() []string {
	// An explain-only recipe has no mapping to promise in the subtitle and
	// nothing to "use" in the footer. What it does still have is the payload,
	// so ↵ stays — pointed at a rule the user writes themselves.
	//
	// "m change model" goes. Every explain-only recipe has zero requirements,
	// so the model has no bearing on this view; offered directly under a
	// sentence about something being absent, it reads as the cause, and a user
	// who has loaded no model concludes that is why they were given no
	// mapping. The model is reachable from the hub and from the rule.
	//
	// Not "start a rule anyway": the user picked this event on purpose, and
	// "anyway" casts an explanation of the common case as advice they are
	// choosing to ignore. Their model may well want a tuple here.
	var keys []string
	switch {
	case c.s.ReadOnly:
	case c.e.Recipe.Maps():
		keys = append(keys, "↵ use this")
	default:
		keys = append(keys, "↵ write your own rule")
	}
	if c.e.Recipe.Maps() {
		keys = append(keys, "m change model")
	}
	return append(keys, "esc back", "? help")
}

func (c *recipeView) heading() (string, string) {
	title := style.SanitizeTerminal(c.e.Type)
	if !c.e.Recipe.Maps() {
		return title, "Why this event maps to no tuples."
	}
	return title, "A ready-made mapping for this event."
}

// isDialog draws the view as a card: it concerns a rule that does not exist
// yet, so a preview beside it could only show the file as it already is.
func (c *recipeView) isDialog() {}

// body renders the teaching moment for a recipe: what it does, the
// resolved values beside the payload paths they came from, and what the
// loaded model has and lacks.
func (c *recipeView) body(cw int) string {
	var parts []string
	if c.e.Recipe.Maps() {
		// What the rule does leads: the tuples and filters, then what the model
		// must have. The prose is behind ?, since a reader who wants to know
		// what they would get has no use for a paragraph first.
		parts = append(parts, c.recipeMappingBlock(cw))
	} else {
		// Nothing is written for this event, so the explanation is the screen.
		parts = append(parts, style.Subtitle.Width(cw).Render(clean(c.e.Recipe.Explain)))
	}
	if len(c.e.Recipe.Requires) > 0 {
		parts = append(parts, c.recipeModelBlock(cw))
	}
	// The gap follows the model rather than leading, so it is a claim about
	// what this mapping misses, read with the mapping in view.
	if c.e.Recipe.Warn != "" {
		parts = append(parts, style.Warn.Render("! the gap this leaves")+"\n"+
			style.Subtitle.Width(cw).Render(clean(c.e.Recipe.Warn)))
	}
	if c.e.Recipe.Maps() {
		parts = append(parts, style.Faint.Render("? why this mapping"))
	}
	return strings.Join(parts, "\n\n")
}

// help is what ? shows after the global keys: why the recipe maps as it does.
func (c *recipeView) help() (string, string) {
	if !c.e.Recipe.Maps() {
		return "Recipe", "Nothing is written for this event. The text on the screen says why; " +
			"↵ starts a rule of your own."
	}
	return "Why this mapping", clean(c.e.Recipe.Explain)
}

// recipeMappingBlock evaluates the recipe's rule against the event's own
// sample and lines each tuple and filter field up with the payload path it
// came from. It renders in four groups because a rule has up to four things to
// say: the tuples it writes, the tuples it writes once per item in a list, the
// tuples it reconciles away, and the tuples it deletes outright.
func (c *recipeView) recipeMappingBlock(cw int) string {
	doc := &mapping.Document{Rules: []mapping.Rule{c.e.Recipe.Rule}}
	preview := mapping.Evaluate(c.s.Ctx, doc, c.e.Sample)

	label := style.Subtitle.Width(9)
	source := style.Faint
	heading := style.Subtitle

	// Value and source path each get their own line rather than sharing one: the
	// value already spends the whole cw-9 budget the row has left after the
	// label, so a source path tacked onto the same line has nothing left to fit
	// in and would run past cw. A path indented on the line below has the full
	// cw to itself instead.
	row := func(name, value, template string) []string {
		return []string{
			label.Render(name) + style.Value.Render(picker.Clamp(clean(value), cw-9)),
			source.Render("  " + picker.Clamp(clean(sourcePath(template)), cw-2)),
		}
	}

	// Every group is headed, for the same two reasons. Unheaded, the rows are
	// six lines of user/relation/object with no word for what they collectively
	// are — the screen teaches the tuple without ever naming it. And a recipe
	// built entirely of filters removes tuples; every word on this screen up to
	// here says a rule produces them, so the one that takes things away has to
	// say so before the user accepts it.
	var lines []string
	group := func(head string, render func()) {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, heading.Render(head))
		render()
	}

	// Each template's values come from evaluating it on its own (see
	// templateValues). An action row is added only when a block both writes and
	// deletes: its heading can no longer say which tuple does which.
	r := c.e.Recipe.Rule
	tupleRows := func(templates []mapping.Tuple, values []tupleValue) {
		mixed := tupleVerb(r, templates) == "writes or deletes"
		for i, t := range templates {
			user, relation, object := t.User, t.Relation, t.Object
			if v := values[i].tuple; v != nil {
				user, relation, object = v.User, v.Relation, v.Object
			}
			if mixed {
				lines = append(lines, label.Render("action")+style.Value.Render(clean(tupleAction(r, t))))
			}
			// A gated tuple leads with its gate and what the sample made of it,
			// which is also the only place this screen shows a tuple's when.
			switch {
			case t.When != "":
				lines = append(lines, label.Render("when")+style.Value.Render(picker.Clamp(clean(t.When), cw-9)))
				if values[i].skipped {
					lines = append(lines, source.Render("  false for this sample: skipped"))
				} else if values[i].tuple != nil {
					lines = append(lines, source.Render("  true for this sample"))
				}
			case values[i].skipped:
				lines = append(lines, source.Render("  skipped for this sample"))
			}
			lines = append(lines, row("user", user, t.User)...)
			lines = append(lines, row("relation", relation, t.Relation)...)
			lines = append(lines, row("object", object, t.Object)...)
		}
	}

	if n := len(r.Tuples); n > 0 {
		group(recipeTupleHeading(r, n), func() {
			tupleRows(r.Tuples, c.templateValues(r.Tuples, false))
		})
	}
	if it := r.Iterator; it != nil && len(it.Tuples) > 0 {
		group(iteratorHeading(r), func() {
			tupleRows(it.Tuples, c.templateValues(it.Tuples, true))
		})
	}

	// mapper groups a rule's rendered filters into one TupleFilterOperation per
	// rule; there is exactly one rule here, so flattening its Filters gives the
	// resolved values in the same order as c.e.Recipe.Rule.Filters. Unlike tuples,
	// filters have no when, so none can drop out and shift the rest.
	var resolvedFilters []language.TupleFilter
	for _, op := range preview.Filters {
		resolvedFilters = append(resolvedFilters, op.Filters...)
	}
	filterRows := func(want func(mapping.TupleFilter) bool) func() {
		return func() {
			for i, f := range c.e.Recipe.Rule.Filters {
				if !want(f) {
					continue
				}
				user, relation, object := f.User, f.Relation, f.Object
				if preview.OK() && i < len(resolvedFilters) {
					user = resolvedFilters[i].User
					relation = resolvedFilters[i].Relation
					object = resolvedFilters[i].Object
				}
				// A filter's user, relation and object are each optional — a
				// filter that names an object with no user leaves user blank on
				// purpose, matching any user, and vice versa — so a template that
				// was never set is skipped rather than shown as an empty row.
				if f.User != "" {
					lines = append(lines, row("user", user, f.User)...)
				}
				if f.Relation != "" {
					lines = append(lines, row("relation", relation, f.Relation)...)
				}
				if f.Object != "" {
					lines = append(lines, row("object", object, f.Object)...)
				}
			}
		}
	}
	if hasFilter(c.e.Recipe.Rule.Filters, isPatchFilter) {
		// Named for its effect rather than its keyword. "Patch" says nothing to
		// someone meeting it here, and the reconciliation only ever shows itself
		// as the deletions above are not: the tuples this rule just wrote stay,
		// and whatever else the filter reaches does not.
		group("then deletes any other tuple matching", filterRows(isPatchFilter))
	}
	if hasFilter(c.e.Recipe.Rule.Filters, func(f mapping.TupleFilter) bool { return !isPatchFilter(f) }) {
		group("deletes every existing tuple matching", filterRows(func(f mapping.TupleFilter) bool {
			return !isPatchFilter(f)
		}))
	}

	if !preview.OK() {
		lines = append(lines, "", style.Failure.Render(
			Glyph(icons.I().Cross, "the sample could not be evaluated")))
	}
	return strings.Join(lines, "\n")
}

// tupleValue is what one tuple template produced from the recipe's sample: the
// tuple, or skipped when a when kept it out. Neither means the sample could not
// be evaluated, and the template is shown as written.
type tupleValue struct {
	tuple   *language.Tuple
	skipped bool
}

// templateValues evaluates each template on its own, inside the rule (or, with
// iterator set, inside its iterator) with every other tuple removed, and keeps
// the first tuple each one produces. One shared result cannot be split back up
// by position: mapper emits an iterator's tuples before the rule's own, and a
// template whose when is false for this sample leaves no slot at all. Filters
// are dropped too: with nothing to reconcile against, mapper returns the
// tuples as plain writes instead of folding them into a filter operation.
func (c *recipeView) templateValues(templates []mapping.Tuple, iterator bool) []tupleValue {
	out := make([]tupleValue, len(templates))
	for i, t := range templates {
		r := c.e.Recipe.Rule
		r.Filters = nil
		if iterator {
			it := *r.Iterator
			it.Tuples = []mapping.Tuple{t}
			r.Iterator, r.Tuples = &it, nil
		} else {
			r.Iterator, r.Tuples = nil, []mapping.Tuple{t}
		}
		p := mapping.Evaluate(c.s.Ctx, &mapping.Document{Rules: []mapping.Rule{r}}, c.e.Sample)
		switch {
		case !p.OK():
		case len(p.Tuples) == 0:
			out[i].skipped = true
		default:
			out[i].tuple = &p.Tuples[0]
		}
	}
	return out
}

// isPatchFilter reports whether a filter reconciles rather than deletes. An
// unset action is a patch — that is mapper's default, not an omission.
func isPatchFilter(f mapping.TupleFilter) bool { return f.Action != "delete" }

func hasFilter(filters []mapping.TupleFilter, want func(mapping.TupleFilter) bool) bool {
	for _, f := range filters {
		if want(f) {
			return true
		}
	}
	return false
}

// tupleAction is what mapper does with t: its own action, else the rule's, else
// a write, which is mapper's default for an unset one.
func tupleAction(r mapping.Rule, t mapping.Tuple) string {
	switch {
	case t.Action != "":
		return t.Action
	case r.Action != "":
		return r.Action
	}
	return "write"
}

// tupleVerb says what a block of tuples does, naming both actions when it
// mixes them rather than letting one delete label every tuple a delete.
func tupleVerb(r mapping.Rule, ts []mapping.Tuple) string {
	writes, deletes := false, false
	for _, t := range ts {
		if tupleAction(r, t) == "delete" {
			deletes = true
		} else {
			writes = true
		}
	}
	switch {
	case writes && deletes:
		return "writes or deletes"
	case deletes:
		return "deletes"
	}
	return "writes"
}

// recipeTupleHeading names the tuple block and says what the rule does with it.
func recipeTupleHeading(r mapping.Rule, n int) string {
	verb := tupleVerb(r, r.Tuples)
	if n == 1 {
		return verb + " this tuple"
	}
	return fmt.Sprintf("%s these %d tuples", verb, n)
}

// iteratorHeading says that the iterator's tuples repeat, and what they repeat
// over. The count is per item rather than a total: how many items arrive is a
// property of the payload, and the sample's count is not the user's.
func iteratorHeading(r mapping.Rule) string {
	what := "one tuple"
	if n := len(r.Iterator.Tuples); n > 1 {
		what = fmt.Sprintf("%d tuples", n)
	}
	return tupleVerb(r, r.Iterator.Tuples) + " " + what + " per item in " + clean(strings.TrimPrefix(r.Iterator.Source, "input."))
}

// recipeModelBlock renders what the recipe's requirements need from the
// loaded model. With no model loaded there is nothing to check against, so
// requirements are stated as plain expectations rather than marked missing —
// the user who skipped loading a model has not told us anything is wrong.
func (c *recipeView) recipeModelBlock(cw int) string {
	statuses := mapping.CheckRequirements(c.s.Index, c.e.Recipe.Requires)
	muted := style.Subtitle

	// Checked comes solely from whether a model is loaded (ix.Empty()), not from
	// anything about the individual requirement, so it is the same on every
	// element — sampling statuses[0] here is reading a document-wide fact, not
	// guessing at the rest of the slice.
	if !statuses[0].Checked {
		lines := []string{muted.Render("this mapping expects")}
		for _, s := range statuses {
			lines = append(lines, "  "+requirementName(s.Requirement), indentDSL(s.Requirement.DSL, cw))
		}
		return strings.Join(lines, "\n")
	}

	lines := []string{muted.Render("your model")}
	for _, s := range statuses {
		if s.Satisfied() {
			lines = append(lines, style.Success.Render(
				Glyph(icons.I().Check, requirementName(s.Requirement))))
			continue
		}
		lines = append(lines, style.Failure.Render(
			"! "+requirementName(s.Requirement)+" not in your model"))
		lines = append(lines, indentDSL(s.Requirement.DSL, cw))
	}
	return strings.Join(lines, "\n")
}

// requirementName is the short type#relation label a requirement checks —
// the DSL fragment itself is shown separately, indented, when it is missing.
func requirementName(r mapping.Requirement) string {
	if r.Relation == "" {
		return clean(r.Type)
	}
	return clean(r.Type + "#" + r.Relation)
}

func indentDSL(dsl string, cw int) string {
	lines := strings.Split(dsl, "\n")
	for i, l := range lines {
		lines[i] = "    " + picker.Clamp(clean(l), cw-4)
	}
	return style.Faint.Render(strings.Join(lines, "\n"))
}

// cloneEvent copies a catalog sample. The catalog is shared and read-only, and
// a rule's sample is document state a caller may edit.
func cloneEvent(e map[string]any) map[string]any {
	if e == nil {
		return nil
	}
	return cloneValue(e).(map[string]any)
}

// cloneRule copies a catalog recipe's rule down to the slices the document
// edits in place, so the rule added to the document does not share them.
func cloneRule(r mapping.Rule) mapping.Rule {
	r.Variables = slices.Clone(r.Variables)
	r.Filters = slices.Clone(r.Filters)
	r.Tuples = cloneTuples(r.Tuples)
	if r.Iterator != nil {
		it := *r.Iterator
		it.Tuples = cloneTuples(it.Tuples)
		r.Iterator = &it
	}
	return r
}

func cloneTuples(ts []mapping.Tuple) []mapping.Tuple {
	out := slices.Clone(ts)
	for i := range out {
		out[i].Context = slices.Clone(out[i].Context)
	}
	return out
}

// clean strips terminal control sequences from text that came from the payload,
// the recipe or the model before it is drawn.
var clean = style.SanitizeTerminal

// sourcePath names where a template's value came from, for the column beside
// the resolved value. It unwraps one enclosing call so fga_escape(...) does not
// hide the path, and gives up honestly when a field interpolates more than once:
// no single path describes such a value, and inventing one would teach a lie.
func sourcePath(template string) string {
	open := strings.Index(template, "{{")
	if open < 0 {
		return "(fixed)"
	}
	if strings.Count(template, "{{") > 1 {
		return template
	}
	end := strings.Index(template, "}}")
	if end < open {
		return template
	}
	expr := strings.TrimSpace(template[open+2 : end])
	if i := strings.Index(expr, "("); i >= 0 && strings.HasSuffix(expr, ")") {
		expr = strings.TrimSpace(expr[i+1 : len(expr)-1])
	}
	return strings.TrimPrefix(expr, "input.")
}
