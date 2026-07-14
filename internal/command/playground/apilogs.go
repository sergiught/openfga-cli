package playground

import (
	"charm.land/bubbles/v2/viewport"

	"github.com/sergiught/openfga-cli/internal/apilog"
)

// apiLogHistory caps how many requests the API Logs view retains per session.
const apiLogHistory = 200

// apiLogMsg is sent (via Recorder.SetNotify -> program.Send) whenever a new
// request is captured, so the API Logs view re-renders.
type apiLogMsg struct{}

// TODO(task5): apiLogDetail is a temporary stub until Task 5 implements the
// real detail rendering (request/response formatting, pretty-printing).
func apiLogDetail(e apilog.Entry, pretty bool) string { return "" }

// refreshAPILogVP rebuilds the detail viewport for the current selection. It
// lazily creates the viewport and clamps the selection into range.
func (m *Model) refreshAPILogVP() {
	w, h := m.contentSize()
	cw := w - splitListWidth(w) - 2
	if cw < 1 {
		cw = 1
	}
	vh := h - 2
	if vh < 1 {
		vh = 1
	}
	if !m.apiLogVPInit {
		m.apiLogVP = viewport.New(viewport.WithWidth(cw), viewport.WithHeight(vh))
		m.apiLogVPInit = true
	} else {
		m.apiLogVP.SetWidth(cw)
		m.apiLogVP.SetHeight(vh)
	}
	// m.recorder is nil in tests that build a Model directly via newModel
	// instead of through Run (which always wires one up); treat that the same
	// as an empty history rather than crashing.
	if m.recorder == nil {
		m.apiLogVP.SetContent("")
		return
	}
	entries := m.recorder.Snapshot()
	if len(entries) == 0 {
		m.apiLogVP.SetContent("")
		return
	}
	if m.apiLogSel < 0 {
		m.apiLogSel = 0
	}
	if m.apiLogSel > len(entries)-1 {
		m.apiLogSel = len(entries) - 1
	}
	e := entries[len(entries)-1-m.apiLogSel] // newest-first index
	m.apiLogVP.SetContent(apiLogDetail(e, m.apiLogPretty))
	m.apiLogVP.GotoTop()
}
