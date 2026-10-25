// Package tui holds the `ofga mappings` editors as components. Each one edits
// a *mapping.Document in place and talks to whatever hosts it only through the
// messages below. The workspace hosts them.
package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// component is one editor. Components are pointer types, so View may record
// what it measured (a list's size, a scroll ceiling) for the next Update.
// height is the row budget for the component's own body; the host draws the
// heading above it.
type component interface {
	Update(tea.Msg) (component, tea.Cmd)
	View(width, height int) string
	Keys() []string // "key label", the shape shell.Status.Keys takes
}

// Optional capabilities. The Stack finds them by type assertion.
type (
	// headed components name themselves for the heading and the breadcrumb.
	headed interface {
		heading() (title, subtitle string)
	}
	// helped components have a concept the ? overlay explains.
	helped interface{ help() (title, body string) }
	// filterable components host a list whose filter box may be taking input,
	// where ? and single-letter keys are text, not commands.
	filterable interface{ filtering() bool }
	// typer components are taking text, so the host's single-key bindings
	// (1-4, tab, q, ?) must reach them instead.
	typer interface{ typing() bool }
	// focused components edit one rule, and the preview follows it.
	focused interface{ focus() Focus }
	// initer components have work to start when pushed, such as focusing a
	// text area.
	initer interface{ Init() tea.Cmd }
	// dialog components are modal questions a host draws as a card, with no
	// preview beside them.
	dialog interface{ isDialog() }
)

// Focus is what the preview follows: the rule a component edits, and whether
// it edits inside that rule's iterator, where expressions address the item.
type Focus struct {
	Rule   int
	InIter bool
}

// The messages a component posts for its host.
type (
	// docChangedMsg says the component edited the document.
	docChangedMsg struct{}
	// doneMsg asks to close the component that posted it. result goes to the
	// component underneath as a closedMsg, or to the host when there is none.
	doneMsg struct{ result any }
	// openMsg pushes c on top of the component that posted it.
	openMsg struct{ c component }
	// toastMsg is a transient line for the host to show: an error, or a note.
	toastMsg struct {
		text string
		err  bool
	}
	// requestMsg asks the host to do something that leaves the component
	// where it is, such as showing the model source over it. what is one of
	// the host's request values (see RulesRequest).
	requestMsg struct{ what any }
)

// closedMsg is what the stack hands the component a child closed onto: the
// child's result, or nil. It is also how a component learns it is on top again
// after the host showed something over it (Stack.Resume), so every component
// re-reads the document on it.
type closedMsg struct{ result any }

// Jump asks the host to show rule Rule in its Rules pane with the editor
// Section open ("" opens the rule itself). The Tests pane's e and the Check
// rows close with it. Section is one of "trigger", "action", "variables",
// "iterator", "filters", "tuple" or "".
type Jump struct {
	Rule    int
	Section string
}

// RuleAdded is what a rule fork closes with: the index of the first rule it
// appended (a recipe can append several).
type RuleAdded struct{ Index int }

// Session is what every component on a stack shares: the document they edit in
// place, the model they check against, the context evaluation runs under, and
// the messages posted that the stack has not settled yet.
type Session struct {
	Ctx   context.Context
	Doc   *mapping.Document
	Index *mapping.ModelIndex
	// ReadOnly makes the components refuse the keys that edit the document and
	// drop them from Keys.
	ReadOnly bool

	posted []tea.Msg
}

// NewSession starts the state the components on one host share.
func NewSession(ctx context.Context, doc *mapping.Document, ix *mapping.ModelIndex) *Session {
	return &Session{Ctx: ctx, Doc: doc, Index: ix}
}

// post queues msg for the stack, which settles it before its Update returns.
//
// Posting is synchronous on purpose. A navigation message returned as a
// tea.Cmd would come back through the runtime after any key already queued,
// so esc then d on a form could type the d into the form esc was closing.
func (s *Session) post(msg tea.Msg) { s.posted = append(s.posted, msg) }

// rule returns rule i, or nil when i is out of range — which happens
// transiently after a delete.
func (s *Session) rule(i int) *mapping.Rule {
	if i < 0 || i >= len(s.Doc.Rules) {
		return nil
	}
	return &s.Doc.Rules[i]
}
