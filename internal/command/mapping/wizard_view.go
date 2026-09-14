package mapping

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
	"github.com/sergiught/openfga-cli/internal/ui/logo"
)

// minCols and minRows match the connection wizard's floor, so the two refuse to
// draw at the same size rather than one of them rendering a broken frame.
const (
	minCols = 44
	minRows = 16
)

// sideBySide reports whether the preview pane sits beside the editor.
func (m *wizardModel) sideBySide() bool { return m.width >= sideBySideMin }

// contentWidth is the editor column's width: the full width when stacked, a bit
// under half when side by side, clamped so neither pane collapses. The extra 6
// reserves room for the frame's border and padding around this column; the
// frame itself is measured off the assembled body, not cw, because side by
// side the body is the joined pair of panes, not just this one column.
func (m *wizardModel) contentWidth() int {
	w := m.width - 4 - 6
	if m.sideBySide() {
		w = m.width/2 - 4 - 6
	}
	if w < 32 {
		w = 32
	}
	if w > 72 {
		w = 72
	}
	return w
}

// applySize propagates the current size to the widgets that need it.
func (m *wizardModel) applySize() {
	cw := m.contentWidth()
	m.modelPath.SetWidth(cw)
	m.rules.SetSize(cw, m.listHeight())
	m.events.SetSize(cw, m.listHeight())
	m.paste.SetWidth(cw)
	m.paste.SetHeight(m.listHeight())
	m.eventPath.SetWidth(cw)
	m.tupleList.SetSize(cw, m.listHeight())
	m.tupleForm.SetWidth(cw)
	m.tupleForm.SetHeight(m.listHeight())
	m.paths.SetSize(cw, m.listHeight())
	m.varList.SetSize(cw, m.listHeight())
	m.varForm.SetWidth(cw)
	m.iterForm.SetWidth(cw)
	m.filterList.SetSize(cw, m.listHeight())
	m.filterForm.SetWidth(cw)
}

func (m *wizardModel) listHeight() int {
	// The extra 4 leaves room for the frame's top/bottom border and padding.
	h := m.height - 10 - 4
	if h < 5 {
		h = 5
	}
	return h
}

// --- chrome ---

// keyHint is one footer affordance: the key, and what pressing it does.
type keyHint struct{ key, label string }

// chrome is a screen's header copy and footer hints. Keeping every screen's
// wording in one table means a new screen cannot ship with a title but no
// guidance, which is how the first cut ended up with bare headings.
type chrome struct {
	title    string
	subtitle string
	keys     []keyHint
}

var screenChrome = map[screen]chrome{
	screenWelcome: {
		"Create a mapping", "Turn identity-provider events into OpenFGA tuples.",
		[]keyHint{{"↵", "begin"}, {"esc", "cancel"}},
	},
	screenModelSource: {
		"Authorization model", "Check your model has what this mapping needs.",
		[]keyHint{{"↑↓", "move"}, {"↵", "select"}, {"esc", "back"}},
	},
	screenModelFile: {
		"Load a model file", "Point at a .fga or .json authorization model.",
		[]keyHint{{"↵", "load"}, {"esc", "back"}},
	},
	screenRules: {
		"Rules", "Each rule turns one kind of event into tuples.",
		[]keyHint{{"a", "add"}, {"↵", "open"}, {"d", "delete"}, {"m", "model"}, {"^s", "save"}, {"esc", "quit"}, {"?", "help"}},
	},
	screenRule: {
		"Rule", "Pick a part of this rule to edit.",
		[]keyHint{{"↑↓", "move"}, {"↵", "open"}, {"^s", "save"}, {"esc", "back"}},
	},
	screenPayloadKind: {
		"What are you mapping?", "Start from a ready-made example, or bring your own payload.",
		[]keyHint{{"↑↓", "move"}, {"↵", "choose"}, {"esc", "back"}},
	},
	// Every form screen commits its fields on the way out, so its esc reads
	// "done" rather than "back": "back" is what a user presses when they want to
	// throw the edit away, and here it would keep it.
	screenTrigger: {
		"Trigger", "Name the rule, and say which events it matches.",
		[]keyHint{{"tab", "next"}, {"^e", "pick event"}, {"^p", "insert path"}, {"esc", "done"}},
	},
	screenEventPick: {
		"Pick an event", "Choose a sample payload to build the rule against.",
		[]keyHint{{"/", "filter"}, {"↵", "select"}, {"esc", "back"}, {"?", "help"}},
	},
	screenEventPaste: {
		"Paste an event", "Paste one event payload as JSON.",
		[]keyHint{{"^d", "accept"}, {"esc", "cancel"}},
	},
	screenEventFile: {
		"Load an event", "Read a sample event from a JSON file.",
		[]keyHint{{"↵", "load"}, {"esc", "back"}},
	},
	screenTuples: {
		"Tuples", "The relationships this rule writes or deletes.",
		[]keyHint{{"a", "add"}, {"↵", "edit"}, {"d", "delete"}, {"esc", "back"}, {"?", "help"}},
	},
	screenTuple: {
		"Tuple", "Wrap an expression in {{ }} to read from the event.",
		[]keyHint{{"tab", "next"}, {"^o", "pick from model"}, {"^p", "insert path"}, {"esc", "done"}},
	},
	screenAction: {
		"Rule action", "Write or delete every tuple in this rule?",
		[]keyHint{{"↑↓", "move"}, {"↵", "select"}, {"esc", "back"}, {"?", "help"}},
	},
	screenVariables: {
		"Variables", "Name an expression once, then reuse it in tuples.",
		[]keyHint{{"a", "add"}, {"↵", "edit"}, {"d", "delete"}, {"esc", "back"}, {"?", "help"}},
	},
	screenVariable: {
		"Variable", "A name, and the expression it stands for.",
		[]keyHint{{"tab", "next"}, {"^p", "insert path"}, {"esc", "done"}},
	},
	screenIterator: {
		"Iterator", "Repeat this rule's tuples for each item in a list.",
		[]keyHint{{"tab", "next"}, {"^t", "edit tuples"}, {"^p", "insert path"}, {"esc", "done"}},
	},
	screenFilters: {
		"Tuple filters", "Delete every existing tuple matching a pattern.",
		[]keyHint{{"a", "add"}, {"↵", "edit"}, {"d", "delete"}, {"esc", "back"}, {"?", "help"}},
	},
	screenFilter: {
		"Tuple filter", "Blank user or relation matches anything; object needs a type.",
		[]keyHint{{"tab", "next"}, {"^p", "insert path"}, {"esc", "done"}},
	},
	screenPathPick: {
		"Insert a path", "Pick a value from the sample event.",
		[]keyHint{{"/", "filter"}, {"↵", "insert"}, {"esc", "cancel"}},
	},
	screenConfirmSave: {
		"Save the mapping", "Review what is about to be written.",
		nil, // built by chromeFor: the offered keys depend on whether it compiles.
	},
	screenConfirmDelete: {
		"Delete rule", "",
		[]keyHint{{"y", "delete"}, {"n", "cancel"}},
	},
	screenRecipe: {
		"", "A ready-made mapping for this event.",
		[]keyHint{{"↵", "use this"}, {"m", "change model"}, {"esc", "back"}},
	},
}

// chromeFor returns the current screen's chrome, specialising the save dialog:
// enter only saves a mapping with nothing to warn about, so offering it next to
// a list of errors would invite confirming a file the user has not read.
func (m *wizardModel) chromeFor() chrome {
	c := screenChrome[m.top()]
	switch {
	case m.loading:
		// Every other key is ignored while a fetch is in flight, so esc is the
		// only one worth offering.
		c.keys = []keyHint{{"esc", "cancel"}}
	case m.top() == screenRules && len(m.doc.Rules) == 0:
		// An empty hub has nothing to open, delete or save, and offering the keys
		// anyway sends the user to a dialog whose only content is that there was
		// nothing to save. Loading a model is still worth offering: it is what
		// makes the pickers on the way in suggest anything.
		c.keys = []keyHint{{"a", "add"}, {"m", "model"}, {"esc", "quit"}, {"?", "help"}}
	case m.top() == screenRecipe:
		c.title = m.recipeEvent.Type
	case m.top() == screenHelp:
		// screenHelp is a leaf pushed only on top of a screen helpFor answered
		// for (see help.go), so the entry underneath always exists.
		c.title, _, _ = helpFor(m.stack[len(m.stack)-2])
		c.keys = []keyHint{{"any key", "close"}}
	case m.top() != screenConfirmSave:
	case len(m.doc.Rules) == 0:
		c.keys = []keyHint{{"esc", "back"}}
	case len(m.saveProblems()) == 0:
		c.keys = []keyHint{{"↵", "save"}, {"esc", "back"}, {"q", "quit without saving"}}
	default:
		c.keys = []keyHint{{"s", "save anyway"}, {"esc", "back"}, {"q", "quit without saving"}}
	}
	return c
}

// --- frame ---

func (m *wizardModel) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	v.BackgroundColor = style.BgBase
	return v
}

func (m *wizardModel) viewString() string {
	if m.width < minCols || m.height < minRows {
		return lipgloss.Place(max(m.width, 1), max(m.height, 1),
			lipgloss.Center, lipgloss.Center,
			style.Faint.Render(fmt.Sprintf("terminal too small — need %d×%d", minCols, minRows)))
	}
	// The welcome screen is the tour's front door and matches the connection
	// wizard exactly; the confirmations are modals, which is the one surface the
	// rest of the CLI also boxes. Every other screen is the two-pane editor.
	switch m.top() {
	case screenWelcome, screenConfirmSave, screenConfirmDelete:
		return m.cardView()
	}
	return m.paneView()
}

// cardWidth is the centered card's content width. It deliberately ignores
// contentWidth, which is half the screen so the preview can sit beside it — a
// card has the whole terminal to itself.
func (m *wizardModel) cardWidth() int {
	w := m.width - 12
	if w < 32 {
		w = 32
	}
	if w > 64 {
		w = 64
	}
	return w
}

func (m *wizardModel) cardView() string {
	c := m.chromeFor()
	cw := m.cardWidth()

	var b strings.Builder
	if m.top() == screenWelcome {
		b.WriteString(logo.Wordmark(-1))
		b.WriteString("\n\n")
	}
	b.WriteString(style.Title.Render(c.title))
	b.WriteString("\n")
	if c.subtitle != "" {
		b.WriteString(style.Subtitle.Render(lipgloss.NewStyle().Width(cw).Render(c.subtitle)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(m.cardBody(cw))

	card := style.Frame(b.String(), cw)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		card+"\n"+" "+renderHints(c.keys))
}

func (m *wizardModel) cardBody(cw int) string {
	switch m.top() {
	case screenWelcome:
		return m.welcomeBody(cw)
	case screenConfirmSave:
		return m.saveSummary()
	case screenConfirmDelete:
		return lipgloss.NewStyle().Width(cw).Render(m.confirmMsg)
	}
	return ""
}

// paneView is the working frame: the editor on the left under a focused section
// header, the live preview beside or below it, and a status bar pinned to the
// last rows.
func (m *wizardModel) paneView() string {
	cw := m.contentWidth()

	body := m.editorPane(cw)
	if m.sideBySide() {
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(m.width/2-6).Render(body),
			m.previewPane(cw))
	} else {
		// Stacked, the two panes sit one above the other, so they share cw: a
		// preview measured off the terminal instead would hang its rule past the
		// editor's by however much contentWidth clamped.
		body += "\n\n" + m.previewPane(cw)
	}
	// One column of breathing room so nothing sits flush against the edge; the
	// status bar's rule spans the full width and indents its own text to match.
	// The frame is measured off the assembled body rather than cw: side by
	// side, cw is only the editor column's width, not the joined pair's.
	body = lipgloss.NewStyle().PaddingLeft(1).Render(style.Frame(body, lipgloss.Width(body)))

	// Height pads the body out so the status bar lands on the bottom rows
	// instead of floating directly under short content.
	h := m.height - statusRows
	if h < 1 {
		h = 1
	}
	return lipgloss.NewStyle().Height(h).MaxHeight(h).Render(body) + "\n" + m.statusBar()
}

func (m *wizardModel) editorPane(cw int) string {
	c := m.chromeFor()

	var b strings.Builder
	b.WriteString(style.Title.Render(c.title))
	b.WriteString("\n")
	if c.subtitle != "" {
		b.WriteString(style.Subtitle.Render(lipgloss.NewStyle().Width(cw).Render(c.subtitle)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(m.screenBody(cw))

	if m.errMsg != "" {
		b.WriteString("\n\n" + lipgloss.NewStyle().Foreground(style.Red).Render(
			style.IconCross+" "+m.errMsg))
	}
	if m.noteMsg != "" {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(style.Muted).Render(m.noteMsg))
	}
	return b.String()
}

// screenBody renders the interactive content of the current screen.
func (m *wizardModel) screenBody(cw int) string {
	switch m.top() {
	case screenModelSource:
		if m.loading {
			return m.loadingBody()
		}
		return m.sourcePick.View(cw)
	case screenModelFile:
		return m.modelPath.View()
	case screenRules:
		return m.rulesBody()
	case screenRule:
		return m.sections.View(cw)
	case screenPayloadKind:
		return m.kindPick.View(cw)
	case screenTrigger:
		return m.trigger.View()
	case screenEventPick:
		return m.events.View()
	case screenRecipe:
		return m.recipeBody(cw)
	case screenEventPaste:
		return m.paste.View()
	case screenEventFile:
		return m.eventPath.View()
	case screenTuples:
		if len(*m.tuplesOrEmpty()) == 0 {
			return emptyState("No tuples yet.", "a", "add one")
		}
		return m.tupleList.View()
	case screenTuple:
		if m.fieldPick != nil {
			return m.fieldPick.View(cw)
		}
		body := m.tupleForm.View()
		if len(m.ctxKeys) > 0 {
			body += "\n" + lipgloss.NewStyle().Foreground(style.Muted).Render(
				"condition parameters: "+strings.Join(m.ctxKeys, ", "))
		}
		return body
	case screenAction:
		return m.actionPick.View(cw)
	case screenVariables:
		return listOrEmpty(m.varList, "No variables yet.", "a", "add one")
	case screenVariable:
		return m.varForm.View()
	case screenIterator:
		return m.iterForm.View()
	case screenFilters:
		return listOrEmpty(m.filterList, "No tuple filters yet.", "a", "add one")
	case screenFilter:
		return m.filterForm.View()
	case screenPathPick:
		return m.paths.View()
	case screenHelp:
		// The body is prose the wizard authors rather than a widget, so this is
		// the only thing holding it inside the frame at narrow widths.
		_, body, _ := helpFor(m.stack[len(m.stack)-2])
		return lipgloss.NewStyle().Foreground(style.Muted).Width(cw).Render(body)
	}
	return ""
}

// --- status bar ---

// statusRows is how many rows statusBar occupies: a rule, the location line and
// the key hints.
const statusRows = 3

func (m *wizardModel) statusBar() string {
	w := max(m.width, 1)
	rule := lipgloss.NewStyle().Foreground(style.Subtle).Render(strings.Repeat("─", w))

	left := m.breadcrumb()
	if chips := m.contextChips(); chips != "" {
		if left != "" {
			left += lipgloss.NewStyle().Foreground(style.Faintc).Render("  ·  ")
		}
		left += chips
	}
	return rule +
		"\n " + ansi.Truncate(left, w-2, "…") +
		"\n " + ansi.Truncate(renderHints(m.chromeFor().keys), w-2, "…")
}

// breadcrumb answers "where am I" for a hub-and-spoke wizard, the way the
// connection wizard's progress dots answer it for a linear one. The welcome and
// model screens are skipped: loading a model is something you step aside to do,
// not a level of the document you are inside, so listing it would imply the
// rules hub hangs off it.
func (m *wizardModel) breadcrumb() string {
	var parts []string
	for _, s := range m.stack {
		switch s {
		case screenWelcome, screenModelSource, screenModelFile:
			continue
		}
		if t := screenChrome[s].title; t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	// Keep the tail: on a deep spoke the last few hops locate you, and the hub
	// you started from does not.
	if len(parts) > 3 {
		parts = append([]string{"…"}, parts[len(parts)-3:]...)
	}
	return lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Join(parts, " › "))
}

// contextChips keep the two facts that change what the wizard can offer — the
// file being written and whether a model is loaded — on screen at all times.
func (m *wizardModel) contextChips() string {
	faint := lipgloss.NewStyle().Foreground(style.Faintc)
	chips := []string{faint.Render(style.IconStore + " " + m.path)}
	if m.index.Empty() {
		chips = append(chips, faint.Render(style.IconModel+" no model"))
	} else {
		chips = append(chips, faint.Render(fmt.Sprintf("%s %d types",
			style.IconModel, len(m.index.TypeNames()))))
	}
	return strings.Join(chips, "  ")
}

// renderHints draws each affordance as a keycap pill followed by its label.
// style.Keycap already pads one column each side, so the label is appended with
// no separator of its own — adding one would double the gap.
func renderHints(hs []keyHint) string {
	parts := make([]string, 0, len(hs))
	for _, h := range hs {
		parts = append(parts, style.Keycap(h.key)+
			lipgloss.NewStyle().Foreground(style.Muted).Render(h.label))
	}
	return strings.Join(parts, "  ")
}

// --- bodies ---

// emptyState renders "nothing here yet" plus the one key that fixes it, with the
// key picked out so it reads as an instruction rather than prose.
func emptyState(what, key, action string) string {
	muted := lipgloss.NewStyle().Foreground(style.Muted)
	return muted.Render(what) + "\n\n" +
		muted.Render("Press") + style.Keycap(key) + muted.Render("to "+action+".")
}

// listOrEmpty renders a list, or its empty state when it has no items. Every
// list screen in the wizard needs this, so it lives here rather than in six
// copies.
func listOrEmpty(l *uilist.List, what, key, action string) string {
	if len(l.Model.Items()) == 0 {
		return emptyState(what, key, action)
	}
	return l.View()
}

// loadingBody stands in for the source picker while a fetch is in flight. The
// second line is the point of it: an unreachable server is retried by the SDK
// before it gives up, and without saying so the wizard just looks frozen.
func (m *wizardModel) loadingBody() string {
	return m.spin.View() + " " +
		style.Value.Render(fmt.Sprintf("Reading the authorization model from profile %s…", m.profile)) +
		"\n\n" +
		lipgloss.NewStyle().Foreground(style.Muted).
			Render("Retried a few times before giving up.")
}

func (m *wizardModel) rulesBody() string {
	return listOrEmpty(m.rules, "No rules yet.", "a", "add the first one")
}

func (m *wizardModel) welcomeBody(cw int) string {
	model := "no authorization model yet"
	if !m.index.Empty() {
		model = fmt.Sprintf("%d types loaded", len(m.index.TypeNames()))
	}
	prose := lipgloss.NewStyle().Foreground(style.Muted).Width(cw).Render(
		"Pick an event, describe the tuples it should produce, and watch " +
			"the file compile as you type.")
	key := lipgloss.NewStyle().Foreground(style.Muted).Width(7)
	return strings.Join([]string{
		prose,
		"",
		key.Render("file") + style.Value.Render(m.path),
		key.Render("model") + style.Value.Render(model),
	}, "\n")
}

// recipeBody renders the teaching moment for a recipe: what it does, the
// resolved values beside the payload paths they came from, and what the
// loaded model has and lacks.
func (m *wizardModel) recipeBody(cw int) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(style.Muted).Width(cw).Render(m.recipe.Explain))

	if m.recipe.Maps() {
		b.WriteString("\n\n")
		b.WriteString(m.recipeMappingBlock(cw))
	}
	if len(m.recipe.Requires) > 0 {
		b.WriteString("\n\n")
		b.WriteString(m.recipeModelBlock(cw))
	}
	return b.String()
}

// recipeMappingBlock evaluates the recipe's rule against the event's own
// sample and lines each tuple and filter field up with the payload path it
// came from. The four cleanup recipes carry no tuples at all — their mapping
// is entirely their filters — so filters get the same treatment, not a
// fallback.
func (m *wizardModel) recipeMappingBlock(cw int) string {
	doc := &mapping.Document{Rules: []mapping.Rule{m.recipe.Rule}}
	preview := mapping.Evaluate(m.ctx, doc, m.recipeEvent.Sample)

	label := lipgloss.NewStyle().Foreground(style.Muted).Width(9)
	source := lipgloss.NewStyle().Foreground(style.Faintc)

	// Value and source path each get their own line rather than sharing one: the
	// value already spends the whole cw-9 budget the row has left after the
	// label, so a source path tacked onto the same line has nothing left to fit
	// in and would run past cw. A path indented on the line below has the full
	// cw to itself instead.
	row := func(name, value, template string) []string {
		return []string{
			label.Render(name) + style.Value.Render(clamp(value, cw-9)),
			source.Render("  " + clamp(sourcePath(template), cw-2)),
		}
	}

	var lines []string
	for i, t := range m.recipe.Rule.Tuples {
		user, relation, object := t.User, t.Relation, t.Object
		if preview.OK() && i < len(preview.Tuples) {
			user = preview.Tuples[i].User
			relation = preview.Tuples[i].Relation
			object = preview.Tuples[i].Object
		}
		lines = append(lines, row("user", user, t.User)...)
		lines = append(lines, row("relation", relation, t.Relation)...)
		lines = append(lines, row("object", object, t.Object)...)
	}

	// mapper groups a rule's rendered filters into one TupleFilterOperation per
	// rule; there is exactly one rule here, so flattening its Filters gives the
	// resolved values in the same order as m.recipe.Rule.Filters, the same way
	// preview.Tuples lines up with the tuple loop above.
	var resolved []language.TupleFilter
	for _, op := range preview.Filters {
		resolved = append(resolved, op.Filters...)
	}
	for i, f := range m.recipe.Rule.Filters {
		user, relation, object := f.User, f.Relation, f.Object
		if preview.OK() && i < len(resolved) {
			user, relation, object = resolved[i].User, resolved[i].Relation, resolved[i].Object
		}
		// A filter's user, relation and object are each optional — a filter that
		// names an object with no user leaves user blank on purpose, matching
		// any user, and vice versa — so a template that was never set is skipped
		// rather than shown as an empty row.
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

	if !preview.OK() {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(style.Red).Render(
			style.IconCross+" the sample could not be evaluated"))
	}
	return strings.Join(lines, "\n")
}

// recipeModelBlock renders what the recipe's requirements need from the
// loaded model. With no model loaded there is nothing to check against, so
// requirements are stated as plain expectations rather than marked missing —
// the user who skipped loading a model has not told us anything is wrong.
func (m *wizardModel) recipeModelBlock(cw int) string {
	statuses := mapping.CheckRequirements(m.index, m.recipe.Requires)
	muted := lipgloss.NewStyle().Foreground(style.Muted)

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
			lines = append(lines, lipgloss.NewStyle().Foreground(style.Green).Render(
				style.IconCheck+" "+requirementName(s.Requirement)))
			continue
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(style.Red).Render(
			"! "+requirementName(s.Requirement)+" not in your model"))
		lines = append(lines, indentDSL(s.Requirement.DSL, cw))
	}
	return strings.Join(lines, "\n")
}

// requirementName is the short type#relation label a requirement checks —
// the DSL fragment itself is shown separately, indented, when it is missing.
func requirementName(r mapping.Requirement) string {
	if r.Relation == "" {
		return r.Type
	}
	return r.Type + "#" + r.Relation
}

func indentDSL(dsl string, cw int) string {
	lines := strings.Split(dsl, "\n")
	for i, l := range lines {
		lines[i] = "    " + clamp(l, cw-4)
	}
	return lipgloss.NewStyle().Foreground(style.Faintc).Render(strings.Join(lines, "\n"))
}

// previewPane renders the file being built and what the current sample turns
// into. It is the whole point of the hub-and-spoke design: every edit is
// visible immediately.
func (m *wizardModel) previewPane(w int) string {
	if w < 20 {
		return ""
	}
	var b strings.Builder
	// On a short terminal the YAML half yields all its rows to the evaluation
	// half; drop its header too, rather than leaving a heading over nothing.
	if n := m.yamlLines(); n > 0 {
		b.WriteString(style.SectionHeader(m.path, w))
		b.WriteString("\n")
		b.WriteString(clampLines(string(m.preview.YAML), w, n))
		b.WriteString("\n\n")
	}
	b.WriteString(style.SectionHeader("preview", w))
	b.WriteString("\n")
	b.WriteString(m.evaluationLines(w))
	return b.String()
}

// yamlLines gives the YAML half whatever is left after the evaluation half; on
// a short terminal the evaluation wins, since it is what the user is reacting to.
func (m *wizardModel) yamlLines() int {
	if m.height < 40 {
		return 0
	}
	return m.height - 20
}

func (m *wizardModel) evaluationLines(w int) string {
	var out []string
	for _, d := range m.preview.Diagnostics {
		line := fmt.Sprintf("%s line %d: %s", style.IconCross, d.Position.StartLine, d.Message)
		if d.Field != "" {
			line = fmt.Sprintf("%s line %d: %s: %s",
				style.IconCross, d.Position.StartLine, d.Field, d.Message)
		}
		out = append(out, lipgloss.NewStyle().Foreground(style.Red).Render(clamp(sanitizeKeepingLines(line), w)))
	}
	if m.preview.EvalErr != nil {
		out = append(out, lipgloss.NewStyle().Foreground(style.Red).Render(
			clamp(sanitizeKeepingLines(style.IconCross+" "+m.preview.EvalErr.Error()), w)))
	}
	for _, t := range m.preview.Tuples {
		action := string(t.Action)
		if action == "" {
			action = "write"
		}
		// An evaluated tuple is built from the user's own event payload.
		line := fmt.Sprintf("%s %-6s %s  %s  %s", style.IconCheck, action, t.User, t.Relation, t.Object)
		out = append(out, lipgloss.NewStyle().Foreground(style.Green).Render(
			clamp(style.SanitizeTerminal(line), w)))
	}
	for _, op := range m.preview.Filters {
		for _, f := range op.Filters {
			out = append(out, lipgloss.NewStyle().Foreground(style.Primary).Render(
				clamp(style.SanitizeTerminal(fmt.Sprintf("%s %-6s %s",
					style.IconChange, f.Action, filterSummary(f))), w)))
		}
	}
	for _, r := range m.preview.Rules {
		if r.Status == "skipped" {
			out = append(out, lipgloss.NewStyle().Foreground(style.Faintc).Render(
				clamp(style.SanitizeTerminal(fmt.Sprintf("– %s: skipped", r.Name)), w)))
		}
	}
	if len(out) == 0 {
		return lipgloss.NewStyle().Foreground(style.Faintc).Render("no sample event yet")
	}
	return strings.Join(out, "\n")
}

func filterSummary(f language.TupleFilter) string {
	var parts []string
	for _, p := range [][2]string{{"user", f.User}, {"relation", f.Relation}, {"object", f.Object}} {
		if p[1] != "" {
			parts = append(parts, p[0]+"="+p[1])
		}
	}
	return strings.Join(parts, " ")
}

// clampLines truncates a block to n lines and each line to w cells. n <= 0 hides
// the block entirely, which is how the YAML half yields space on short screens.
func clampLines(s string, w, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], lipgloss.NewStyle().Foreground(style.Faintc).Render("…"))
	}
	for i, l := range lines {
		lines[i] = clamp(l, w)
	}
	return strings.Join(lines, "\n")
}

// sanitizeKeepingLines strips terminal control sequences the way
// style.SanitizeTerminal does, but per line, so a message laid out across
// several lines keeps its shape. mapper's compile and evaluation errors use
// that layout to point a caret at the offending column, which collapses into
// nonsense if the newlines are dropped along with the escapes.
func sanitizeKeepingLines(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = style.SanitizeTerminal(lines[i])
	}
	return strings.Join(lines, "\n")
}

func clamp(s string, w int) string {
	r := []rune(s)
	if w < 1 || len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}
