package mapping

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// openTrigger loads the current rule into the trigger form and shows it.
func (m *wizardModel) openTrigger() {
	r := m.rule()
	if r == nil {
		return
	}
	// Reset before SetValues, never after: Reset clears the values along with
	// the errors and the focus. This order is load-bearing in every open* below.
	m.trigger.Reset()
	m.trigger.SetValues([]string{r.Name, r.When})
	m.push(screenTrigger)
}

// commitTrigger writes the form back into the rule. Called on every exit from
// the trigger screen so a half-finished edit is never silently dropped.
func (m *wizardModel) commitTrigger() {
	r := m.rule()
	if r == nil {
		return
	}
	v := m.trigger.Values()
	r.Name, r.When = v[0], v[1]
	m.syncRules()
}

func (m *wizardModel) keyTrigger(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.commitTrigger()
		m.pop()
		return nil
	case "ctrl+e":
		// Choosing an event is the one action on this screen that is not typing,
		// so it gets its own key rather than a field.
		m.commitTrigger()
		m.openEventPick()
		return nil
	case "ctrl+p":
		m.openPathPick(m.trigger, m.trigger.FocusedIndex(), false)
		return nil
	}
	cmd := m.trigger.Update(k)
	// A form submits on enter at its last field and on ctrl+s anywhere, and a
	// submitted form stops accepting keys. Every form screen here therefore has
	// to treat a submit as the exit esc already performs; without it the form
	// silently swallows everything the user types next. Same shape in keyTuple,
	// keyVariable, keyIterator and keyFilter.
	if m.trigger.Completed() {
		m.commitTrigger()
		m.pop()
		return nil
	}
	// Commit as the user types so the preview and the problem list keep up. Same
	// shape in keyTuple, keyVariable, keyIterator and keyFilter.
	m.commitTrigger()
	return cmd
}

// applyEventType records the picked event on the rule, auto-filling name and
// when.
//
// Auto-fill must never destroy the user's work, so each field is only replaced
// when it is empty or still holds exactly what the previous pick wrote. That is
// what AutoName and AutoWhen record — without them a second pick could not tell
// "unchanged since I filled it" from "deliberately typed".
func (m *wizardModel) applyEventType(typ string) {
	r := m.rule()
	if r == nil {
		return
	}
	when := fmt.Sprintf("input.type == %q", typ)

	if r.Name == "" || r.Name == r.AutoName {
		r.Name = typ
	}
	if r.When == "" || r.When == r.AutoWhen {
		r.When = when
	}
	r.AutoName, r.AutoWhen = typ, when

	m.trigger.SetValues([]string{r.Name, r.When})
	m.syncRules()
}

// pasteID and fileID are the two non-catalog rows. They are prefixed so they
// can never collide with an event type.
const (
	pasteID = "__paste__"
	fileID  = "__file__"
)

// openEventPick shows the catalog plus the two escapes.
// recipeNote says what an event maps to, for the row the user reads before
// picking it. Nine of the twenty-one events map to nothing, and finding that
// out only after choosing one reads as a fault in the wizard — or in the
// user's own setup — rather than a fact about the event. The vocabulary is the
// rule editor's own ("tuple", "tuple filter"), so the row teaches the word the
// next screen will use.
// mappedCount reports how many catalog events ship a ready-made mapping.
// Derived rather than written down: the two places that quoted it by hand had
// drifted apart — the payload-kind screen promised all twenty-one mapped while
// the help one screen later said twelve — and the wrong number taught the user
// that an event mapping to nothing meant something was missing from their own
// setup, rather than being a fact about the event.
func mappedCount() int {
	n := 0
	for _, e := range auth0.Catalog() {
		if e.Recipe.Maps() {
			n++
		}
	}
	return n
}

func recipeNote(r auth0.Recipe) string {
	if n := len(r.Rule.Tuples); n > 0 {
		return plural(n, "tuple")
	}
	if n := len(r.Rule.Filters); n > 0 {
		return plural(n, "tuple filter")
	}
	return "no tuples"
}

func (m *wizardModel) openEventPick() {
	cat := auth0.Catalog()
	items := make([]uilist.Item, 0, len(cat)+2)
	for i, e := range cat {
		items = append(items, uilist.Item{
			// The note rides on the title because this list is compact: the
			// description is never drawn (see SetCompact in newWizard), so a row's
			// title is the only thing the user reads before choosing.
			TitleText: fmt.Sprintf("%s · %s", e.Type, recipeNote(e.Recipe)),
			DescText:  fmt.Sprintf("%s · %s", e.Group, e.Summary),
			Filter:    e.Type + " " + e.Group + " " + e.Summary,
			ID:        e.Type,
			Index:     i,
		})
	}
	items = append(items,
		uilist.Item{TitleText: "Paste JSON", DescText: "paste an event payload", Filter: "paste json", ID: pasteID, Index: len(cat)},
		uilist.Item{TitleText: "Load from file", DescText: "read an event payload from disk", Filter: "load file", ID: fileID, Index: len(cat) + 1},
	)
	m.events.SetItems(items)
	m.events.ResetFilter()
	// Reached straight from the fork there is no rule behind this pick yet, so
	// m.rule() would be a stale pointer left over from whatever was open before.
	// Answered through fromFork() rather than a second position test, so this
	// and acceptPick can never drift onto different answers to the same question.
	if !m.fromFork() {
		if r := m.rule(); r != nil && r.Sample != nil {
			m.events.SelectID(r.Sample.Label)
		}
	} else {
		m.events.SelectIndex(0)
	}
	m.push(screenEventPick)
}

// fromFork reports whether the screen on top of the stack was reached from
// the add-rule fork ("a" -> Auth0/JSON), as opposed to ctrl+e on a rule that
// already exists. screenPayloadKind is on the stack only during a fork,
// however many screens deep the current screen sits above it (straight to a
// pick, or via paste/file, or now via the recipe screen) — that presence, not
// its position, is the question being asked here. Every place that needs this
// decision calls it, rather than scanning the stack a second way: two
// screens making that call independently is how Task 6 shipped a hang.
func (m *wizardModel) fromFork() bool {
	for _, s := range m.stack {
		if s == screenPayloadKind {
			return true
		}
	}
	return false
}

// acceptPick creates the rule when the pick was reached from the add-rule
// fork, then attaches the sample and navigates on: to the new rule's hub for
// the fork, or back to the trigger form when a rule was already there.
// keyEventPick, keyEventPaste and keyEventFile all call this so none of them
// can diverge from the others on when the rule gets created.
// landOnNewRule unwinds the add-rule fork and opens the rule just appended.
// Both exits from the fork share it — the recipe the user accepted and the rule
// they start from an explain-only event — so the two cannot drift onto
// different answers to "where am I, and what does esc mean now".
//
// The fork's screens are popped rather than the stack being replaced. Replacing
// it leaves screenRules alone on the stack, and esc on the hub opens the save
// dialog: the user reaching for "back" half a second after accepting a mapping
// was instead asked whether to write the file.
func (m *wizardModel) landOnNewRule() {
	for m.top() != screenPayloadKind {
		m.pop()
	}
	m.pop() // payload kind -> rules hub
	m.push(screenRule)
}

func (m *wizardModel) acceptPick(label string, event map[string]any) {
	fork := m.fromFork()
	if fork {
		// Reached from the fork, with no rule behind it yet. The rule is
		// created here, on accept, rather than when the fork was entered —
		// otherwise esc on an abandoned pick would strand an empty rule on the
		// hub.
		m.doc.Rules = append(m.doc.Rules, mapping.Rule{})
		m.ruleIdx = len(m.doc.Rules) - 1
		m.syncRules()
	}
	m.setSample(label, event)
	if fork {
		m.landOnNewRule()
		return
	}
	for m.top() != screenTrigger {
		m.pop()
	}
}

func (m *wizardModel) keyEventPick(k tea.KeyPressMsg) tea.Cmd {
	if m.events.SettingFilter() {
		// Resync the same way keyPathPick does: wizardModel.Update only ever
		// routes tea.KeyPressMsg (plus window size, spinner tick and
		// model-loaded) back into the screen, so the tea.Cmd bubbles' filtering
		// returns to recompute matches never comes back — list.FilterMatchesMsg
		// is dropped for real users too, not only in a keystroke-at-a-time test.
		// Without this, "enter" sees stale (or no) matches and resets the filter
		// instead of accepting it.
		cmd := m.events.Update(k)
		m.events.ResyncFilter()
		return cmd
	}
	switch k.String() {
	case "esc":
		m.pop()
		return nil
	case "enter":
		it, ok := m.events.Selected()
		if !ok {
			return nil
		}
		switch it.ID {
		case pasteID:
			m.paste.SetValue("")
			m.push(screenEventPaste)
			return m.paste.Focus()
		case fileID:
			m.push(screenEventFile)
			m.eventPath.Resume()
			return nil
		default:
			e, ok := auth0.Lookup(it.ID)
			if !ok {
				m.errMsg = fmt.Sprintf("unknown event %q", it.ID)
				return nil
			}
			// From the fork the user is creating a rule out of a recipe, so the
			// worked mapping shows first. From ctrl+e they already have a rule and
			// are changing its event, so the pick attaches directly — a one-key
			// "use this" would otherwise mean "overwrite what I wrote".
			if m.fromFork() {
				m.openRecipe(e)
			} else {
				m.acceptPick(e.Type, e.Sample)
			}
		}
		return nil
	}
	return m.events.Update(k)
}

func (m *wizardModel) keyEventPaste(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.pop()
		return nil
	case "ctrl+d":
		// enter inserts a newline in a textarea, so accepting needs its own key.
		event, err := decodeEvent([]byte(m.paste.Value()))
		if err != nil {
			m.errMsg = err.Error()
			return nil
		}
		m.acceptPick(eventLabel(event), event)
		return nil
	}
	var cmd tea.Cmd
	m.paste, cmd = m.paste.Update(k)
	return cmd
}

func (m *wizardModel) keyEventFile(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.pop()
		return nil
	case "enter":
		path := strings.TrimSpace(m.eventPath.Values()[0])
		raw, err := os.ReadFile(path)
		if err != nil {
			m.errMsg = fmt.Sprintf("could not read %s: %v", path, err)
			return nil
		}
		event, err := decodeEvent(raw)
		if err != nil {
			m.errMsg = err.Error()
			return nil
		}
		m.acceptPick(eventLabel(event), event)
		return nil
	}
	return m.eventPath.Update(k)
}

// setSample attaches the event to the current rule and auto-fills from its type.
func (m *wizardModel) setSample(label string, event map[string]any) {
	r := m.rule()
	if r == nil {
		return
	}
	r.Sample = &mapping.Sample{Label: label, Event: event}
	if typ, ok := event["type"].(string); ok && typ != "" {
		m.applyEventType(typ)
	}
	m.syncRules()
}

// decodeEvent parses an event payload, rejecting anything that is not a JSON
// object — mapper's `input` is a map, and a bare array or scalar would fail
// later with a much worse message.
func decodeEvent(raw []byte) (map[string]any, error) {
	var event map[string]any
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if len(event) == 0 {
		return nil, errors.New("the event is empty")
	}
	return event, nil
}

// eventLabel names a sample for the hub and the preview header.
func eventLabel(event map[string]any) string {
	if typ, ok := event["type"].(string); ok && typ != "" {
		return typ
	}
	return "pasted event"
}
