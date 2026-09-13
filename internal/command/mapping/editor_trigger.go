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
	return m.trigger.Update(k)
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
func (m *wizardModel) openEventPick() {
	cat := auth0.Catalog()
	items := make([]uilist.Item, 0, len(cat)+2)
	for i, e := range cat {
		items = append(items, uilist.Item{
			TitleText: e.Type,
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
	if r := m.rule(); r != nil && r.Sample != nil {
		m.events.SelectID(r.Sample.Label)
	}
	m.push(screenEventPick)
}

func (m *wizardModel) keyEventPick(k tea.KeyPressMsg) tea.Cmd {
	if m.events.SettingFilter() {
		return m.events.Update(k)
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
		case fileID:
			m.push(screenEventFile)
		default:
			e, ok := auth0.Lookup(it.ID)
			if !ok {
				m.errMsg = fmt.Sprintf("unknown event %q", it.ID)
				return nil
			}
			m.setSample(e.Type, e.Sample)
			m.pop()
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
		m.setSample(eventLabel(event), event)
		m.pop() // paste -> event pick
		m.pop() // event pick -> trigger
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
		m.setSample(eventLabel(event), event)
		m.pop()
		m.pop()
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
