package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// testsRunMsg is a badge run finishing.
type testsRunMsg struct {
	gen int
	res TestResults
}

// startTestsRun runs the Tests badge on the document as it is now.
func (w *workspace) startTestsRun() tea.Cmd { return w.startTestsRunFrom(w.render().src) }

// startTestsRunFrom runs the Tests badge in the background on src, the bytes a
// save would write, replacing any run in flight. The cmd gets bytes and
// nothing else: it must never see the live document or what shares memory
// with it (rule samples are maps), because the UI keeps editing while it
// evaluates. A nil src, which a read-only file renders to, is marshalled here
// from the document, on the goroutine that owns it.
func (w *workspace) startTestsRunFrom(src []byte) tea.Cmd {
	w.testsGen++
	if w.testsCancel != nil {
		w.testsCancel()
		w.testsCancel = nil
	}
	if w.loadErr != nil {
		w.tests = TestResults{}
		return nil
	}
	if src == nil {
		var err error
		if src, err = mapping.Marshal(w.savedDoc()); err != nil {
			w.tests = TestResults{}
			return nil
		}
	}
	ctx, cancel := context.WithCancel(w.ctx)
	w.testsCancel = cancel
	w.busy++
	gen := w.testsGen
	run := func() tea.Msg { return testsRunMsg{gen: gen, res: runSource(ctx, src)} }
	return tea.Batch(w.spin.Tick, run)
}

// testsArrived takes a finished badge run unless a later one replaced it.
func (w *workspace) testsArrived(msg testsRunMsg) tea.Cmd {
	w.busy--
	if msg.gen != w.testsGen {
		return nil
	}
	w.testsCancel()
	w.testsCancel = nil
	w.tests = msg.res
	// A draft that did not compile had no samples recovered at open. Once it
	// does, once, since this fills only the rules that have none.
	if !msg.res.Compiled {
		w.needSamples = true
	} else if w.needSamples {
		w.needSamples = false
		mapping.SamplesFromTests(w.ctx, &w.doc)
		w.preview.Refresh(w.focusRule())
		// An open rule hub shows the samples just restored.
		cmd, out := w.stacks[paneRules].Resume()
		return tea.Batch(w.tag(paneRules, cmd), w.settle(paneRules, out))
	}
	return nil
}
