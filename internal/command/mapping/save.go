package mapping

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

// canSave reports whether the save key has anything to do from here. It gates
// both the key and the hint that advertises it, so the two cannot disagree.
//
// An empty document is excluded because the dialog it opens says only that
// there is nothing to save — a key whose entire effect is that rebuke is worse
// than one that is absent. The three modal screens are excluded because they
// already own the keyboard: stacking a save dialog on a delete confirmation
// answers a question the user was asked but has not answered yet.
func (m *wizardModel) canSave() bool {
	switch m.top() {
	case screenConfirmSave, screenConfirmDelete, screenHelp:
		return false
	}
	return len(m.doc.Rules) > 0
}

func (m *wizardModel) keyConfirmSave(k tea.KeyPressMsg) tea.Cmd {
	if len(m.doc.Rules) == 0 {
		// Nothing to write: the only useful action is going back.
		m.pop()
		return nil
	}
	blocking := m.saveProblems()
	switch k.String() {
	case "esc", "b":
		m.pop()
		return nil
	case "q":
		m.cancelled = true
		return tea.Quit
	case "s":
		return m.finish()
	case "enter", " ":
		// enter is Save only when there is nothing to warn about; with problems
		// on screen it would be too easy to confirm a file you did not read.
		if len(blocking) == 0 {
			return m.finish()
		}
		return nil
	}
	return nil
}

// finish renders the final document, appends the generated tests and hands the
// bytes back to the command, which owns the actual write.
//
// Tests are generated last, from the finished document, so they assert what the
// file really does — including rules the user edited after picking their sample.
func (m *wizardModel) finish() tea.Cmd {
	doc := m.doc
	doc.Tests = mapping.BuildTests(m.ctx, &doc)

	data, err := mapping.Marshal(&doc)
	if err != nil {
		m.errMsg = fmt.Sprintf("could not render the mapping: %v", err)
		return nil
	}

	m.result = &wizardResult{
		data:   data,
		rules:  len(doc.Rules),
		tuples: countTuples(doc.Rules),
		tests:  len(doc.Tests),
	}
	// A user who loaded no model leaves with a file naming types and relations
	// that nothing says exist, and finds out which ones are missing one rejected
	// write at a time. The catalog's own model is the one already written to
	// agree with the recipes, so it goes out beside them — to edit, not to adopt:
	// its second half is an example resource, marked as the part to replace.
	if m.index.Empty() {
		m.result.model = []byte(auth0.Model())
	}
	m.done = true
	return tea.Quit
}

func countTuples(rules []mapping.Rule) int {
	n := 0
	for _, r := range rules {
		n += len(r.Tuples)
		if r.Iterator != nil {
			n += len(r.Iterator.Tuples)
		}
	}
	return n
}

// saveProblems is everything standing between the document and the file: Lint's
// blocking problems plus mapper's diagnostics. Lint checks the structure the
// wizard lets you leave half-finished and never parses an expression, so it
// cannot answer the question the dialog puts to the user — whether the mapping
// compiles. Only mapper can, and refresh has already asked it.
func (m *wizardModel) saveProblems() []mapping.Problem {
	return append(mapping.Blocking(m.problems), m.preview.Problems()...)
}

// saveSummary is the dialog's body: what is about to be written, and what is
// wrong with it.
func (m *wizardModel) saveSummary() string {
	if len(m.doc.Rules) == 0 {
		return "There are no rules yet, so there is nothing to save.\n\nPress esc to go back and add one."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Write %s to %s.\n", plural(len(m.doc.Rules), "rule"), m.path)
	fmt.Fprintf(&b, "%s, %s.\n",
		plural(countTuples(m.doc.Rules), "tuple"), plural(m.sampledRules(), "sampled rule"))
	if m.index.Empty() {
		fmt.Fprintf(&b, "No model was loaded, so a starting %s goes beside it.\n", startingModelFile)
	}

	if blocking := m.saveProblems(); len(blocking) > 0 {
		fmt.Fprintf(&b, "\nMapping has %s. Save anyway to fix by hand?\n\n", plural(len(blocking), "error"))
		b.WriteString(m.problemLines(blocking))
		return b.String()
	}

	// Compiling is all mapper can vouch for. A tuple naming a type or relation
	// the loaded model does not have compiles perfectly and is still refused by
	// the store, and this dialog is the last screen before the file is written —
	// so the sentence that ends it cannot be an unqualified all-clear while the
	// rule screen behind it flags exactly that.
	warnings := mapping.Warnings(m.problems)
	if len(warnings) == 0 {
		b.WriteString("\nThe mapping compiles.")
		return b.String()
	}
	b.WriteString("\nThe mapping compiles, but the authorization model does not have\neverything it names:\n\n")
	b.WriteString(m.problemLines(warnings))
	b.WriteString("\nThe store will reject those writes until the model catches up.\nSave anyway if the model is the thing due to change.")
	return b.String()
}

// problemLines renders up to five problems, each against the rule it belongs to
// and marked with its own severity.
func (m *wizardModel) problemLines(ps []mapping.Problem) string {
	var b strings.Builder
	for i, p := range ps {
		if i == 5 {
			fmt.Fprintf(&b, "  … and %d more\n", len(ps)-5)
			break
		}
		name := "document"
		if p.Rule >= 0 && p.Rule < len(m.doc.Rules) {
			name = m.doc.Rules[p.Rule].Name
			if name == "" {
				name = fmt.Sprintf("rule %d", p.Rule+1)
			}
		}
		fmt.Fprintf(&b, "  %s %s: %s\n", problemMark(p), name, p.Message)
	}
	return b.String()
}

// sampledRules counts the rules that will produce an embedded test.
func (m *wizardModel) sampledRules() int {
	n := 0
	for _, r := range m.doc.Rules {
		if r.Sample != nil {
			n++
		}
	}
	return n
}
