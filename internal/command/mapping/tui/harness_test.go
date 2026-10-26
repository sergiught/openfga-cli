package tui

import (
	"context"
	"reflect"
	"regexp"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// ansiSeq matches the colour and style escapes lipgloss writes, so assertions
// about wording read against the text alone.
var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(view string) string { return ansiSeq.ReplaceAllString(view, "") }

// key builds a key press the way the tests here do: named keys by code,
// anything else as text, so key("ctrl+p").String() is "ctrl+p".
func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "alt+enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "ctrl+t":
		return tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
}

// harness drives a real Stack the way a host does, and keeps what the host
// would have been told.
type harness struct {
	t        *testing.T
	s        *Session
	st       *Stack
	changes  int
	toasts   []Toast
	requests []any
	closed   bool
	result   any
}

func newHarness(t *testing.T, doc *mapping.Document, ix *mapping.ModelIndex, open func(s *Session) component) *harness {
	t.Helper()
	s := NewSession(context.Background(), doc, ix)
	h := &harness{t: t, s: s, st: newStack(s, open(s))}
	h.st.Init()
	return h
}

func (h *harness) send(msgs ...tea.Msg) {
	for _, msg := range msgs {
		_, out := h.st.Update(msg)
		if out.Changed {
			h.changes++
		}
		h.toasts = append(h.toasts, out.Toasts...)
		h.requests = append(h.requests, out.Requests...)
		if out.Closed {
			h.closed, h.result = true, out.Result
		}
	}
}

func (h *harness) press(keys ...string) {
	for _, k := range keys {
		h.send(key(k))
	}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (h *harness) top() component            { return h.st.top() }
func (h *harness) view(w, height int) string { return h.st.View(w, height) }

func (h *harness) lastToast() Toast {
	h.t.Helper()
	if len(h.toasts) == 0 {
		h.t.Fatal("no toast was posted")
	}
	return h.toasts[len(h.toasts)-1]
}

// topAs is the component on top as T, failing the test when it is another.
func topAs[T component](t *testing.T, h *harness) T {
	t.Helper()
	c, ok := h.top().(T)
	if !ok {
		t.Fatalf("top is %T", h.top())
	}
	return c
}

// backspace deletes the last n characters of the focused field. A value set
// into a field starts with the caret at 0, so it moves to the end first. key()
// cannot spell either key: its default case would type the word.
func backspace(h *harness, n int) {
	h.send(tea.KeyPressMsg{Code: tea.KeyEnd})
	for range n {
		h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
}

// pathIDs is every path a picker offers, in order.
func pathIDs(p *pathPicker) []string {
	var out []string
	for _, it := range p.list.Model.Items() {
		out = append(out, it.(uilist.Item).ID)
	}
	return out
}

// assertOnlyEditsChangeTheDocument opens a form on an existing item twice.
// Moving through it and leaving (tab, tab, esc) edits nothing, so it must not
// report a change or leave the document different; typing one character is an
// edit, and must report one.
func assertOnlyEditsChangeTheDocument(t *testing.T, doc func() *mapping.Document, open func(s *Session) component) {
	t.Helper()
	h := newHarness(t, doc(), nil, open)
	h.press("tab", "tab", "esc")
	if h.changes != 0 {
		t.Fatalf("tab, tab, esc reported %d changes against an unchanged document", h.changes)
	}
	if !reflect.DeepEqual(h.s.Doc, doc()) {
		t.Fatalf("tab, tab, esc rewrote the document:\n%+v", h.s.Doc.Rules)
	}
	h = newHarness(t, doc(), nil, open)
	h.typeText("x")
	if h.changes < 1 {
		t.Fatal("typing a character reported no change")
	}
}
