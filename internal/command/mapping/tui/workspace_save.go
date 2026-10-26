package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
	"github.com/sergiught/openfga-cli/internal/style"
)

// save writes the file and reports success; on a conflict it opens the
// conflict dialog and returns false. Problems never block it: work in
// progress is worth keeping.
//
// It always writes savedDoc(), and never adopts what Save hands back as the
// working document: that one carries the generated tests, which would become
// the user's own and be generated again on the next save.
func (w *workspace) save() (bool, tea.Cmd) { return w.saveThen(nil) }

// saveFile is File.Save, a variable so a test can make the layout fail: no real
// input is known that does.
var saveFile = (*mapping.File).Save

// saveThen is save, for a caller that goes on when the save is made later: a
// document whose layout cannot be kept is saved only after a confirm, and then
// is the command that follows it (nil for none).
func (w *workspace) saveThen(then func() tea.Cmd) (bool, tea.Cmd) {
	if w.applying {
		return false, w.toast(applyLocksFile, true)
	}
	doc := w.savedDoc()
	wasFresh := w.fresh
	var (
		f     *mapping.File
		wrote = true
		err   error
	)
	if wasFresh {
		f, err = mapping.Create(w.path, doc)
	} else {
		f = w.file
		wrote, err = saveFile(f, doc)
	}
	switch {
	case wasFresh && errors.Is(err, fs.ErrExist):
		return false, w.openConflict(true)
	case errors.Is(err, mapping.ErrChangedOnDisk):
		return false, w.openConflict(false)
	case errors.Is(err, mapping.ErrLayout):
		return false, w.askReformatted(then)
	case err != nil:
		return false, w.toast("could not save "+w.path+": "+err.Error(), true)
	}
	if !wrote {
		// Nothing to write is only true if the file is still there to be
		// unchanged: one deleted or replaced outside is the last copy at risk.
		if gone, err := f.ChangedOnDisk(); err != nil {
			return false, w.toast("could not save "+w.path+": "+err.Error(), true)
		} else if gone {
			return false, w.openConflict(false)
		}
		w.file, w.fresh, w.dirty = f, false, false
		return true, w.toast("nothing to save", false)
	}
	w.file, w.fresh, w.dirty = f, false, false
	return true, w.wrote(doc, wasFresh)
}

// askReformatted offers the save a layout-losing document can still have. Only
// y saves; anything else leaves the document dirty.
func (w *workspace) askReformatted(then func() tea.Cmd) tea.Cmd {
	q := "Could not keep the file's layout (" + mapping.ErrLayout.Error() + "). Save it reformatted? Comments and key order are lost."
	return w.openOverlay(newStack(w.sess, newConfirm(w.sess, "Save reformatted", q, "save reformatted")),
		func(result any) tea.Cmd {
			if result != true {
				return nil
			}
			return w.saveReformatted(then)
		})
}

// saveReformatted is save writing Marshal's layout. It follows the rules of
// save: not while an apply runs, the changed-on-disk check, and wrote().
func (w *workspace) saveReformatted(then func() tea.Cmd) tea.Cmd {
	if w.applying {
		return w.toast(applyLocksFile, true)
	}
	doc := w.savedDoc()
	f := w.file
	_, err := f.SaveReformatted(doc)
	switch {
	case errors.Is(err, mapping.ErrChangedOnDisk):
		return w.openConflict(false)
	case err != nil:
		return w.toast("could not save "+w.path+": "+err.Error(), true)
	}
	w.file, w.fresh, w.dirty = f, false, false
	cmd := w.wrote(doc, false)
	if then != nil {
		return tea.Batch(cmd, then())
	}
	return cmd
}

// shownPath is the file's path as the start screen shows it, from the working
// folder or ~ when it can be: the absolute one wraps a toast to several lines.
func (w *workspace) shownPath() string {
	dir := w.opts.Dir
	if dir == "" {
		dir = "."
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	home, _ := os.UserHomeDir()
	return recentPath(w.path, dir, home)
}

// wrote is what follows a write: the Apply pane's plan dropped and its events,
// the recent list, the starting model of an Auth0-built file, and the toasts.
func (w *workspace) wrote(doc *mapping.Document, first bool) tea.Cmd {
	cmds := []tea.Cmd{
		w.dropPlan("the file was saved"),
		w.syncApply(),
		w.toast(fmt.Sprintf("saved %s (%s, %s)", w.shownPath(),
			mapping.Plural(len(doc.Rules), "rule"), mapping.Plural(len(doc.Tests), "test")), false),
	}
	// A list that cannot be written must not make the save look failed.
	if err := rememberRecent(w.opts.RecentFile, w.path); err != nil {
		cmds = append(cmds, w.toast("could not update the recent files: "+err.Error(), true))
	}
	if first && w.auth0 {
		cmds = append(cmds, w.writeStartingModel())
	}
	return tea.Batch(cmds...)
}

// writeStartingModel puts the Auth0 catalog's model beside the mapping so the
// user has something to edit and push, unless a model is already there. When
// the session has no model it checks against this one from now on.
func (w *workspace) writeStartingModel() tea.Cmd {
	dir := filepath.Dir(w.path)
	for _, name := range []string{"model.fga", "model.json"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return nil
		}
	}
	data := []byte(auth0.Model())
	// O_EXCL, so a model created since the check above is not overwritten.
	target := filepath.Join(dir, "model.fga")
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // a model is shared like the mapping beside it, not private
	if err == nil {
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			// Ours, and partial: left there it would block every later first save.
			_ = os.Remove(target)
		}
	}
	if err != nil {
		return w.toast("could not write model.fga beside it: "+err.Error(), true)
	}
	cmds := []tea.Cmd{w.toast("wrote model.fga beside it: a starting model to edit, then push it with 'ofga model write'", false)}
	// A store fetch in flight is a choice the user made; the file must not cancel it.
	if w.sess.Index.Empty() && w.model.cancel == nil {
		cmds = append(cmds, w.useModel(modelChoice{kind: modelFile, path: target}))
	}
	return tea.Batch(cmds...)
}

// saveAnswer is what the conflict dialog closes with.
type saveAnswer int

const (
	answerReload saveAnswer = iota + 1
	answerOverwrite
)

// openConflict asks what to do about a file that is not as it was left.
func (w *workspace) openConflict(appeared bool) tea.Cmd {
	return w.openOverlay(newStack(w.sess, newSaveConflict(w.sess, w.path, appeared)), func(result any) tea.Cmd {
		switch result {
		case answerReload:
			return w.openOverlay(newStack(w.sess, newConfirm(w.sess, "Reload",
				"Discard your unsaved edits and load the file from disk?", "reload")),
				func(result any) tea.Cmd {
					if result == true {
						return w.reload()
					}
					return nil
				})
		case answerOverwrite:
			return w.openOverlay(newStack(w.sess, newConfirm(w.sess, "Save over",
				"Replace the file on disk with your version?", "save over it")),
				func(result any) tea.Cmd {
					if result == true {
						return w.overwrite()
					}
					return nil
				})
		}
		return nil
	})
}

// overwrite saves over whatever is on disk, or nothing.
func (w *workspace) overwrite() tea.Cmd {
	if w.applying {
		return w.toast(applyLocksFile, true)
	}
	doc := w.savedDoc()
	wasFresh := w.fresh
	f := w.file
	var err error
	if f == nil {
		// Nothing was ever loaded, so there is no File to overwrite through.
		var src []byte
		if src, err = mapping.Marshal(doc); err == nil {
			f, err = mapping.LoadDraftBytes(w.path, src)
		}
	}
	if err == nil {
		_, err = f.Overwrite(doc)
	}
	if err != nil {
		return w.toast("could not save "+w.path+": "+err.Error(), true)
	}
	w.file, w.fresh, w.dirty = f, false, false
	return w.wrote(doc, wasFresh)
}

// reload replaces the working document with the file on disk.
func (w *workspace) reload() tea.Cmd {
	if w.applying {
		return w.toast(applyLocksFile, true)
	}
	f, err := mapping.LoadDraft(w.path)
	var le *mapping.LoadError
	if errors.As(err, &le) {
		findings := mapping.FindingsFrom(le.Diagnostics)
		if len(findings) == 0 {
			findings = []mapping.Finding{{Rule: -1, Message: le.Error()}}
		}
		dropped := w.dropPlan("the file was reloaded")
		w.file, w.fresh, w.dirty = nil, false, false
		w.gen++ // as below: an edit's tick in flight is stale
		w.enterLoadError(findings)
		return dropped
	}
	if err != nil {
		return w.toast("could not reload "+w.path+": "+err.Error(), true)
	}
	mapping.SamplesFromTests(w.ctx, f.Doc)
	w.file, w.fresh, w.dirty = f, false, false
	w.gen++ // an edit's tick still in flight would check what this replaced
	w.doc = *f.Doc
	w.loadErr = nil
	w.sess.ReadOnly = !f.Editable
	cmds := []tea.Cmd{w.rebuild(), w.toast("reloaded "+w.path, false)}
	if w.modelDue {
		cmds = append(cmds, w.openModelDialog())
	}
	return tea.Batch(cmds...)
}

// saveConflict says the file is not as it was left and offers the ways out.
// It closes with an answer, or doneMsg{} to cancel.
type saveConflict struct {
	s        *Session
	path     string
	appeared bool // a fresh file that now exists, rather than one that changed
}

func newSaveConflict(s *Session, path string, appeared bool) *saveConflict {
	return &saveConflict{s: s, path: path, appeared: appeared}
}

func (c *saveConflict) Update(msg tea.Msg) (component, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	switch k.String() {
	case "r":
		c.s.post(doneMsg{result: answerReload})
	case "o":
		c.s.post(doneMsg{result: answerOverwrite})
	case "esc":
		c.s.post(doneMsg{})
	}
	return c, nil
}

func (c *saveConflict) View(width, height int) string {
	what := " changed on disk since it was opened"
	if c.appeared {
		what = " appeared on disk"
	}
	return wrapped(pathTail(style.SanitizeTerminal(c.path), width)+what, width, height)
}

func (c *saveConflict) Keys() []string {
	return []string{"r reload", "o save over it", "esc cancel"}
}

func (c *saveConflict) heading() (string, string) { return "Not saved", "" }

func (c *saveConflict) isDialog() {}
