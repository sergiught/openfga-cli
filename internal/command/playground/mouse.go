package playground

import (
	tea "charm.land/bubbletea/v2"

	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// This file holds the mouse-input handlers (wheel scroll and click hit-testing),
// split out of update.go's message dispatch to keep that file focused.
func (m Model) handleWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	// Don't scroll under a modal/overlay or while editing text.
	if m.helpOpen || m.formErr != "" || m.confirm != nil || m.paletteOpen || m.editorOpen || m.editing {
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
	if m.sh.InSidebar(msg.X) {
		m.focus = shell.FocusSidebar
	} else {
		m.focus = shell.FocusPanel
	}
	return m, nil
}
