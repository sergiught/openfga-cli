package tui

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// applyEvent is one row: an event and where it came from.
type applyEvent struct {
	label    string // "test <name>", "rule <name> sample", "paste 2", "events.jsonl:3"
	payload  map[string]any
	file     string // the loaded file it came from; "" for samples and pastes
	line     int    // its position in that file (1-based), for the resume command
	selected bool
	// sample marks a row from the file's tests and samples: it can be
	// deselected, but only the file can take it away.
	sample bool
	// fileEvents is how many events that file held, so the resume command
	// knows a selection that is all of them.
	fileEvents int
}

// applyRequest is what the Apply pane asks of its host.
type applyRequest int

const (
	// requestPlan asks the host to plan the selected events.
	requestPlan applyRequest = iota
	// requestApply asks the host to apply the plan on screen, asking first.
	requestApply
	// requestResume asks the host to plan again from the event the last apply
	// did not finish, then to apply it as requestApply does.
	requestResume
)

// planDropped tells the host the events or the selection changed, so a plan
// made from them, or being made, no longer holds.
type planDropped struct{ why string }

// applyPlan is a plan on screen and what it was made from.
type applyPlan struct {
	plan *mapping.Plan
	key  string    // planKey at planning time
	at   time.Time // w.now() when the plan landed
	from int       // 1 unless resuming
}

// applyView is the Apply pane's root component. It renders and edits the
// list; the workspace runs planning and applying (workspace_apply.go), and
// sets what shows under the list.
type applyView struct {
	s      *Session
	dir    string // where the file browser starts
	header string // the store line, already sanitized
	events []applyEvent
	list   *uilist.List
	pastes int // events pasted so far, which number the next "paste <k>"

	plan     *applyPlan // the plan on screen; nil when there is none
	planText []string   // its lines as the CLI prints them, sanitized, then its summary
	off      int        // how far the plan is scrolled
	page     int        // the rows the plan had when last drawn
	planErr  []string   // why the last planning failed, sanitized
	// report is how the last apply ended, sanitized. It and the resume point
	// outlast saves, reloads and a re-plan that fails or is cancelled: only a
	// new apply, a fresh plan from p, or another selection clears them.
	report    []string
	resumeAt  int      // the event r plans again from; 0 when r is not offered
	resumeHow []string // the CLI command that resumes, then what it needs, sanitized
	running   string   // "planning event 2 of 5" or "applying event 1 of 3"; "" when idle
	applying  bool     // events are being sent: the keys that would change them are refused
	outcome   string   // ✓ or ✗ after an apply, until the next plan
}

func newApplyView(s *Session, dir string) *applyView {
	l := uilist.New()
	l.SetCompact(true)
	return &applyView{s: s, dir: dir, list: l}
}

// setSamples puts events, the file's own, at the top of the list. A row
// still there keeps its selection, a new one starts deselected, and one gone
// is dropped. Pasted and loaded rows stay below them as they were.
func (c *applyView) setSamples(events []mapping.Event) {
	selected := map[string]bool{}
	for _, e := range c.events {
		if e.sample && e.selected {
			key, _ := mapping.SampleKey(e.payload)
			selected[key] = true
		}
	}
	rows := make([]applyEvent, 0, len(events)+len(c.events))
	for _, e := range events {
		key, _ := mapping.SampleKey(e.Payload)
		rows = append(rows, applyEvent{label: e.Label, payload: e.Payload, sample: true, selected: selected[key]})
	}
	for _, e := range c.events {
		if !e.sample {
			rows = append(rows, e)
		}
	}
	c.events = rows
	c.refresh()
}

// setHeader sets the store line the pane opens with.
func (c *applyView) setHeader(header string) { c.header = header }

// selected is how many events are selected.
func (c *applyView) selected() int {
	n := 0
	for _, e := range c.events {
		if e.selected {
			n++
		}
	}
	return n
}

// selectedEvents is a copy of every selected event, in order. Planning reads
// it in the background, while the rows and the document they share maps with
// may change.
func (c *applyView) selectedEvents() []map[string]any {
	var out []map[string]any
	for _, e := range c.events {
		if e.selected {
			out = append(out, cloneValue(e.payload).(map[string]any))
		}
	}
	return out
}

// cloneValue copies an event value deeply: the maps and slices JSON and YAML
// decode to, and the scalars as they are.
func cloneValue(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[k] = cloneValue(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = cloneValue(x)
		}
		return out
	}
	return v
}

// eventsFile is the file the selected events came from when they are all of
// its events, in its order, and nothing else is selected; "" otherwise. Only
// then does the CLI's --from-event count the same events the plan does.
func (c *applyView) eventsFile() string {
	file, n, of := "", 0, 0
	for _, e := range c.events {
		if !e.selected {
			continue
		}
		n++
		if e.file == "" || e.line != n || file != "" && e.file != file {
			return ""
		}
		file, of = e.file, e.fileEvents
	}
	if n != of {
		return ""
	}
	return file
}

// setPlan shows p, or no plan when p is nil.
func (c *applyView) setPlan(p *applyPlan) {
	c.plan, c.planText, c.off = p, nil, 0
	if p == nil {
		return
	}
	c.planText = append(mapping.PlanText(p.plan, style.SanitizeTerminal), mapping.PlanSummary(p.plan))
	if p.plan.Writes+p.plan.Deletes == 0 {
		c.planText = append(c.planText, "nothing to change")
	}
}

// setReport shows lines where the plan would be, and offers r from event
// resumeAt (0 offers nothing) beside how to resume from the CLI. Every line
// must be sanitized already.
func (c *applyView) setReport(lines []string, resumeAt int, how []string) {
	c.report, c.resumeAt, c.resumeHow, c.off = lines, resumeAt, how, 0
}

// clearResume forgets how the last apply ended: the report, r, and the badge.
func (c *applyView) clearResume() {
	c.setReport(nil, 0, nil)
	c.outcome = ""
}

// below is what shows under the count, past the progress line, wrapped at
// width (0 leaves it as it is): the plan, or else why planning failed above
// the last report. The resume command wraps as a command, so a copy of only
// its first rows cannot run.
func (c *applyView) below(width int) []string {
	var rows []string
	add := func(lines ...string) {
		for _, l := range lines {
			rows = append(rows, wrapLine(l, width)...)
		}
	}
	if c.plan != nil {
		add(c.planText...)
		return rows
	}
	add(c.planErr...)
	add(c.report...)
	if c.resumeAt > 0 {
		add(fmt.Sprintf("r resume from event %d", c.resumeAt))
	}
	if len(c.resumeHow) > 0 {
		rows = append(rows, wrapCommand(c.resumeHow[0], width)...)
		add(c.resumeHow[1:]...)
	}
	return rows
}

// dropped asks the host to drop the plan, because of why.
func (c *applyView) dropped(why string) { c.s.post(requestMsg{what: planDropped{why: why}}) }

// refresh redraws the rows from c.events. SetItems keeps the cursor and
// re-applies an active filter.
func (c *applyView) refresh() {
	items := make([]uilist.Item, len(c.events))
	for i, e := range c.events {
		// Labels carry test, rule and file names, and the type is the payload's.
		text := style.SanitizeTerminal(e.label)
		if typ, ok := e.payload["type"].(string); ok && typ != "" {
			text += " · " + style.SanitizeTerminal(typ)
		}
		mark := "○ "
		if e.selected {
			mark = "◉ "
		}
		items[i] = uilist.Item{TitleText: mark + text, Filter: text, ID: strconv.Itoa(i), Index: i}
	}
	c.list.SetItems(items)
}

// add appends rows, selected, and puts the cursor on the first of them.
func (c *applyView) add(rows []applyEvent) {
	first := len(c.events)
	c.events = append(c.events, rows...)
	c.refresh()
	c.list.SelectID(strconv.Itoa(first))
}

// addPasted adds events from a paste, each numbered across the session.
func (c *applyView) addPasted(events []map[string]any) {
	rows := make([]applyEvent, len(events))
	for i, e := range events {
		c.pastes++
		rows[i] = applyEvent{label: fmt.Sprintf("paste %d", c.pastes), payload: e, selected: true}
	}
	c.add(rows)
	c.dropped("events were pasted")
}

// load adds the events in the file at path, each named by its place there.
func (c *applyView) load(path string) {
	raw, err := readEventFile(path)
	if err != nil {
		c.s.post(toastMsg{text: err.Error(), err: true})
		return
	}
	events, err := mapping.ParseEvents(bytes.NewReader(raw), path)
	if err != nil {
		c.s.post(toastMsg{text: style.SanitizeTerminal(err.Error()), err: true})
		return
	}
	base := filepath.Base(path)
	rows := make([]applyEvent, len(events))
	for i, e := range events {
		rows[i] = applyEvent{label: fmt.Sprintf("%s:%d", base, i+1), payload: e, file: path, line: i + 1,
			fileEvents: len(events), selected: true}
	}
	c.add(rows)
	c.dropped("events were loaded")
}

// remove drops the pasted or loaded row under the cursor. The file's own
// rows go only when the file loses them.
func (c *applyView) remove() {
	it, ok := c.list.Selected()
	if !ok {
		return
	}
	if c.events[it.Index].sample {
		c.s.post(toastMsg{text: "samples can be deselected, not removed"})
		return
	}
	c.events = slices.Delete(c.events, it.Index, it.Index+1)
	c.refresh()
	c.dropped("an event was removed")
}

func (c *applyView) Update(msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case closedMsg:
		if c.applying && msg.result != nil {
			// A paste box or browser left open: what it brings would change
			// the events under the apply.
			c.s.post(toastMsg{text: applyRunning, err: true})
			return c, nil
		}
		switch r := msg.result.(type) {
		case []map[string]any:
			c.addPasted(r)
		case string:
			c.load(r)
		}
		return c, nil
	case tea.KeyPressMsg:
		return c, c.key(msg)
	}
	return c, c.list.Update(msg)
}

func (c *applyView) key(k tea.KeyPressMsg) tea.Cmd {
	if c.list.SettingFilter() {
		cmd := c.list.Update(k)
		c.list.ResyncFilter()
		return cmd
	}
	s := k.String()
	if c.applying {
		switch s {
		case "space", "v", "o", "d", "p", "a", "r":
			c.s.post(toastMsg{text: applyRunning, err: true})
			return nil
		}
	}
	// pgup and pgdown scroll what is under the list, and ↑↓ the plan too: with
	// a plan on screen it is what the keys are for. View clamps the offset.
	switch {
	case s == "pgup" && len(c.below(0)) > 0:
		c.off = max(c.off-max(c.page-1, 1), 0)
		return nil
	case s == "pgdown" && len(c.below(0)) > 0:
		c.off += max(c.page-1, 1)
		return nil
	case s == "up" && c.plan != nil:
		c.off = max(c.off-1, 0)
		return nil
	case s == "down" && c.plan != nil:
		c.off++
		return nil
	}
	switch s {
	case "space":
		if it, ok := c.list.Selected(); ok {
			c.events[it.Index].selected = !c.events[it.Index].selected
			c.refresh()
			c.dropped("the selection changed")
		}
	case "v":
		c.s.post(openMsg{c: newApplyPaste(c.s)})
	case "o":
		c.s.post(openMsg{c: newFileBrowse(c.s, "Load events", c.dir, []string{".json", ".jsonl"})})
	case "d":
		c.remove()
	case "p":
		c.s.post(requestMsg{what: requestPlan})
	case "a":
		if c.plan != nil {
			c.s.post(requestMsg{what: requestApply})
		}
	case "r":
		if c.resumeAt > 0 {
			c.s.post(requestMsg{what: requestResume})
		}
	case "esc":
		c.s.post(doneMsg{})
	default:
		return c.list.Update(k)
	}
	return nil
}

func (c *applyView) View(width, height int) string {
	if len(c.events) == 0 {
		return emptyState("No events yet: the file has no tests or samples.", "v", "paste some, or o to load a file")
	}
	count := style.Subtitle.Render(fmt.Sprintf("%d of %s selected", c.selected(), mapping.Plural(len(c.events), "event")))
	below := c.below(width)
	if c.running == "" && len(below) == 0 {
		c.list.SetSize(width, max(height-2, 1))
		return c.list.View() + "\n\n" + count
	}
	// Under the list: a blank, the count, a blank, the progress line, then the
	// plan or the report. The list keeps a third of the rows when that is too
	// long, and the plan scrolls.
	room := height - 3
	head := count + "\n\n"
	if c.running != "" {
		room--
		head += style.Subtitle.Render(c.running) + "\n"
	}
	floor := listMin
	if len(below) > 0 {
		// A plan or report the list squeezed to one row (a marker) is no plan.
		floor = min(listMin, max(room-belowMin, 1))
	}
	listH := max(min(len(c.events)+listChrome, max(room-len(below), room/3)), floor)
	c.list.SetSize(width, listH)
	c.page = max(room-listH, 1)
	c.off = min(c.off, max(len(below)-c.page, 0))
	var rest string
	if len(below) > 0 {
		rest = scrolled(slices.Clone(below), c.off, c.page)
	}
	shown := c.list.View()
	if listH < listMin {
		shown = fitHeight(shown, listH) // the list draws four rows at the fewest
	}
	return shown + "\n\n" + head + rest
}

const (
	// listChrome is the rows a list needs besides its items to show them all:
	// the filter line, and the blank and the page dots it keeps room for.
	listChrome = 3
	// listMin is the fewest rows a list draws, however few it is given.
	listMin = 4
	// belowMin is the fewest rows the plan or report gets, unless it has fewer lines.
	belowMin = 3
)

func (c *applyView) Keys() []string {
	switch {
	case len(c.events) == 0:
		return []string{"v paste", "o load", "esc back", "? help"}
	case c.applying:
		return []string{"ctrl+c stop", "? help"}
	case c.running != "":
		return []string{"esc cancel", "space select", "p plan", "? help"}
	case c.plan != nil:
		keys := []string{"↑↓ scroll", "p plan", "space select", "v paste", "o load", "d remove", "/ filter", "esc back", "? help"}
		if c.plan.plan.Writes+c.plan.plan.Deletes > 0 {
			keys = append([]string{"a apply"}, keys...)
		}
		return keys
	}
	keys := []string{"space select", "v paste", "o load", "d remove", "p plan", "/ filter", "esc back", "? help"}
	if c.resumeAt > 0 {
		keys = append([]string{"r resume"}, keys...)
	}
	return keys
}

func (c *applyView) heading() (string, string) { return "Apply", c.header }

func (c *applyView) help() (string, string) {
	return "Apply", "Apply runs the selected events through the saved file and writes the tuples " +
		"they make to the store. The list starts with the file's test inputs and its rules' samples. " +
		"v pastes more events (one JSON object, an array, or JSONL), and o loads them from a file.\n\n" +
		"space selects an event, and only the selected ones are sent. d removes a pasted or loaded " +
		"event; the file's own can only be deselected. p plans the selected events: nothing is " +
		"written until you have seen the plan and confirmed it.\n\n" +
		"↑↓ scroll the plan, and pgup/pgdown the plan or a report. a applies the plan after asking, " +
		"and only y says yes. " +
		"esc cancels planning. A plan is dropped when the events, the selection or the saved file " +
		"change, and one older than five minutes is made again before the question.\n\n" +
		"While events are sent, ctrl+c or q stops them between requests, and nothing quits until " +
		"the stop is reported. After a failure or a stop, r plans again from the event that did " +
		"not finish, and the CLI command that does the same is shown."
}

func (c *applyView) filtering() bool { return c.list.SettingFilter() }

// applyPaste takes events pasted as JSON: one object, an array, or JSONL. It
// closes with the events, or with nothing.
type applyPaste struct {
	s    *Session
	area textarea.Model
}

func newApplyPaste(s *Session) *applyPaste {
	a := textarea.New()
	a.Placeholder = `{"type": "...", "data": {"object": {}}}`
	return &applyPaste{s: s, area: a}
}

func (c *applyPaste) Init() tea.Cmd { return c.area.Focus() }

func (c *applyPaste) Update(msg tea.Msg) (component, tea.Cmd) {
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
			// enter inserts a newline in a textarea, so adding needs its own key.
			events, err := mapping.ParseEvents(strings.NewReader(c.area.Value()), "paste")
			if err != nil {
				c.s.post(toastMsg{text: style.SanitizeTerminal(err.Error()), err: true})
				return c, nil
			}
			c.s.post(doneMsg{result: events})
			return c, nil
		}
	}
	var cmd tea.Cmd
	c.area, cmd = c.area.Update(msg)
	return c, cmd
}

func (c *applyPaste) View(width, height int) string {
	c.area.SetWidth(width)
	c.area.SetHeight(height)
	return c.area.View()
}

func (c *applyPaste) Keys() []string { return []string{"ctrl+d add", "esc cancel"} }

func (c *applyPaste) heading() (string, string) {
	return "Paste events", "Paste one JSON object, an array, or JSONL."
}

func (c *applyPaste) typing() bool { return true }
