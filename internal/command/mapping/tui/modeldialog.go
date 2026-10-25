package tui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/picker"
)

type modelKind int

const (
	modelNone modelKind = iota
	modelFile
	modelStore
)

// modelChoice is what the model dialog closes with.
type modelChoice struct {
	kind modelKind
	path string // modelFile
}

// Row values the picker switches on.
const (
	rowBeside  = "beside"
	rowStore   = "store"
	rowBrowse  = "browse"
	rowNoModel = "none"
)

// modelDialog asks which model to check the mapping against. It only reports
// the choice; the workspace loads the file or fetches from the store.
type modelDialog struct {
	s      *Session
	dir    string
	beside string // the model file found beside the mapping, or ""
	picks  *picker.Picker
}

// besideModels are the names looked for next to the mapping, in order.
var besideModels = []string{"model.fga", "model.json"}

func newModelDialog(s *Session, dir, store string) *modelDialog {
	c := &modelDialog{s: s, dir: dir}
	for _, name := range besideModels {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			c.beside = p
			break
		}
	}

	var items []picker.Item
	cursor := -1
	add := func(it picker.Item, preselect bool) {
		if preselect {
			cursor = len(items)
		}
		items = append(items, it)
	}
	if c.beside != "" {
		add(picker.Item{Title: style.SanitizeTerminal(filepath.Base(c.beside)),
			Desc: "The model file beside the mapping", Value: rowBeside}, true)
	}
	if store != "" {
		add(picker.Item{Title: style.SanitizeTerminal(fmt.Sprintf("The store model (%s)", store)),
			Value: rowStore}, c.beside == "")
	}
	add(picker.Item{Title: "Another model file…", Value: rowBrowse}, false)
	add(picker.Item{Title: "No model — check the mapping alone", Value: rowNoModel}, cursor < 0)

	c.picks = picker.New(items)
	c.picks.SetCursor(cursor)
	return c
}

func (c *modelDialog) Update(msg tea.Msg) (component, tea.Cmd) {
	if m, ok := msg.(closedMsg); ok {
		// Back from the browser: a path is a pick, nothing means it was
		// cancelled and the question is still open.
		if p, ok := m.result.(string); ok && p != "" {
			c.s.post(doneMsg{result: modelChoice{kind: modelFile, path: p}})
		}
		return c, nil
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	switch k.String() {
	case "esc":
		// Always nothing: the workspace reads that as keeping the model in use,
		// so esc can never discard one.
		c.s.post(doneMsg{})
	case "up", "k":
		c.picks.Move(-1)
	case "down", "j":
		c.picks.Move(1)
	case "enter":
		switch c.picks.Selected().Value {
		case rowBeside:
			c.s.post(doneMsg{result: modelChoice{kind: modelFile, path: c.beside}})
		case rowStore:
			c.s.post(doneMsg{result: modelChoice{kind: modelStore}})
		case rowBrowse:
			c.s.post(openMsg{c: newFileBrowse(c.s, "Choose a model file", c.dir, []string{".fga", ".json"})})
		case rowNoModel:
			c.s.post(doneMsg{result: modelChoice{kind: modelNone}})
		}
	}
	return c, nil
}

func (c *modelDialog) View(width, height int) string { return pickerWindow(c.picks, width, height) }

func (c *modelDialog) Keys() []string {
	return []string{"↑↓ choose", "↵ select", "esc keep current"}
}

func (c *modelDialog) heading() (string, string) {
	return "Check against a model", "Which model should the mapping be checked against?"
}

func (c *modelDialog) isDialog() {}
