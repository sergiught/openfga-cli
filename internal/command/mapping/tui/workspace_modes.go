package tui

import (
	"os/exec"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/extedit"
	shell "github.com/sergiught/openfga-cli/internal/ui/shell"
)

// mustLoad is what Rules and Tests say while the file does not load.
const mustLoad = "The file must load before it can be edited: fix it in your editor (e)."

// editorDoneMsg is $EDITOR exiting.
type editorDoneMsg struct{ err error }

// enterLoadError shows a file whose YAML did not parse: its findings, on
// Check, with nothing to edit or save. reload leaves the mode again.
func (w *workspace) enterLoadError(fs []mapping.Finding) {
	w.loadErr = fs
	w.sess.ReadOnly = true
	w.startTestsRun() // clears the badge: nothing loaded to test
	w.runCheck()
	w.active, w.focus = paneCheck, shell.FocusPanel
}

// readOnlyBanner is the line every pane starts with when the file is read-only
// ("" otherwise). The reason comes from the file, so it is sanitized.
func (w *workspace) readOnlyBanner(width int) string {
	if w.loadErr != nil || w.file == nil || w.file.Editable {
		return ""
	}
	return style.Warn.Width(width).Render("Read-only: " + style.SanitizeTerminal(w.file.Reason))
}

// openInEditor runs $EDITOR on the file. The workspace issues the command
// itself: tagged would swallow an exec.
func (w *workspace) openInEditor() tea.Cmd {
	name, args := extedit.Command()
	if name == "" {
		return w.toast("no editor found: set $EDITOR", true)
	}
	return tea.ExecProcess(exec.Command(name, append(args, w.path)...), func(err error) tea.Msg {
		return editorDoneMsg{err: err}
	})
}

// editorDone reloads the file the editor may have fixed. reload leaves
// load-error mode on success, or refreshes the findings when still broken.
func (w *workspace) editorDone(msg editorDoneMsg) tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		cmds = append(cmds, w.toast("editor failed: "+style.SanitizeTerminal(msg.err.Error()), true))
	}
	return tea.Batch(append(cmds, w.reload())...)
}
