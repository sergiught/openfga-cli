package tui

import tea "charm.land/bubbletea/v2"

// Stack is a host's handle on a pile of components: the top one has the
// keyboard, and the ones under it are where closing goes back to.
type Stack struct {
	s     *Session
	items []component
}

// Toast is a line a component asked the host to show.
type Toast struct {
	Text string
	Err  bool
}

// Outcome is what one Update settled that the host has to act on.
type Outcome struct {
	// Changed reports that a component edited the document.
	Changed bool
	// Toasts are the lines components asked for, in order.
	Toasts []Toast
	// Requests are what components asked of the host, in order, that leave the
	// stack as it is.
	Requests []any
	// Closed reports that the root asked to close. The stack never pops its
	// root: whoever opened the stack decides what closing means.
	Closed bool
	// Result is what the root closed with.
	Result any
}

func newStack(s *Session, root component) *Stack {
	return &Stack{s: s, items: []component{root}}
}

// Update hands msg to the top component, then settles everything posted while
// it ran — including what handling those posts posts in turn — before
// returning, so the next key always reaches the component now on top.
func (st *Stack) Update(msg tea.Msg) (tea.Cmd, Outcome) {
	cmd := st.send(msg)
	var out Outcome
	// settle fills out, so it runs before out is read: a return statement
	// leaves the order between the two unspecified.
	settled := st.settle(&out)
	return tea.Batch(cmd, settled), out
}

// Resume tells the top component it is on top again after the host showed
// something over the whole stack, so it re-reads the document. It settles and
// returns like Update.
func (st *Stack) Resume() (tea.Cmd, Outcome) {
	var out Outcome
	cmd := st.send(closedMsg{})
	settled := st.settle(&out)
	return tea.Batch(cmd, settled), out
}

// Init starts the root component's work, such as focusing a text area. A root
// may open its first child here (see ruleFork), so what it posted is settled
// too. A root's Init may only open children: anything else it posts (a change,
// a toast, a request) is dropped with the Outcome.
func (st *Stack) Init() tea.Cmd {
	var cmd tea.Cmd
	if c, ok := st.items[0].(initer); ok {
		cmd = c.Init()
	}
	var out Outcome
	return tea.Batch(cmd, st.settle(&out))
}

func (st *Stack) send(msg tea.Msg) tea.Cmd {
	top := len(st.items) - 1
	c, cmd := st.items[top].Update(msg)
	st.items[top] = c
	return cmd
}

func (st *Stack) settle(out *Outcome) tea.Cmd {
	var cmds []tea.Cmd
	for len(st.s.posted) > 0 {
		msg := st.s.posted[0]
		st.s.posted = st.s.posted[1:]
		switch m := msg.(type) {
		case docChangedMsg:
			out.Changed = true
		case toastMsg:
			out.Toasts = append(out.Toasts, Toast{Text: m.text, Err: m.err})
		case requestMsg:
			out.Requests = append(out.Requests, m.what)
		case openMsg:
			cmds = append(cmds, st.push(m.c))
		case doneMsg:
			if len(st.items) == 1 {
				out.Closed, out.Result = true, m.result
				continue
			}
			st.items = st.items[:len(st.items)-1]
			cmds = append(cmds, st.send(closedMsg(m)))
		}
	}
	return tea.Batch(cmds...)
}

// push opens c on top of the stack from outside, as an openMsg posted from
// inside would, and returns c's Init when it has one.
func (st *Stack) push(c component) tea.Cmd {
	st.items = append(st.items, c)
	if i, ok := c.(initer); ok {
		return i.Init()
	}
	return nil
}

func (st *Stack) top() component { return st.items[len(st.items)-1] }

// Depth is how many components are open.
func (st *Stack) Depth() int { return len(st.items) }

// View renders the top component.
func (st *Stack) View(width, height int) string { return st.top().View(width, height) }

// Keys is the top component's hints.
func (st *Stack) Keys() []string { return st.top().Keys() }

// Heading is the top component's title and subtitle.
func (st *Stack) Heading() (title, subtitle string) {
	if h, ok := st.top().(headed); ok {
		return h.heading()
	}
	return "", ""
}

// Titles names every open component, bottom first, for a breadcrumb.
func (st *Stack) Titles() []string {
	out := make([]string, 0, len(st.items))
	for _, c := range st.items {
		if h, ok := c.(headed); ok {
			// A component with no title of its own, like the fork under its
			// children, is not a hop.
			if title, _ := h.heading(); title != "" {
				out = append(out, title)
			}
		}
	}
	return out
}

// Help is the top component's ? overlay, when it has one.
func (st *Stack) Help() (title, body string, ok bool) {
	if h, ok := st.top().(helped); ok {
		title, body = h.help()
		return title, body, true
	}
	return "", "", false
}

// Filtering reports whether the top component's list filter is taking input.
func (st *Stack) Filtering() bool {
	f, ok := st.top().(filterable)
	return ok && f.filtering()
}

// Typing reports whether the top component takes text: a form, a paste box, a
// typed path, or a list whose filter is being set. The host's single-key
// bindings must reach it instead.
func (st *Stack) Typing() bool {
	if t, ok := st.top().(typer); ok && t.typing() {
		return true
	}
	return st.Filtering()
}

// Dialog reports whether the top component is a dialog.
func (st *Stack) Dialog() bool {
	_, ok := st.top().(dialog)
	return ok
}

// Focus is the rule the top component edits, when it edits one.
func (st *Stack) Focus() (Focus, bool) {
	if f, ok := st.top().(focused); ok {
		return f.focus(), true
	}
	return Focus{}, false
}

// holdsNewRule says the stack has an add-rule fork with input in it: an event
// picked, or text in the paste box above the fork. Dropping it would lose
// that. A paste box with no fork below it is ctrl+e on a rule that exists.
func (st *Stack) holdsNewRule() bool {
	fork := false
	for _, c := range st.items {
		switch c := c.(type) {
		case *ruleFork:
			if c.inRecipe {
				return true
			}
			fork = true
		case *eventPaste:
			if fork && c.area.Value() != "" {
				return true
			}
		}
	}
	return false
}

// holdsPaste says some paste box in the stack has text in it. Dropping the
// stack would lose what was typed, whether or not a rule is being added.
func (st *Stack) holdsPaste() bool {
	for _, c := range st.items {
		if p, ok := c.(*eventPaste); ok && p.area.Value() != "" {
			return true
		}
	}
	return false
}
