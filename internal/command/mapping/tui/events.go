package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// pasteID and fileID are the two non-catalog rows. They are prefixed so they
// can never collide with an event type.
const (
	pasteID = "__paste__"
	fileID  = "__file__"
)

// MappedCount reports how many catalog events ship a ready-made mapping.
// Derived rather than written down: the two places that quoted it by hand had
// drifted apart, and the wrong number taught the user that an event mapping to
// nothing meant something was missing from their own setup.
func MappedCount() int {
	n := 0
	for _, e := range auth0.Catalog() {
		if e.Recipe.Maps() {
			n++
		}
	}
	return n
}

// recipeNote says what an event maps to, for the row the user reads before
// picking it, in the rule editor's own vocabulary. The rule's own tuples and
// its iterator's are counted together, one per template: the number is a floor
// rather than a total, since an iterator writes its tuples once per item.
func recipeNote(r auth0.Recipe) string {
	if n := ruleTuples(r.Rule); n > 0 {
		return mapping.Plural(n, "tuple")
	}
	if n := len(r.Rule.Filters); n > 0 {
		return mapping.Plural(n, "tuple filter")
	}
	return r.Note
}

// parseEvent decodes pasted or loaded bytes into an event. Empty input says
// empty, which the paste and the file word apart; anything else reads "not an
// event: <where and why>". {} is decoded but refused: it has nothing to map.
func parseEvent(raw []byte, empty string) (map[string]any, error) {
	event, err := mapping.DecodeEvent(raw)
	if errors.Is(err, mapping.ErrNoEvent) {
		return nil, errors.New(empty)
	}
	if err != nil {
		return nil, fmt.Errorf("not an event: %s", style.SanitizeTerminal(err.Error()))
	}
	if len(event) == 0 {
		return nil, errors.New("the event is empty")
	}
	return event, nil
}

// readEventFile reads an event file, refusing a folder, any other file that is
// not regular, and anything unreadable, with errors worded for a toast. The
// path is sanitized in every message.
func readEventFile(path string) ([]byte, error) {
	shown := style.SanitizeTerminal(path)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %s", shown, style.SanitizeTerminal(err.Error()))
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a folder", shown)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", shown)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %s", shown, style.SanitizeTerminal(err.Error()))
	}
	return raw, nil
}

// eventLabel names a sample for the hub and the preview header.
func eventLabel(event map[string]any) string {
	if typ, ok := event["type"].(string); ok && typ != "" {
		return typ
	}
	return "pasted event"
}

// Picked is what an event pick closes with.
type Picked struct {
	Label string
	Event map[string]any
	// Catalog is the catalog event chosen, set only by the add-rule fork's
	// pick, which shows the event's recipe before anything is committed.
	// Nil for a paste or a file, and for every pick on an existing rule.
	Catalog *auth0.Event
}

// eventPick lists the two escapes, paste and file, then the catalog.
type eventPick struct {
	s    *Session
	at   Focus
	fork bool
	list *uilist.List
}

func newEventPick(s *Session, at Focus, fork bool) *eventPick {
	l := uilist.New()
	l.SetFilterPlaceholder("filter events")
	// Compact: the catalog plus the two escapes do not fit on one page at
	// title+description height, which would hide the escapes below the fold.
	// The filter still searches type, group and summary through each item's
	// Filter field even though the description is not drawn.
	l.SetCompact(true)
	cat := auth0.Catalog()
	items := make([]uilist.Item, 0, len(cat)+2)
	// Paste and file first: a user with their own payload should not scroll
	// the whole catalog to reach them.
	items = append(items,
		uilist.Item{TitleText: "Paste JSON", DescText: "paste an event payload", Filter: "paste json", ID: pasteID, Index: 0},
		uilist.Item{TitleText: "Load from file", DescText: "read an event payload from disk", Filter: "load file", ID: fileID, Index: 1},
	)
	for i, e := range cat {
		items = append(items, uilist.Item{
			// The note rides on the title: compact never draws the description.
			TitleText: fmt.Sprintf("%s · %s", e.Type, recipeNote(e.Recipe)),
			DescText:  fmt.Sprintf("%s · %s", e.Group, e.Summary),
			Filter:    e.Type + " " + e.Group + " " + e.Summary,
			ID:        e.Type,
			Index:     i + 2,
		})
	}
	l.SetItems(items)
	c := &eventPick{s: s, at: at, fork: fork, list: l}
	// The fork has no rule behind it yet; ctrl+e starts on the rule's own sample.
	if r := s.rule(at.Rule); !fork && r != nil && r.Sample != nil {
		l.SelectID(r.Sample.Label)
	} else {
		l.SelectIndex(0)
	}
	return c
}

func (c *eventPick) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case closedMsg:
		// A paste or a file the child accepted passes straight on.
		if p, ok := msg.result.(Picked); ok {
			c.s.post(doneMsg{result: p})
		}
		return c, nil
	case tea.KeyPressMsg:
		return c, c.key(msg)
	}
	return c, c.list.Update(msg)
}

func (c *eventPick) key(k tea.KeyPressMsg) tea.Cmd {
	if c.list.SettingFilter() {
		cmd := c.list.Update(k)
		c.list.ResyncFilter()
		return cmd
	}
	switch k.String() {
	case "esc":
		c.s.post(doneMsg{})
		return nil
	case "enter":
		it, ok := c.list.Selected()
		if !ok {
			return nil
		}
		switch it.ID {
		case pasteID:
			c.s.post(openMsg{c: newEventPaste(c.s)})
		case fileID:
			c.s.post(openMsg{c: newEventFile(c.s)})
		default:
			e, ok := auth0.Lookup(it.ID)
			if !ok {
				c.s.post(toastMsg{text: fmt.Sprintf("unknown event %q", it.ID), err: true})
				return nil
			}
			// From the fork the user is creating a rule out of a recipe, so the
			// worked mapping shows first. From ctrl+e they already have a rule
			// and are changing its event, so the pick attaches directly.
			p := Picked{Label: e.Type, Event: cloneEvent(e.Sample)}
			if c.fork {
				p.Catalog = &e
			}
			c.s.post(doneMsg{result: p})
		}
		return nil
	}
	return c.list.Update(k)
}

func (c *eventPick) View(width, height int) string {
	c.list.SetSize(width, height)
	return c.list.View()
}

func (c *eventPick) Keys() []string { return []string{"/ filter", "↵ select", "esc back", "? help"} }

func (c *eventPick) heading() (string, string) {
	return "Pick an event", "Choose a sample payload to build the rule against."
}

func (c *eventPick) help() (string, string) {
	mapped, all := MappedCount(), len(auth0.Catalog())
	return "Events", fmt.Sprintf("Pick the one event you want to map. %d of the %d come with a "+
		"ready-made mapping; any other shows why it needs none. Either way you end up with a rule you can edit.",
		mapped, all)
}

func (c *eventPick) filtering() bool { return c.list.SettingFilter() }

// eventPaste takes one event payload pasted as JSON.
type eventPaste struct {
	s    *Session
	area textarea.Model
}

func newEventPaste(s *Session) *eventPaste {
	a := textarea.New()
	a.Placeholder = `{"type": "...", "data": {"object": {}}}`
	return &eventPaste{s: s, area: a}
}

func (c *eventPaste) Init() tea.Cmd { return c.area.Focus() }

func (c *eventPaste) Update(msg tea.Msg) (component, tea.Cmd) {
	if m, ok := msg.(closedMsg); ok {
		// The discard confirm answered; only y throws the text away.
		if m.result == true {
			c.s.post(doneMsg{})
		}
		return c, nil
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			if strings.TrimSpace(c.area.Value()) == "" {
				c.s.post(doneMsg{})
				return c, nil
			}
			c.s.post(openMsg{c: newConfirm(c.s, "Discard paste", "Discard the pasted text?", "discard")})
			return c, nil
		case "ctrl+d":
			// enter inserts a newline in a textarea, so accepting needs its own key.
			event, err := parseEvent([]byte(c.area.Value()),
				"nothing pasted yet — paste one event payload, then press ctrl+d")
			if err != nil {
				c.s.post(toastMsg{text: err.Error(), err: true})
				return c, nil
			}
			c.s.post(doneMsg{result: Picked{Label: eventLabel(event), Event: event}})
			return c, nil
		}
	}
	var cmd tea.Cmd
	c.area, cmd = c.area.Update(msg)
	return c, cmd
}

func (c *eventPaste) View(width, height int) string {
	c.area.SetWidth(width)
	c.area.SetHeight(height)
	return c.area.View()
}

func (c *eventPaste) Keys() []string { return []string{"ctrl+d accept", "esc cancel"} }

func (c *eventPaste) heading() (string, string) {
	return "Paste an event", "Paste one event payload as JSON."
}

func (c *eventPaste) typing() bool { return true }

// eventFile reads a sample event from a JSON file.
type eventFile struct {
	s    *Session
	form *field.Form
}

func newEventFile(s *Session) *eventFile {
	return &eventFile{s: s, form: field.NewForm(field.New("Event file", "event.json"))}
}

func (c *eventFile) Init() tea.Cmd { return c.form.Init() }

func (c *eventFile) Update(msg tea.Msg) (component, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			c.s.post(doneMsg{})
			return c, nil
		case "enter":
			path := strings.TrimSpace(c.form.Values()[0])
			if path == "" {
				c.s.post(toastMsg{text: "type the path of an event file", err: true})
				return c, nil
			}
			raw, err := readEventFile(path)
			if err != nil {
				c.s.post(toastMsg{text: err.Error(), err: true})
				return c, nil
			}
			event, err := parseEvent(raw, style.SanitizeTerminal(path)+" holds no event")
			if err != nil {
				c.s.post(toastMsg{text: err.Error(), err: true})
				return c, nil
			}
			c.s.post(doneMsg{result: Picked{Label: eventLabel(event), Event: event}})
			return c, nil
		}
	}
	return c, c.form.Update(msg)
}

func (c *eventFile) View(width, height int) string {
	c.form.SetWidth(width)
	return c.form.View()
}

func (c *eventFile) Keys() []string { return []string{"↵ load", "esc back"} }

func (c *eventFile) heading() (string, string) {
	return "Load an event", "Read a sample event from a JSON file."
}

func (c *eventFile) typing() bool { return true }
