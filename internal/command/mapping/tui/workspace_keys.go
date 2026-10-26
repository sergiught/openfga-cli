package tui

import (
	tea "charm.land/bubbletea/v2"

	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// globalKeys are the workspace's own keys, as the help lists them. Every other
// key belongs to whatever has focus.
var globalKeys = [][2]string{
	{"1 2 3 4", "show Rules, Check, Tests or Apply"},
	{"tab / shift+tab", "next / previous pane"},
	{"esc", "back; at the top of a pane, the sidebar"},
	{"ctrl+t", "the preview: file or event"},
	{"pgup / pgdown", "page the preview"},
	{"ctrl+s", "save"},
	{"?", "this help"},
	{"q / ctrl+c", "quit; asks first when there are unsaved edits, and stops an apply first"},
}

// key handles a key press. ctrl+c and ctrl+s work anywhere, and the preview's
// keys wherever the preview shows. The single keys (1-4, tab, ?, q) are the
// workspace's only while nothing is taking text: otherwise they are
// characters, and go to the field.
func (w *workspace) key(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()
	switch s {
	case "ctrl+c":
		w.help = false
		return w.quit()
	case "ctrl+s":
		return w.saveKey()
	}
	if w.help {
		w.helpKey(s)
		return nil
	}
	// The preview is not on the stack, so its keys reach it whatever editor is
	// on top, a form included: none of them is text.
	if w.overlay == nil && w.focus == shell.FocusPanel && w.active == paneRules && w.previewShown() {
		switch s {
		case "ctrl+t", "pgup", "pgdown":
			w.preview.Update(k)
			return nil
		}
	}
	typing := w.typing()
	if !typing {
		switch s {
		case "q":
			return w.quit()
		case "?":
			w.help, w.helpOff = true, 0
			return nil
		}
	}
	// An overlay is modal: the panes under it keep still.
	if w.overlay != nil {
		return w.route(overlayPane, k)
	}
	// In load-error mode the panes hold only a message, so e belongs to the
	// workspace on every one of them, the Tests list's jump included.
	if !typing && s == "e" && w.loadErr != nil {
		return w.openInEditor()
	}
	if !typing {
		switch s {
		case "1", "2", "3", "4":
			return w.show(pane(s[0] - '1'))
		case "tab":
			return w.show((w.active + 1) % paneCount)
		case "shift+tab":
			return w.show((w.active + paneCount - 1) % paneCount)
		}
	}
	if w.focus == shell.FocusSidebar {
		return w.sidebarKey(s)
	}
	if !typing && s == "esc" && w.planningShown() {
		w.cancelPlanning()
		return nil
	}
	if !typing && s == "esc" && w.stacks[w.active].Depth() == 1 {
		w.focus = shell.FocusSidebar
		return nil
	}
	if w.paneHidden() {
		return nil
	}
	return w.route(w.active, k)
}

// paneHidden is whether the active pane shows only a message: in load-error
// mode every pane but Check, and Apply while it cannot list events. Its stack
// stays built but gets no keys, or a filter could open on it unseen and
// swallow the workspace's own.
func (w *workspace) paneHidden() bool {
	return w.loadErr != nil && w.active != paneCheck || w.active == paneApply && w.applyBlocked() != ""
}

// planningShown is whether the Apply pane is on top and planning, where esc
// cancels the planning rather than leaving for the sidebar.
func (w *workspace) planningShown() bool {
	return w.active == paneApply && w.pending != nil && w.stacks[paneApply].Depth() == 1
}

// typing reports whether what has the keys takes text. A hidden pane has
// none of them, whatever its stack still holds.
func (w *workspace) typing() bool {
	switch {
	case w.overlay != nil:
		return w.overlay.Typing()
	case w.focus == shell.FocusPanel:
		return !w.paneHidden() && w.stacks[w.active].Typing()
	}
	return false
}

// sidebarKey moves through the panes, showing each as it goes, and hands the
// keys to the one chosen.
func (w *workspace) sidebarKey(s string) tea.Cmd {
	switch s {
	case "up", "k":
		return w.switchTo((w.active + paneCount - 1) % paneCount)
	case "down", "j":
		return w.switchTo((w.active + 1) % paneCount)
	case "enter", "right", "l":
		w.focus = shell.FocusPanel
	}
	return nil
}

// helpKey scrolls the help and closes it. Every other key does nothing, so a
// key pressed to scroll on a short help does not close it.
func (w *workspace) helpKey(s string) {
	switch s {
	case "?", "esc", "q", "enter":
		w.help = false
	case "up", "k":
		w.helpOff = max(w.helpOff-1, 0)
	case "down", "j":
		w.helpOff++ // the view clamps it to what there is
	}
}

// saveKey is ctrl+s, and the hub's s: a save, unless the file cannot take one.
func (w *workspace) saveKey() tea.Cmd {
	switch {
	case w.loadErr != nil:
		return w.toast("fix the file in your editor first (press e)", true)
	case w.file != nil && !w.file.Editable:
		return w.toast("this file is read-only: "+w.file.Reason, true)
	}
	_, cmd := w.save()
	return cmd
}
