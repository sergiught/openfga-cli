package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/style"
)

// modelState is the model the mapping is checked against, and the store fetch
// that may be producing the next one.
type modelState struct {
	label  string // "model ./model.fga (12 types)", "store model 01ARZ… (3 types)", "no model"
	path   string // the file in use, "" otherwise
	gen    int    // numbers the choices, so a late fetch is told from the current one
	cancel context.CancelFunc
}

// storeModelMsg is a store fetch finishing.
type storeModelMsg struct {
	gen   int
	model *openfga.AuthorizationModel
	err   error
}

// openModelDialog asks which model to check against. Esc keeps the one in use.
func (w *workspace) openModelDialog() tea.Cmd {
	// While the file is broken the dialog is still owed: backing out of one
	// the user asked for with m is no answer to the one postponed at open.
	if w.loadErr == nil {
		w.modelDue = false
	}
	st := newStack(w.sess, newModelDialog(w.sess, filepath.Dir(w.path), w.opts.Store))
	return w.openOverlay(st, func(result any) tea.Cmd {
		if c, ok := result.(modelChoice); ok {
			// A choice is the answer, even over a broken file.
			w.modelDue = false
			return w.useModel(c)
		}
		return nil
	})
}

// useModel switches to the chosen model. A store model arrives later, as a
// storeModelMsg; a choice made meanwhile cancels the fetch.
func (w *workspace) useModel(c modelChoice) tea.Cmd {
	w.cancelModelFetch()
	w.model.gen++
	switch c.kind {
	case modelFile:
		// A pipe would block the read; a folder is no model.
		if info, err := os.Stat(c.path); err == nil && !info.Mode().IsRegular() {
			return w.modelFailed("could not read the model: " + c.path + " is not a regular file")
		}
		raw, err := os.ReadFile(c.path)
		if err != nil {
			return w.modelFailed("could not read the model: " + err.Error())
		}
		lm, err := modeltest.LoadModelBytes(raw)
		if err != nil {
			return w.modelFailed("could not read the model: " + err.Error())
		}
		ix := mapping.IndexModel(lm.SDK)
		return w.modelChanged(ix, w.fileLabel(c.path, ix), c.path)
	case modelStore:
		if w.opts.LoadStoreModel == nil {
			return w.modelFailed("could not read the store model: no store is configured")
		}
		ctx, cancel := context.WithCancel(w.ctx)
		w.model.cancel = cancel
		w.busy++
		gen, load := w.model.gen, w.opts.LoadStoreModel
		fetch := func() tea.Msg {
			m, err := load(ctx)
			return storeModelMsg{gen: gen, model: m, err: err}
		}
		return tea.Batch(w.spin.Tick, fetch)
	}
	return w.modelChanged(nil, noModel, "")
}

// storeModelArrived applies a finished fetch unless a later choice replaced it.
func (w *workspace) storeModelArrived(msg storeModelMsg) tea.Cmd {
	w.busy--
	if msg.gen != w.model.gen {
		return nil
	}
	w.model.cancel = nil
	if msg.err != nil || msg.model == nil {
		err := msg.err
		if err == nil {
			err = fmt.Errorf("the store has no model")
		}
		return w.modelFailed("could not read the store model: " + err.Error())
	}
	ix := mapping.IndexModel(msg.model)
	label := "store model"
	if msg.model.ID != "" {
		label += " " + msg.model.ID
	}
	return w.modelChanged(ix, fmt.Sprintf("%s (%s)", label, mapping.Plural(len(ix.Types), "type")), "")
}

// modelFailed leaves no model, and says why.
func (w *workspace) modelFailed(text string) tea.Cmd {
	return tea.Batch(w.toast(text, true), w.modelChanged(nil, noModel, ""))
}

// fileLabel names a model file the way the mapping sits beside it.
func (w *workspace) fileLabel(path string, ix *mapping.ModelIndex) string {
	name := path
	if filepath.Dir(path) == filepath.Dir(w.path) {
		name = "./" + filepath.Base(path)
	}
	return fmt.Sprintf("model %s (%s)", name, mapping.Plural(len(ix.Types), "type"))
}

// modelChanged makes ix the model in use. The shown pane re-reads it now and
// the others when switchTo shows them: each resume can run samples, up to a
// second apiece, on the goroutine that draws the screen. Check runs at once:
// the answer is what the user asked for, not an edit to settle. While an
// overlay's answer is handled, the pane is resumed once it is on screen again.
func (w *workspace) modelChanged(ix *mapping.ModelIndex, label, path string) tea.Cmd {
	w.sess.Index = ix
	w.model.label, w.model.path = label, path
	var cmd tea.Cmd
	if !w.closing {
		cmd = w.switchTo(w.active)
	}
	w.runCheck()
	return cmd
}

// cancelModelFetch stops a store fetch in flight.
func (w *workspace) cancelModelFetch() {
	if w.model.cancel != nil {
		w.model.cancel()
		w.model.cancel = nil
	}
}

// modelName is the model as Check words it, "" when there is none.
func (w *workspace) modelName() string {
	if w.sess.Index == nil {
		return ""
	}
	return style.SanitizeTerminal(w.model.label)
}
