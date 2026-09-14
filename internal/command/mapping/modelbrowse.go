package mapping

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/style"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// openModelBrowse lists the working directory for a model to load. It starts
// there rather than anywhere cleverer because a mapping is written beside the
// model it maps onto far more often than not.
func (m *wizardModel) openModelBrowse() {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	m.enterDir(dir)
	m.push(screenModelBrowse)
}

// enterDir lists one directory. A directory that cannot be read leaves the
// listing on the one that could, and says why: replacing it with an empty pane
// would make an unreadable directory look like an empty one.
func (m *wizardModel) enterDir(dir string) {
	items, err := modelDirItems(dir)
	if err != nil {
		m.errMsg = fmt.Sprintf("could not list %s: %v", dir, err)
		return
	}
	m.modelDir = dir
	m.modelFiles.SetItems(items)
	m.modelFiles.ResetFilter()
}

// modelDirItems lists what is worth opening in dir: the parent, the
// subdirectories, and the files a model can live in.
//
// Dot-entries are left out. .git alone holds more directories than most of the
// trees this browser will walk, and burying model.fga under them would make the
// listing worse at the one thing it is for. A model kept inside one is still
// reachable by typing its path, which is the other half of this screen.
func modelDirItems(dir string) ([]uilist.Item, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var dirs, files []uilist.Item
	if parent := filepath.Dir(dir); parent != dir {
		dirs = append(dirs, uilist.Item{TitleText: "../", Filter: "..", ID: parent})
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		it := uilist.Item{TitleText: e.Name(), Filter: e.Name(), ID: filepath.Join(dir, e.Name())}
		switch {
		case e.IsDir():
			// The trailing slash is the only thing telling the two apart: the rows
			// are compact, so a description saying "directory" is never drawn.
			it.TitleText += "/"
			dirs = append(dirs, it)
		case filepath.Ext(e.Name()) == ".fga", filepath.Ext(e.Name()) == ".json":
			files = append(files, it)
		}
	}

	// Directories first, so walking down a tree does not mean stepping over the
	// files at every level. os.ReadDir has already sorted each group by name.
	items := append(dirs, files...)
	for i := range items {
		items[i].Index = i
	}
	return items, nil
}

func (m *wizardModel) keyModelBrowse(k tea.KeyPressMsg) tea.Cmd {
	if m.modelFiles.SettingFilter() {
		cmd := m.modelFiles.Update(k)
		m.modelFiles.ResyncFilter()
		return cmd
	}
	switch k.String() {
	case "esc":
		m.pop()
		return nil
	case "ctrl+p":
		m.push(screenModelFile)
		m.modelPath.Resume()
		return nil
	case "enter":
		it, ok := m.modelFiles.Selected()
		if !ok {
			return nil
		}
		// A row that stats as a directory is walked into; everything else is
		// tried as a model. A stat that fails falls through on purpose — the read
		// below reports what went wrong with the actual path, which is a better
		// sentence than anything this branch could write.
		if info, err := os.Stat(it.ID); err == nil && info.IsDir() {
			m.enterDir(it.ID)
			return nil
		}
		return m.loadModelFile(it.ID)
	}
	return m.modelFiles.Update(k)
}

// loadModelFile indexes a model from disk and leaves the model screens. A file
// that will not read or parse leaves the wizard exactly where it is with the
// reason on screen: whichever screen asked is the one to try again from.
func (m *wizardModel) loadModelFile(path string) tea.Cmd {
	raw, err := os.ReadFile(path)
	if err != nil {
		m.errMsg = fmt.Sprintf("could not read %s: %v", path, err)
		return nil
	}
	loaded, err := modeltest.LoadModelBytes(raw)
	if err != nil {
		m.errMsg = fmt.Sprintf("could not parse %s: %v", path, err)
		return nil
	}
	m.index = mapping.IndexModel(loaded.SDK)
	m.leaveModelScreens()
	return nil
}

// isModelScreen reports whether a screen belongs to the model question.
func isModelScreen(s screen) bool {
	switch s {
	case screenModelSource, screenModelBrowse, screenModelFile:
		return true
	}
	return false
}

// leaveModelScreens returns to whatever asked for a model, so every way of
// answering it lands in the same place however deep the answer was given. The
// question spans three screens — the source, the browser, and the typed path —
// and a fixed number of pops was already wrong once when the third arrived.
func (m *wizardModel) leaveModelScreens() {
	for len(m.stack) > 1 && isModelScreen(m.top()) {
		m.pop()
	}
}

// modelBrowseBody puts the directory being listed above the listing. The rows
// carry bare names, so without it two directories each holding a model.fga look
// like the same screen.
//
// No empty state, unlike the other list screens: every directory but the root
// offers its parent, so the listing is never actually empty, and a folder with
// nothing in it says so by offering only the way back out.
func (m *wizardModel) modelBrowseBody(cw int) string {
	return style.SectionHeader(pathTail(m.modelDir, cw), cw) + "\n" + m.modelFiles.View()
}

// pathTail shortens a directory path to fit, dropping whole segments from the
// front. The last segments are the ones that say where you are; the first ones
// are the part you could have guessed, which is why the generic truncation that
// eats the end is the wrong one for a path.
func pathTail(p string, width int) string {
	if lipgloss.Width(p) <= width {
		return p
	}
	segs := strings.Split(p, string(filepath.Separator))
	for i := 1; i < len(segs); i++ {
		if tail := "…/" + filepath.Join(segs[i:]...); lipgloss.Width(tail) <= width {
			return tail
		}
	}
	// Not even the last segment fits, so there is nothing left to keep whole.
	return ansi.Truncate(p, width, "…")
}
