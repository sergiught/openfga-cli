package playground

import (
	tea "charm.land/bubbletea/v2"

	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// This file holds the mouse-input handlers (wheel scroll and click hit-testing),
// split out of update.go's message dispatch to keep that file focused.
func (m Model) handleWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	// Don't scroll under a modal/overlay or while editing text.
	if m.helpOpen || m.formErr != "" || m.confirm != nil || m.paletteOpen || m.editorOpen || m.wb.newPromptOpen || m.editing {
		return m, nil
	}
	up := msg.Button == tea.MouseWheelUp
	switch {
	case m.section == secModel:
		if up {
			return m.scrollGraph(-graphLineStep)
		}
		return m.scrollGraph(graphLineStep)
	case m.section == secQuery && m.showRes:
		var cmd tea.Cmd
		m.resVP, cmd = m.resVP.Update(msg)
		return m, cmd
	case m.section == secTestResults:
		if m.wb.running {
			return m, nil
		}
		// The coverage report isn't a cursor list, so the wheel scrolls its
		// capLines window (clamped) instead of moving a selection.
		if m.wb.showCoverage && m.wb.coverage != nil {
			w, h := m.contentSize()
			m.wb.covScroll = clampScroll(m.wb.covScroll, up, renderWorkbenchCoverage(m.wb.coverage, w), h)
			return m, nil
		}
		// With the verbose detail card open, the wheel scrolls it when it's over
		// the detail pane below the tree; over the tree it moves the selection.
		if m.wb.verbose {
			_, h := m.contentSize()
			treeH, detailH := m.wbLayout(h)
			if detailH > 0 {
				_, card := m.wbDetail()
				_, by := m.sh.MainBodyOrigin()
				if msg.Y-by >= treeH+1 {
					m.wb.detailScroll = clampScroll(m.wb.detailScroll, up, card, detailH)
					return m, nil
				}
			}
		}
		// Over the tree: move the selection (the render scrolls to keep it in
		// view), mirroring up/down keys. A selection change resets the detail
		// scroll since the card then shows a different node.
		if up {
			m.wb.treeSel--
		} else {
			m.wb.treeSel++
		}
		m.wb.detailScroll = 0
		m.clampWbTreeSel()
		return m, nil
	case m.section == secAPILogs:
		bx, _ := m.sh.MainBodyOrigin()
		w, _ := m.contentSize()
		if msg.X >= bx+apiLogListWidth(w) {
			// Over the detail pane: scroll the active section (req/resp body).
			if up {
				m.apiLogVP.ScrollUp(apiLogScrollStep)
			} else {
				m.apiLogVP.ScrollDown(apiLogScrollStep)
			}
			return m, nil
		}
		// Over the list: move the selection (reusing the key handler's logic).
		dir := "down"
		if up {
			dir = "up"
		}
		return m.handleSectionKey(dir, keyMsg(dir))
	}
	// List sections: move the selection, which pages the list at its boundaries.
	if lst, _ := m.sectionList(); lst != nil {
		dir := "down"
		if up {
			dir = "up"
		}
		return m, lst.Update(keyMsg(dir))
	}
	return m, nil
}

// handleClick routes a left mouse click: dismiss an info overlay, jump to a
// clicked nav item, invoke a clicked footer keycap, or focus a pane.
func (m Model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return m, nil
	}
	// A dialog is open: clicking outside it behaves like esc (dismiss/cancel);
	// a click inside is consumed since dialogs are driven by their own keys.
	if _, body := m.dialogContent(); body != "" {
		if !m.sh.InDialog(msg.X, msg.Y) {
			return m.handleKey(keyMsg("esc"))
		}
		return m, nil
	}
	// Click a query mode chip to switch modes (works even while the form is
	// focused, since the mode strip sits above it).
	if m.section == secQuery && !m.showRes && m.storeID != "" {
		bx, by := m.sh.MainBodyOrigin()
		if msg.Y == by {
			x := bx
			for i, name := range queryModes {
				w := len(name) + 2 // Padding(0, 1)
				if msg.X >= x && msg.X < x+w {
					m.qmode = i
					m.rebuildQueryForm()
					m.hasResult = false
					return m, nil
				}
				x += w + 1
			}
		}
	}
	// Click an API Logs detail sub-section tab (Req/Resp headers/body) to switch
	// it, mirroring the Tab key.
	if m.section == secAPILogs {
		if i, ok := m.apiLogTabAt(msg.X, msg.Y); ok {
			m.apiLogTab = i
			m.refreshAPILogVP()
			m.focus = shell.FocusPanel
			return m, nil
		}
	}
	// Inline query editing (not a dialog) ignores other stray clicks.
	if m.editing {
		return m, nil
	}
	if idx := m.sh.NavHit(msg.X, msg.Y); idx >= 0 {
		m.focus = shell.FocusSidebar
		return m.gotoSection(section(idx))
	}
	// A footer keycap acts as a button: synthesize its key press.
	if hint := m.sh.KeyHit(msg.X, msg.Y); hint != "" {
		if tok := footerKeyToken(hint); tok != "" {
			return m.handleKey(keyMsg(tok))
		}
		return m, nil
	}
	// Click a list row to select it.
	if lst, off := m.sectionList(); lst != nil {
		bx, by := m.sh.MainBodyOrigin()
		w, h := m.contentSize()
		if msg.X >= bx && msg.X < bx+w && msg.Y >= by+off && msg.Y < by+h {
			if idx := lst.IndexAt(msg.Y - by - off); idx >= 0 {
				lst.SelectIndex(idx)
				m.focus = shell.FocusPanel
				return m, nil
			}
		}
	}
	// Click a Tests navigator row (file or nested test) to select it, the same
	// row a click-through would land on via up/k, down/j — the tree isn't a
	// list.List (sectionList doesn't cover it), and clicks are ignored while
	// coverage/spinner occupy the pane since neither renders selectable rows.
	if m.section == secTestResults && !m.wb.showCoverage && !m.wb.running {
		bx, by := m.sh.MainBodyOrigin()
		w, h := m.contentSize()
		if msg.X >= bx && msg.X < bx+w && msg.Y >= by && msg.Y < by+h {
			if idx, ok := m.wbNodeAt(msg.Y - by); ok {
				m.wb.treeSel = idx
				m.wb.detailScroll = 0
				m.clampWbTreeSel()
				m.focus = shell.FocusPanel
				return m, nil
			}
		}
	}
	if m.sh.InSidebar(msg.X) {
		m.focus = shell.FocusSidebar
	} else {
		m.focus = shell.FocusPanel
	}
	return m, nil
}
