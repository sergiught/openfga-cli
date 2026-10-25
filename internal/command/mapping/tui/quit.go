package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// quit leaves, asking Save / Discard / Cancel first when there are unsaved
// edits. An apply running is stopped instead, between requests: the
// next quit, once the stop is reported, leaves. A plan being made is dropped
// when the workspace leaves, and kept when the quit is cancelled.
func (w *workspace) quit() tea.Cmd {
	if w.applying {
		return w.stopApply()
	}
	leave := func() tea.Cmd {
		w.cancelPlanning()
		w.cancelModelFetch()
		return tea.Quit
	}
	// The flag can lag the last keystroke by a debounce: an edit undone within
	// it is no reason to ask.
	if w.dirty {
		w.dirty = w.unsaved()
	}
	if !w.dirty {
		return leave()
	}
	return w.openOverlay(newStack(w.sess, newQuitPrompt(w.sess, w.path)), func(result any) tea.Cmd {
		switch result {
		case quitSave:
			ok, cmd := w.saveThen(leave)
			if !ok {
				return cmd // the save says why, or opened a dialog that goes on from there
			}
			return tea.Batch(cmd, leave())
		case quitDiscard:
			return leave()
		}
		return nil
	})
}

// quitAnswer is what the quit prompt closes with.
type quitAnswer int

const (
	quitSave quitAnswer = iota + 1
	quitDiscard
)

// quitPrompt asks Save / Discard / Cancel: s, d, esc. enter does nothing.
// It closes with quitSave, quitDiscard, or doneMsg{} (cancel).
type quitPrompt struct {
	s    *Session
	path string
}

func newQuitPrompt(s *Session, path string) *quitPrompt { return &quitPrompt{s: s, path: path} }

func (c *quitPrompt) Update(msg tea.Msg) (component, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	switch k.String() {
	case "s":
		c.s.post(doneMsg{result: quitSave})
	case "d":
		c.s.post(doneMsg{result: quitDiscard})
	case "esc":
		c.s.post(doneMsg{})
	}
	// enter is deliberately none of them: there is no answer it could safely
	// default to, and one of them throws the edits away.
	return c, nil
}

func (c *quitPrompt) View(width, height int) string {
	// The path's tail, so it never breaks across lines.
	return wrapped(pathTail(style.SanitizeTerminal(c.path), width)+
		" has unsaved edits. Save them before quitting?", width, height)
}

func (c *quitPrompt) Keys() []string {
	return []string{"s save", "d discard", "esc cancel"}
}

func (c *quitPrompt) heading() (string, string) { return "Unsaved edits", "" }

func (c *quitPrompt) isDialog() {}

// confirm is a y/N dialog: only y confirms; n, esc and enter cancel. It
// closes with true or false.
type confirm struct {
	s                     *Session
	title, question, verb string
}

func newConfirm(s *Session, title, question, verb string) *confirm {
	return &confirm{s: s, title: title, question: question, verb: verb}
}

func (c *confirm) Update(msg tea.Msg) (component, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	switch k.String() {
	case "y":
		c.s.post(doneMsg{result: true})
	case "n", "esc", "enter":
		// enter cancels: it is every other screen's "go on", and here going on
		// out of habit would destroy something.
		c.s.post(doneMsg{result: false})
	}
	return c, nil
}

func (c *confirm) View(width, height int) string { return wrapped(c.question, width, height) }

func (c *confirm) Keys() []string { return []string{"y " + c.verb, "n / esc / enter cancel"} }

func (c *confirm) heading() (string, string) { return c.title, "" }

func (c *confirm) isDialog() {}

// wrapped is text wrapped to width and windowed to height.
func wrapped(text string, width, height int) string {
	return windowAround(strings.Split(lipgloss.NewStyle().Width(width).Render(text), "\n"), 0, height)
}
