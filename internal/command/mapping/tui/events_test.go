package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

func forkPick(s *Session) component { return newEventPick(s, Focus{}, true) }

// toPick presses ctrl+e on the trigger form and returns the pick it opens.
func toPick(t *testing.T, h *harness) *eventPick {
	t.Helper()
	h.press("ctrl+e")
	return topAs[*eventPick](t, h)
}

func TestEventPickListsTheCatalogAndTheTwoEscapes(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	toPick(t, h)
	v := plain(h.view(100, 60))
	for _, want := range []string{"organization.member.added", "Paste JSON", "Load from file"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
}

func TestPickingACatalogEventSetsTheSampleAndAutoFills(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	if !toPick(t, h).list.SelectID("organization.member.added") {
		t.Fatal("could not select the event")
	}
	h.press("enter")
	f := topAs[*triggerForm](t, h)
	r := h.s.Doc.Rules[0]
	if r.Sample == nil || r.Sample.Label != "organization.member.added" || r.Sample.Event["type"] != "organization.member.added" {
		t.Fatalf("sample = %+v", r.Sample)
	}
	if r.Name != "organization.member.added" || r.When != `input.type == "organization.member.added"` {
		t.Fatalf("rule = %+v", r)
	}
	if got := f.form.Values()[0]; got != "organization.member.added" {
		t.Fatalf("the form shows %q, not the auto-filled name", got)
	}
	if n := len(h.s.Doc.Rules); n != 1 {
		t.Fatalf("rules = %d, want ctrl+e to append none", n)
	}
}

func TestCtrlEStartsOnTheRulesSample(t *testing.T) {
	d := freshRule()
	d.Rules[0].Sample = &mapping.Sample{Label: "user.created", Event: userCreatedEvent()}
	h := newHarness(t, d, nil, openTrigger)
	if it, ok := toPick(t, h).list.Selected(); !ok || it.ID != "user.created" {
		t.Fatalf("selected = %+v, want the rule's own sample", it)
	}
}

func pasteInto(t *testing.T, h *harness, payload string) {
	t.Helper()
	toPick(t, h).list.SelectID(pasteID)
	h.press("enter")
	topAs[*eventPaste](t, h)
	h.send(tea.PasteMsg{Content: payload})
	h.press("ctrl+d")
}

func TestPastedJSONBecomesTheSample(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	pasteInto(t, h, `{"type": "user.created", "data": {"object": {"user_id": "u1"}}}`)
	topAs[*triggerForm](t, h)
	r := h.s.Doc.Rules[0]
	if r.Sample == nil || r.Sample.Label != "user.created" || r.Name != "user.created" {
		t.Fatalf("rule = %+v", r)
	}
	if r.Sample.Event["type"] != "user.created" || r.When != `input.type == "user.created"` {
		t.Fatalf("event = %+v, when = %q", r.Sample.Event, r.When)
	}
	if n := len(h.s.Doc.Rules); n != 1 {
		t.Fatalf("rules = %d, want a paste from ctrl+e to append none", n)
	}
}

func TestPastedJSONWithoutATypeStillWorks(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	pasteInto(t, h, `{"data": {"id": "x"}}`)
	r := h.s.Doc.Rules[0]
	if r.Sample == nil || r.Sample.Label != "pasted event" || r.Name != "" {
		t.Fatalf("rule = %+v", r)
	}
	// With no type there is nothing to auto-fill `when` from, so it stays empty.
	if r.When != "" {
		t.Fatalf("when = %q, want empty", r.When)
	}
}

func TestBadPastedJSONStaysOnTheScreen(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	pasteInto(t, h, `{nope`)
	topAs[*eventPaste](t, h)
	if toast := h.lastToast(); !toast.Err || !strings.Contains(toast.Text, "not an event: line 1, column") {
		t.Fatalf("toast = %+v", toast)
	}
	if h.s.Doc.Rules[0].Sample != nil {
		t.Fatal("a bad paste became the sample")
	}
}

func TestAcceptingAnEmptyPasteSaysSoInEnglish(t *testing.T) {
	for _, raw := range []string{"", "   \n\t "} {
		h := newHarness(t, freshRule(), nil, openTrigger)
		pasteInto(t, h, raw)
		want := "nothing pasted yet — paste one event payload, then press ctrl+d"
		if toast := h.lastToast(); !toast.Err || toast.Text != want {
			t.Fatalf("paste %q: toast = %+v, want %q", raw, toast, want)
		}
	}
}

func loadFrom(t *testing.T, h *harness, path string) {
	t.Helper()
	toPick(t, h).list.SelectID(fileID)
	h.press("enter")
	topAs[*eventFile](t, h)
	h.typeText(path)
	h.press("enter")
}

func TestLoadEventFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, []byte(`{"type": "user.created", "data": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, freshRule(), nil, openTrigger)
	loadFrom(t, h, path)
	topAs[*triggerForm](t, h)
	if r := h.s.Doc.Rules[0]; r.Sample == nil || r.Sample.Label != "user.created" || r.Sample.Event["type"] != "user.created" {
		t.Fatalf("rule = %+v", r)
	}
	if n := len(h.s.Doc.Rules); n != 1 {
		t.Fatalf("rules = %d, want a file from ctrl+e to append none", n)
	}
}

func TestMissingEventFileStaysOnTheField(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	loadFrom(t, h, filepath.Join(t.TempDir(), "missing.json"))
	topAs[*eventFile](t, h)
	if toast := h.lastToast(); !toast.Err || !strings.Contains(toast.Text, "could not read") {
		t.Fatalf("toast = %+v", toast)
	}
}

func TestAnEmptyEventFileSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty event.json")
	if err := os.WriteFile(path, []byte(" \n\t"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, freshRule(), nil, openTrigger)
	loadFrom(t, h, path)
	topAs[*eventFile](t, h)
	want := path + " holds no event"
	if toast := h.lastToast(); !toast.Err || toast.Text != want {
		t.Fatalf("toast = %+v, want %q", toast, want)
	}
	if h.s.Doc.Rules[0].Sample != nil {
		t.Fatal("an empty file became the sample")
	}
}

func TestEscFromThePasteReturnsToThePick(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	toPick(t, h).list.SelectID(pasteID)
	h.press("enter", "esc")
	topAs[*eventPick](t, h)
	if h.s.Doc.Rules[0].Sample != nil {
		t.Fatal("cancelling the paste set a sample")
	}
}

// The fork's pick commits nothing: there is no rule yet, and a catalog event
// goes back to the host to show its recipe first.
func TestTheForkPickHandsBackTheCatalogEvent(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, forkPick)
	topAs[*eventPick](t, h).list.SelectID("organization.member.added")
	h.press("enter")
	p, ok := h.result.(Picked)
	if !h.closed || !ok || p.Catalog == nil || p.Catalog.Type != "organization.member.added" || p.Label != "organization.member.added" {
		t.Fatalf("closed = %v, result = %#v", h.closed, h.result)
	}
	if len(h.s.Doc.Rules) != 0 {
		t.Fatal("the fork's pick wrote to the document")
	}
}

func TestTheForkPickHandsBackAPastedPayload(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, forkPick)
	topAs[*eventPick](t, h).list.SelectID(pasteID)
	h.press("enter")
	h.send(tea.PasteMsg{Content: `{"type": "custom.thing"}`})
	h.press("ctrl+d")
	p, ok := h.result.(Picked)
	if !h.closed || !ok || p.Catalog != nil || p.Label != "custom.thing" {
		t.Fatalf("closed = %v, result = %#v", h.closed, h.result)
	}
}

func TestTheEventListSaysWhatEachEventMaps(t *testing.T) {
	h := newHarness(t, &mapping.Document{}, nil, forkPick)
	v := plain(h.view(100, 60))
	for _, want := range []string{"user.created · 2 tuples", "user.deleted · 3 tuple filters"} {
		if !strings.Contains(v, want) {
			t.Fatalf("the list does not say what the event maps: want %q in:\n%s", want, v)
		}
	}
	p := topAs[*eventPick](t, h)
	for id, want := range map[string]string{
		"organization.member.role.assigned": "organization.member.role.assigned · 1 tuple",
		"organization.updated":              "organization.updated · attributes",
	} {
		p.list.SelectID(id)
		if v := plain(h.view(100, 60)); !strings.Contains(v, want) {
			t.Fatalf("the list does not say what %s maps: want %q in:\n%s", id, want, v)
		}
	}
}

func TestTheEventHelpCountsTheMappedEvents(t *testing.T) {
	want := 0
	for _, e := range auth0.Catalog() {
		if e.Recipe.Maps() {
			want++
		}
	}
	if got := MappedCount(); got != want {
		t.Fatalf("MappedCount() = %d, want %d", got, want)
	}
	_, body := newEventPick(NewSession(t.Context(), &mapping.Document{}, nil), Focus{}, true).help()
	if !strings.Contains(body, fmt.Sprintf("%d of the %d", want, len(auth0.Catalog()))) {
		t.Fatalf("the event help misstates the catalog: %q", body)
	}
}

// A rule's own tuples and its iterator's both count. connection.created writes
// a tenant tuple and one per client; counting only the rule's own read "1
// tuple" for a recipe whose sample produces three.
func TestTheRowCountsTheIteratorsTuplesToo(t *testing.T) {
	e, ok := auth0.Lookup("connection.created")
	if !ok {
		t.Fatal("connection.created is not in the catalog")
	}
	if got := recipeNote(e.Recipe); got != "2 tuples" {
		t.Fatalf("recipeNote = %q, want %q", got, "2 tuples")
	}
}

// One explain-only event is "the other", not "the rest".
func TestThePickHelpCountsTheExplainOnlyEventsInTheRightNumber(t *testing.T) {
	_, body := (&eventPick{}).help()
	if others := len(auth0.Catalog()) - MappedCount(); others == 1 && strings.Contains(body, "the rest") {
		t.Fatalf("one explain-only event is described in the plural: %q", body)
	}
}

func TestPasteAndFileComeFirst(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	toPick(t, h)
	lines := strings.Split(plain(h.view(80, 24)), "\n")
	var rows []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			rows = append(rows, l)
		}
	}
	paste, file, first := -1, -1, -1
	for i, l := range rows {
		switch {
		case strings.Contains(l, "Paste JSON"):
			paste = i
		case strings.Contains(l, "Load from file"):
			file = i
		case strings.Contains(l, "organization.member.added") && first < 0:
			first = i
		}
	}
	if paste < 0 || file != paste+1 || first < file {
		t.Fatalf("paste row %d, file row %d, first event row %d in:\n%s", paste, file, first, strings.Join(rows, "\n"))
	}
	for _, id := range []string{pasteID, fileID} {
		it := topAs[*eventPick](t, h)
		if !it.list.SelectID(id) {
			t.Fatalf("no row %s", id)
		}
	}
}

func TestAPasteErrorNamesLineAndColumn(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	pasteInto(t, h, "{\n  \"type\": nope\n}")
	topAs[*eventPaste](t, h)
	if toast := h.lastToast(); !toast.Err || !strings.Contains(toast.Text, "not an event: line 2, column") {
		t.Fatalf("toast = %+v", toast)
	}
}

func TestPastingAnArrayNamesWhatItGot(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	pasteInto(t, h, `[{"type": "x"}]`)
	want := "not an event: an event must be a JSON object, not an array"
	if toast := h.lastToast(); !toast.Err || toast.Text != want {
		t.Fatalf("toast = %+v, want %q", toast, want)
	}
}

func openFile(t *testing.T, h *harness) {
	t.Helper()
	toPick(t, h).list.SelectID(fileID)
	h.press("enter")
	topAs[*eventFile](t, h)
}

func TestAnEmptyPathAsksForOne(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	openFile(t, h)
	h.typeText("   ")
	h.press("enter")
	topAs[*eventFile](t, h)
	if toast := h.lastToast(); !toast.Err || toast.Text != "type the path of an event file" {
		t.Fatalf("toast = %+v", toast)
	}
}

func TestADirectoryIsRefused(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, freshRule(), nil, openTrigger)
	openFile(t, h)
	h.typeText(dir)
	h.press("enter")
	topAs[*eventFile](t, h)
	if toast := h.lastToast(); !toast.Err || toast.Text != dir+" is a folder" {
		t.Fatalf("toast = %+v", toast)
	}
}

func TestAnUnreadableFileIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions are not enforced here")
	}
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, []byte(`{"type":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, freshRule(), nil, openTrigger)
	openFile(t, h)
	h.typeText(path)
	h.press("enter")
	topAs[*eventFile](t, h)
	if toast := h.lastToast(); !toast.Err || !strings.HasPrefix(toast.Text, "could not read "+path+": ") {
		t.Fatalf("toast = %+v", toast)
	}
}

func openPaste(t *testing.T, h *harness) *eventPaste {
	t.Helper()
	toPick(t, h).list.SelectID(pasteID)
	h.press("enter")
	return topAs[*eventPaste](t, h)
}

func TestEscOnATypedPasteAsks(t *testing.T) {
	h := newHarness(t, freshRule(), nil, openTrigger)
	openPaste(t, h)
	h.send(tea.PasteMsg{Content: `{"type"`})
	h.press("esc")
	c := topAs[*confirm](t, h)
	if c.title != "Discard paste" || c.question != "Discard the pasted text?" || c.verb != "discard" {
		t.Fatalf("confirm = %+v", c)
	}
	h.press("y")
	topAs[*eventPick](t, h)
}

func TestEscOnAnEmptyPasteCloses(t *testing.T) {
	for _, text := range []string{"", " \n\t "} {
		h := newHarness(t, freshRule(), nil, openTrigger)
		openPaste(t, h)
		if text != "" {
			h.send(tea.PasteMsg{Content: text})
		}
		h.press("esc")
		topAs[*eventPick](t, h)
	}
}

func TestCancellingTheDiscardKeepsTheText(t *testing.T) {
	for _, k := range []string{"n", "esc", "enter"} {
		h := newHarness(t, freshRule(), nil, openTrigger)
		p := openPaste(t, h)
		h.send(tea.PasteMsg{Content: `{"type"`})
		h.press("esc", k)
		if got := topAs[*eventPaste](t, h); got != p || got.area.Value() != `{"type"` {
			t.Fatalf("after %q: same=%v value=%q", k, got == p, got.area.Value())
		}
	}
}
