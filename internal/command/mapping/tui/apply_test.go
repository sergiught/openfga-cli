package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// twoEvents is what the file gives the list: a test input and a sample.
func twoEvents() []mapping.Event {
	return []mapping.Event{
		{Label: "test added", Payload: map[string]any{"type": "organization.member.added"}},
		{Label: "rule member-removed sample", Payload: map[string]any{"type": "organization.member.deleted"}},
	}
}

// applyHarness opens the Apply list on events, with the file browser starting
// in dir.
func applyHarness(t *testing.T, dir string, events []mapping.Event) (*harness, *applyView) {
	t.Helper()
	var view *applyView
	h := newHarness(t, &mapping.Document{}, nil, func(s *Session) component {
		view = newApplyView(s, dir)
		view.setSamples(events)
		return view
	})
	return h, view
}

// labels is every row's label, in order.
func (c *applyView) labels() []string {
	out := make([]string, len(c.events))
	for i, e := range c.events {
		out[i] = e.label
	}
	return out
}

// selection is every row's selected flag, in order.
func (c *applyView) selection() []bool {
	out := make([]bool, len(c.events))
	for i, e := range c.events {
		out[i] = e.selected
	}
	return out
}

func TestSpaceSelectsEvents(t *testing.T) {
	h, c := applyHarness(t, t.TempDir(), twoEvents())
	if got := c.selection(); !slices.Equal(got, []bool{false, false}) {
		t.Fatalf("the file's events start %v, want deselected", got)
	}
	h.press("space")
	if got := c.selection(); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("after space: %v", got)
	}
	if v := h.view(80, 20); !strings.Contains(v, "1 of 2 events selected") {
		t.Fatalf("no count line:\n%s", v)
	}
	h.press("down", "space")
	if v := h.view(80, 20); !strings.Contains(v, "2 of 2 events selected") {
		t.Fatalf("after selecting both:\n%s", v)
	}
	h.press("space")
	if got := c.selection(); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("space again did not deselect: %v", got)
	}

	// The file's events again, as a save or a reload brings them: a selection
	// survives, a new event starts deselected, and one gone is dropped.
	c.setSamples([]mapping.Event{
		{Label: "test added", Payload: map[string]any{"type": "organization.member.added"}},
		{Label: "test new", Payload: map[string]any{"type": "organization.updated"}},
	})
	if got := c.labels(); !slices.Equal(got, []string{"test added", "test new"}) {
		t.Fatalf("labels = %v", got)
	}
	if got := c.selection(); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("selection after a re-sync = %v", got)
	}
}

func TestPastedEventsJoinTheList(t *testing.T) {
	h, c := applyHarness(t, t.TempDir(), twoEvents())
	h.press("v")
	p := topAs[*applyPaste](t, h)
	if !h.st.Typing() || !slices.Equal(p.Keys(), []string{"ctrl+d add", "esc cancel"}) {
		t.Fatalf("typing = %v, keys = %v", h.st.Typing(), p.Keys())
	}
	h.send(tea.PasteMsg{Content: "{\"type\": \"a\"}\n{\"type\": \"b\"}"})
	h.press("ctrl+d")
	topAs[*applyView](t, h)
	want := []string{"test added", "rule member-removed sample", "paste 1", "paste 2"}
	if got := c.labels(); !slices.Equal(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	if got := c.selection(); !slices.Equal(got, []bool{false, false, true, true}) {
		t.Fatalf("pasted events start %v, want selected", got)
	}

	// An array is events too, and the numbering carries on.
	h.press("v")
	h.send(tea.PasteMsg{Content: `[{"type": "c"}]`})
	h.press("ctrl+d")
	if got := c.labels(); got[len(got)-1] != "paste 3" {
		t.Fatalf("labels = %v", got)
	}

	// The file's events again keep every pasted row and its selection.
	c.events[2].selected = false
	c.setSamples(twoEvents())
	if got := c.labels(); len(got) != 5 || c.events[2].selected || !c.events[3].selected {
		t.Fatalf("after a re-sync: %v %v", got, c.selection())
	}

	// esc with text asks, and only y drops it.
	h.press("v")
	p = topAs[*applyPaste](t, h)
	h.send(tea.PasteMsg{Content: `{"type"`})
	h.press("esc")
	if cf := topAs[*confirm](t, h); cf.title != "Discard paste" || cf.question != "Discard the pasted text?" || cf.verb != "discard" {
		t.Fatalf("confirm = %+v", cf)
	}
	h.press("n")
	if got := topAs[*applyPaste](t, h); got != p || got.area.Value() != `{"type"` {
		t.Fatalf("a cancelled discard lost the text: same = %v, value = %q", got == p, got.area.Value())
	}
	h.press("esc", "y")
	topAs[*applyView](t, h)
	if len(c.events) != 5 {
		t.Fatalf("a discarded paste added rows: %v", c.labels())
	}

	// esc on a blank paste just closes.
	h.press("v")
	h.send(tea.PasteMsg{Content: " \n "})
	h.press("esc")
	topAs[*applyView](t, h)
}

func TestPastedEventErrorsNameTheLine(t *testing.T) {
	h, c := applyHarness(t, t.TempDir(), twoEvents())
	h.press("v")
	h.send(tea.PasteMsg{Content: "{\"type\": \"a\"}\n{\"type\": nope}"})
	h.press("ctrl+d")
	topAs[*applyPaste](t, h)
	if toast := h.lastToast(); !toast.Err || !strings.HasPrefix(toast.Text, "paste:2:") {
		t.Fatalf("toast = %+v, want an error at paste:2:", toast)
	}
	if len(c.events) != 2 {
		t.Fatalf("a paste with an error added rows: %v", c.labels())
	}
	h.press("esc", "y", "v", "ctrl+d")
	if toast := h.lastToast(); !toast.Err || toast.Text != "no events in paste" {
		t.Fatalf("an empty paste toasts %+v", toast)
	}
}

func TestLoadedEventsJoinTheList(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "events.jsonl")
	writeFile(t, events, "{\"type\": \"a\"}\n{\"type\": \"b\"}\n")
	writeFile(t, filepath.Join(dir, "notes.txt"), "not events")
	h, c := applyHarness(t, dir, twoEvents())

	h.press("o")
	b := topAs[*fileBrowse](t, h)
	var listed []string
	for _, it := range b.list.Model.Items() {
		listed = append(listed, it.FilterValue())
	}
	if !slices.Contains(listed, "events.jsonl") || slices.Contains(listed, "notes.txt") {
		t.Fatalf("the browser lists %v", listed)
	}
	b.list.SelectID(events)
	h.press("enter")
	topAs[*applyView](t, h)
	want := []string{"test added", "rule member-removed sample", "events.jsonl:1", "events.jsonl:2"}
	if got := c.labels(); !slices.Equal(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	if e := c.events[3]; e.file != events || e.line != 2 || !e.selected || e.payload["type"] != "b" {
		t.Fatalf("row = %+v", e)
	}

	// Typed paths the list would not offer, refused the way the paste form refuses
	// them, and files that hold no events.
	empty := filepath.Join(dir, "empty.json")
	writeFile(t, empty, "")
	bad := filepath.Join(dir, "bad.json")
	writeFile(t, bad, "{\"type\": \"a\"}\n{\"type\": nope}\n")
	gone := filepath.Join(dir, "gone.json")
	cases := map[string]string{
		gone:  "could not read " + gone + ": ",
		empty: "no events in " + empty,
		bad:   bad + ":2:",
	}
	if runtime.GOOS != "windows" {
		cases[os.DevNull] = os.DevNull + " is not a regular file"
	}
	refused := func(path, want string) {
		t.Helper()
		topAs[*applyView](t, h)
		if toast := h.lastToast(); !toast.Err || !strings.HasPrefix(toast.Text, want) {
			t.Errorf("%s: toast = %+v, want %q", path, toast, want)
		}
		if len(c.events) != 4 {
			t.Fatalf("%s: a refused file added rows: %v", path, c.labels())
		}
	}
	for path, want := range cases {
		h.press("o", "ctrl+p")
		h.typeText(path)
		h.press("enter")
		refused(path, want)
	}
	// The browser walks into a typed folder, so only a folder it closes with
	// reaches the list.
	h.send(closedMsg{result: dir})
	refused(dir, dir+" is a folder")
}

func TestSamplesCannotBeRemoved(t *testing.T) {
	h, c := applyHarness(t, t.TempDir(), twoEvents())
	h.press("d")
	if toast := h.lastToast(); toast.Text != "samples can be deselected, not removed" {
		t.Fatalf("toast = %+v", toast)
	}
	if len(c.events) != 2 {
		t.Fatalf("d removed a row from the file: %v", c.labels())
	}
	h.press("v")
	h.send(tea.PasteMsg{Content: `{"type": "a"}`})
	h.press("ctrl+d")
	// The cursor is on the pasted row.
	h.press("d")
	if got := c.labels(); !slices.Equal(got, []string{"test added", "rule member-removed sample"}) {
		t.Fatalf("after removing the pasted row: %v", got)
	}
}

func TestTheEventsFileIsAWholeFileInOrder(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "events.jsonl")
	writeFile(t, events, "{\"type\": \"a\"}\n{\"type\": \"b\"}\n")
	_, c := applyHarness(t, dir, twoEvents())
	c.load(events)
	if got := c.eventsFile(); got != events {
		t.Fatalf("all of one file names %q", got)
	}
	c.events[0].selected = true
	if got := c.eventsFile(); got != "" {
		t.Fatalf("a sample beside the file names %q", got)
	}
	c.events[0].selected = false
	c.events[2].selected = false
	if got := c.eventsFile(); got != "" {
		t.Fatalf("part of the file names %q", got)
	}
	// Loaded twice, its second event from the first load, then its first from
	// the second, are as many events as it holds, but not in its order.
	c.load(events)
	c.events[5].selected = false
	if got := c.eventsFile(); got != "" {
		t.Fatalf("the file out of order names %q (selection %v)", got, c.selection())
	}
	c.events[3].selected = false
	c.events[5].selected = true
	if got := c.eventsFile(); got != events {
		t.Fatalf("the second load whole names %q (selection %v)", got, c.selection())
	}
}
