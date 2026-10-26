package tui

import (
	"context"
	"reflect"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// scripted is a component a test drives: each key posts whatever the test
// queued for it, and every message it receives is recorded.
type scripted struct {
	s     *Session
	title string
	onKey map[string][]tea.Msg
	onRet []tea.Msg // posted when a closedMsg arrives
	ret   tea.Cmd   // returned when a closedMsg arrives
	got   []tea.Msg
	init  tea.Cmd
}

func (c *scripted) Update(msg tea.Msg) (component, tea.Cmd) {
	c.got = append(c.got, msg)
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		for _, m := range c.onKey[msg.String()] {
			c.s.post(m)
		}
	case closedMsg:
		for _, m := range c.onRet {
			c.s.post(m)
		}
		return c, c.ret
	}
	return c, nil
}

func (c *scripted) View(int, int) string              { return c.title }
func (c *scripted) Keys() []string                    { return []string{"esc " + c.title} }
func (c *scripted) heading() (title, subtitle string) { return c.title, "about " + c.title }
func (c *scripted) Init() tea.Cmd                     { return c.init }

func newScripted(s *Session, title string) *scripted {
	return &scripted{s: s, title: title, onKey: map[string][]tea.Msg{}}
}

func testSession() *Session {
	return NewSession(context.Background(), &mapping.Document{}, nil)
}

func press(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }

func TestOpenPushesAndDonePopsWithTheResult(t *testing.T) {
	s := testSession()
	root, child := newScripted(s, "root"), newScripted(s, "child")
	root.onKey["o"] = []tea.Msg{openMsg{c: child}}
	child.onKey["x"] = []tea.Msg{doneMsg{result: "picked"}}
	st := newStack(s, root)

	st.Update(press("o"))
	if st.Depth() != 2 || st.View(0, 0) != "child" {
		t.Fatalf("after open: depth %d, view %q", st.Depth(), st.View(0, 0))
	}
	_, out := st.Update(press("x"))
	if st.Depth() != 1 || out.Closed {
		t.Fatalf("after done: depth %d, outcome %+v", st.Depth(), out)
	}
	last := root.got[len(root.got)-1]
	if !reflect.DeepEqual(last, closedMsg{result: "picked"}) {
		t.Fatalf("the root got %#v, want the child's result", last)
	}
}

func TestTheRootIsNeverPopped(t *testing.T) {
	s := testSession()
	root := newScripted(s, "root")
	root.onKey["x"] = []tea.Msg{doneMsg{result: 7}}
	st := newStack(s, root)

	_, out := st.Update(press("x"))
	if st.Depth() != 1 || !out.Closed || out.Result != 7 {
		t.Fatalf("depth %d, outcome %+v; want the root kept and its close reported", st.Depth(), out)
	}
}

func TestChangesAndToastsReachTheHost(t *testing.T) {
	s := testSession()
	root := newScripted(s, "root")
	root.onKey["x"] = []tea.Msg{docChangedMsg{}, toastMsg{text: "bad", err: true}, toastMsg{text: "fyi"}}
	st := newStack(s, root)

	_, out := st.Update(press("x"))
	want := Outcome{Changed: true, Toasts: []Toast{{Text: "bad", Err: true}, {Text: "fyi"}}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("outcome %+v, want %+v", out, want)
	}
}

// A request leaves the stack as it is: the host acts on it, in the order it was
// asked, and nothing is popped.
func TestRequestsReachTheHostInOrderWithoutClosing(t *testing.T) {
	s := testSession()
	root := newScripted(s, "root")
	root.onKey["x"] = []tea.Msg{requestMsg{what: RequestModel}, toastMsg{text: "fyi"}, requestMsg{what: RequestSave}}
	st := newStack(s, root)

	_, out := st.Update(press("x"))
	if want := []any{RequestModel, RequestSave}; !reflect.DeepEqual(out.Requests, want) {
		t.Fatalf("requests %v, want %v", out.Requests, want)
	}
	if out.Closed || st.Depth() != 1 {
		t.Fatalf("a request closed the stack: %+v, depth %d", out, st.Depth())
	}
}

// A root may open its first child from Init, as the add-rule fork does.
func TestInitSettlesWhatTheRootPosts(t *testing.T) {
	s := testSession()
	root, child := newScripted(s, "root"), newScripted(s, "child")
	child.init = func() tea.Msg { return nil }
	st := newStack(s, root)
	s.post(openMsg{c: child})

	if cmd := st.Init(); cmd == nil {
		t.Fatal("the opened child's Init was dropped")
	}
	if st.Depth() != 2 || len(s.posted) != 0 {
		t.Fatalf("depth %d, unsettled %v", st.Depth(), s.posted)
	}
}

// A parent that re-reads the document when its child closes posts in turn; the
// stack settles that too before returning, or the host would miss the change.
func TestPostsMadeWhileSettlingAreSettled(t *testing.T) {
	s := testSession()
	root, child := newScripted(s, "root"), newScripted(s, "child")
	root.onKey["o"] = []tea.Msg{openMsg{c: child}}
	root.onRet = []tea.Msg{docChangedMsg{}}
	child.onKey["x"] = []tea.Msg{doneMsg{}}
	st := newStack(s, root)

	st.Update(press("o"))
	_, out := st.Update(press("x"))
	if !out.Changed {
		t.Fatal("the parent's post while settling was dropped")
	}
	if len(s.posted) != 0 {
		t.Fatalf("left unsettled: %v", s.posted)
	}
}

func TestOpeningAComponentStartsIt(t *testing.T) {
	s := testSession()
	root, child := newScripted(s, "root"), newScripted(s, "child")
	child.init = func() tea.Msg { return "started" }
	root.onKey["o"] = []tea.Msg{openMsg{c: child}}
	st := newStack(s, root)

	cmd, _ := st.Update(press("o"))
	if got := cmdMsgs(cmd); !reflect.DeepEqual(got, []tea.Msg{"started"}) {
		t.Fatal("the child's Init command did not come back from Update")
	}
}

func TestResumeTellsTheTopItIsBack(t *testing.T) {
	s := testSession()
	root := newScripted(s, "root")
	st := newStack(s, root)
	st.Resume()
	if len(root.got) != 1 || root.got[0] != (closedMsg{}) {
		t.Fatalf("root got %#v, want one closedMsg", root.got)
	}
}

// Resume hands back every command it causes: the one the top returns on
// coming back, and the Init of whatever that opens. Dropping them left a
// component that starts work on resume waiting for a result that never came.
func TestResumeReturnsTheCommandsItCauses(t *testing.T) {
	s := testSession()
	root, child := newScripted(s, "root"), newScripted(s, "child")
	root.ret = func() tea.Msg { return "resumed" }
	root.onRet = []tea.Msg{openMsg{c: child}, toastMsg{text: "back"}}
	child.init = func() tea.Msg { return "started" }
	st := newStack(s, root)

	cmd, out := st.Resume()
	if got := cmdMsgs(cmd); !reflect.DeepEqual(got, []tea.Msg{"resumed", "started"}) {
		t.Fatalf("Resume's commands produced %v, want the top's and the child's Init", got)
	}
	if !reflect.DeepEqual(out.Toasts, []Toast{{Text: "back"}}) {
		t.Fatalf("toasts = %+v", out.Toasts)
	}
}

// cmdMsgs runs cmd and every command a batch of it carries, in order, and
// collects what they produce, unbound from the component that issued them.
func cmdMsgs(cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	for _, msg := range deliveries(cmd) {
		if b, ok := msg.(boundMsg); ok {
			msg = b.msg
		}
		out = append(out, msg)
	}
	return out
}

func TestTheStackAnswersForItsTop(t *testing.T) {
	s := testSession()
	root, child := newScripted(s, "root"), newScripted(s, "child")
	root.onKey["o"] = []tea.Msg{openMsg{c: child}}
	st := newStack(s, root)
	st.Update(press("o"))

	if title, sub := st.Heading(); title != "child" || sub != "about child" {
		t.Fatalf("heading = %q, %q", title, sub)
	}
	if got := st.Titles(); !reflect.DeepEqual(got, []string{"root", "child"}) {
		t.Fatalf("titles = %v", got)
	}
	if got := st.Keys(); !reflect.DeepEqual(got, []string{"esc child"}) {
		t.Fatalf("keys = %v", got)
	}
	if _, _, ok := st.Help(); ok {
		t.Fatal("a component with no help reported some")
	}
	if st.Filtering() {
		t.Fatal("a component with no list reported filtering")
	}
	if _, ok := st.Focus(); ok {
		t.Fatal("a component with no rule reported a focus")
	}
}

type modal struct{ *scripted }

func (modal) isDialog() {}

func TestDialogReportsADialogOnTop(t *testing.T) {
	s := testSession()
	root := newScripted(s, "root")
	root.onKey["o"] = []tea.Msg{openMsg{c: modal{newScripted(s, "modal")}}}
	st := newStack(s, root)
	if st.Dialog() {
		t.Fatal("a plain component reads as a dialog")
	}
	st.Update(press("o"))
	if !st.Dialog() {
		t.Fatal("the dialog on top does not read as one")
	}
}

func TestPushOpensAComponentFromOutside(t *testing.T) {
	s := NewSession(context.Background(), &mapping.Document{}, nil)
	st := newStack(s, &scripted{s: s, title: "root"})
	child := &scripted{s: s, title: "child", init: func() tea.Msg { return nil }}
	if cmd := st.push(child); cmd == nil {
		t.Fatal("push dropped the component's Init")
	}
	if st.Depth() != 2 || st.top() != component(child) {
		t.Fatalf("depth = %d, top = %T", st.Depth(), st.top())
	}
	if cmd := st.push(&scripted{s: s}); cmd != nil {
		t.Fatal("push invented an Init")
	}
}

func TestTypingIsTrueOnFormsAndFilters(t *testing.T) {
	spoke := func() *harness { return newHarness(t, identitiesDoc(), nil, openSpoke) }
	// Rows of the rule spoke, in order, and what each opens.
	downTo := func(n int) []string {
		return append(slices.Repeat([]string{"down"}, n), "enter")
	}
	toEditor := func(h *harness, row string) *harness {
		selectEditorRow(t, h, row)
		return h
	}
	for _, tc := range []struct {
		name string
		open func() *harness
		keys []string
		want func(component) bool
	}{
		{"trigger form", spoke, downTo(0), func(c component) bool { _, ok := c.(*triggerForm); return ok }},
		{"var form", spoke, append(downTo(2), "a"), func(c component) bool { _, ok := c.(*varForm); return ok }},
		{"filter form", spoke, append(downTo(4), "a"), func(c component) bool { _, ok := c.(*filterForm); return ok }},
		{"tuple form", spoke, append(downTo(5), "a"), func(c component) bool { _, ok := c.(*tupleForm); return ok }},
		{"iterator form", spoke, append(downTo(3), "enter"), func(c component) bool { _, ok := c.(*iterForm); return ok }},
		{"event paste", func() *harness {
			h := newHarness(t, freshRule(), nil, func(s *Session) component { return newEventPick(s, Focus{}, false) })
			topAs[*eventPick](t, h).list.SelectID(pasteID)
			return h
		}, []string{"enter"}, func(c component) bool { _, ok := c.(*eventPaste); return ok }},
		{"event file", func() *harness {
			h := newHarness(t, freshRule(), nil, func(s *Session) component { return newEventPick(s, Focus{}, false) })
			topAs[*eventPick](t, h).list.SelectID(fileID)
			return h
		}, []string{"enter"}, func(c component) bool { _, ok := c.(*eventFile); return ok }},
		{"test name form", func() *harness {
			return toEditor(newHarness(t, editorDoc(t), nil, openTestEditor), "Name")
		}, []string{"enter"}, func(c component) bool { _, ok := c.(*testNameForm); return ok }},
		{"expect form", func() *harness {
			return newHarness(t, editorDoc(t), nil, openTestEditor)
		}, []string{"a"}, func(c component) bool { _, ok := c.(*expectForm); return ok }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.open()
			if h.st.Typing() {
				t.Fatalf("%T is typing before the form is opened", h.top())
			}
			h.press(tc.keys...)
			if !tc.want(h.top()) {
				t.Fatalf("keys %v opened %T", tc.keys, h.top())
			}
			if !h.st.Typing() {
				t.Fatalf("%T does not report typing", h.top())
			}
		})
	}

	// Pickers and hubs take commands, not text.
	h := newHarness(t, recipeDoc(t, "user.updated"), nil, openSpoke)
	if h.st.Typing() {
		t.Fatal("the spoke is typing")
	}
	h.press("down", "enter")
	if _, ok := h.top().(*actionPick); !ok || h.st.Typing() {
		t.Fatalf("top = %T, typing = %v", h.top(), h.st.Typing())
	}

	h = newHarness(t, threeRules(), nil, openHub)
	if h.st.Typing() {
		t.Fatal("the hub is typing")
	}
	h.press("/")
	if !h.st.Typing() {
		t.Fatal("a filter being set is not typing")
	}
}

// A command's result belongs to the component that issued it. One that comes
// back after that component closed must not reach the one under it: a list's
// late filter matches would replace the other list's rows, and enter or d
// would act on the wrong one. It reaches the issuer while it is on top, batched
// or not.
func TestALateResultReachesOnlyTheComponentThatAskedForIt(t *testing.T) {
	s := testSession()
	root, child := newScripted(s, "root"), newScripted(s, "child")
	root.onKey["o"] = []tea.Msg{openMsg{c: child}}
	child.onKey["x"] = []tea.Msg{doneMsg{}}
	st := newStack(s, root)
	st.Update(press("o"))

	child.ret = tea.Batch(func() tea.Msg { return "first" }, tea.Batch(func() tea.Msg { return "nested" }))
	cmd, _ := st.Resume()
	results := deliveries(cmd)
	for _, m := range results {
		st.Update(m)
	}
	if got := child.got[len(child.got)-2:]; !reflect.DeepEqual(got, []tea.Msg{"first", "nested"}) {
		t.Fatalf("the child got %v, want its own results", got)
	}

	cmd, _ = st.Resume()
	late := deliveries(cmd)
	st.Update(press("x"))
	before := len(root.got)
	for _, m := range late {
		st.Update(m)
	}
	if got := root.got[before:]; len(got) != 0 {
		t.Fatalf("the root got the closed child's results: %v", got)
	}
}

// deliveries runs cmd and every command a batch of it carries, and returns
// the messages the runtime would hand back to Update.
func deliveries(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, deliveries(c)...)
	}
	return out
}
