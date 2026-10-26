package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
)

// DirItems lists what is worth opening in dir: the parent, the
// subdirectories, and the files whose extension is in exts.
//
// Dot-entries are left out. .git alone holds more directories than most of the
// trees a browser will walk, and burying model.fga under them would make the
// listing worse at the one thing it is for. A file kept inside one is still
// reachable by typing its path, which is the other half of the browser.
//
// Names come off the disk, so they are sanitized for display; Item.ID keeps the
// real path.
func DirItems(dir string, exts []string) ([]uilist.Item, error) {
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
		name := style.SanitizeTerminal(e.Name())
		it := uilist.Item{TitleText: name, Filter: name, ID: filepath.Join(dir, e.Name())}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			// ReadDir reports the link itself: a linked folder is a folder
			// to walk into, and the ID keeps the link the user sees.
			info, err := os.Stat(it.ID)
			isDir = err == nil && info.IsDir()
		}
		switch {
		case isDir:
			// The trailing slash is the only thing telling the two apart: the rows
			// are compact, so a description saying "directory" is never drawn.
			it.TitleText += "/"
			dirs = append(dirs, it)
		case hasExt(e.Name(), exts):
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

func hasExt(name string, exts []string) bool {
	ext := filepath.Ext(name)
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}

// fileBrowse walks directories to a file. It closes with the absolute path of
// the file chosen as a string, or with nothing.
type fileBrowse struct {
	s          *Session
	title      string
	exts       []string
	dir        string
	list       *uilist.List
	path       *field.Form
	typingPath bool
}

func newFileBrowse(s *Session, title, dir string, exts []string) *fileBrowse {
	l := uilist.New()
	l.SetCompact(true)
	c := &fileBrowse{s: s, title: title, exts: exts, list: l,
		path: field.NewForm(field.New("Path", "/path/to/file"))}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	c.enterDir(dir)
	return c
}

// enterDir lists one directory. One that cannot be read leaves the listing on
// the one that could, and says why: replacing it with an empty pane would make
// an unreadable directory look like an empty one.
func (c *fileBrowse) enterDir(dir string) {
	items, err := DirItems(dir, c.exts)
	if err != nil {
		c.s.post(toastMsg{text: style.SanitizeTerminal(fmt.Sprintf("could not list %s: %v", dir, err)), err: true})
		return
	}
	c.dir = dir
	c.list.SetItems(items)
	c.list.ResetFilter()
}

func (c *fileBrowse) Init() tea.Cmd { return nil }

func (c *fileBrowse) Update(msg tea.Msg) (component, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		if c.typingPath {
			return c, c.path.Update(msg)
		}
		return c, c.list.Update(msg)
	}
	if c.typingPath {
		return c, c.keyPath(k)
	}
	if c.list.SettingFilter() {
		cmd := c.list.Update(k)
		c.list.ResyncFilter()
		return c, cmd
	}
	switch k.String() {
	case "esc":
		c.s.post(doneMsg{})
		return c, nil
	case "ctrl+p":
		c.typingPath = true
		c.path.Reset()
		return c, c.path.Init()
	case "enter":
		it, ok := c.list.Selected()
		if ok {
			c.open(it.ID)
		}
		return c, nil
	}
	return c, c.list.Update(k)
}

// open walks into a directory and picks anything else. A path that does not
// stat is picked too, on purpose: the reader of the pick reports what went
// wrong with the actual path, which is a better sentence than anything this
// could write.
func (c *fileBrowse) open(path string) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		c.enterDir(path)
		return
	}
	c.s.post(doneMsg{result: path})
}

func (c *fileBrowse) keyPath(k tea.KeyPressMsg) tea.Cmd {
	if k.String() == "esc" {
		c.typingPath = false
		return nil
	}
	cmd := c.path.Update(k)
	if c.path.Completed() {
		c.path.Resume()
		p := strings.TrimSpace(c.path.Values()[0])
		if p == "" {
			return cmd
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(c.dir, p)
		}
		c.typingPath = false
		c.open(filepath.Clean(p))
	}
	return cmd
}

func (c *fileBrowse) View(width, height int) string {
	if c.typingPath {
		c.path.SetWidth(width)
		c.path.SetHeight(height)
		return windowAround(strings.Split(c.path.View(), "\n"), 0, height)
	}
	c.list.SetSize(width, max(height-1, 1))
	out := c.list.View()
	if height >= 2 {
		out = style.SectionHeader(style.SanitizeTerminal(pathTail(c.dir, width)), width) + "\n" + out
	}
	// A list has a floor of a few rows, so a very short pane has to be cut.
	if lines := strings.Split(out, "\n"); len(lines) > height {
		out = strings.Join(lines[:max(height, 1)], "\n")
	}
	return out
}

func (c *fileBrowse) Keys() []string {
	if c.typingPath {
		return []string{"↵ open", "esc back to list"}
	}
	return []string{"/ filter", "↵ open", "ctrl+p type a path", "esc cancel"}
}

func (c *fileBrowse) heading() (string, string) { return c.title, "" }

func (c *fileBrowse) filtering() bool { return !c.typingPath && c.list.SettingFilter() }

func (c *fileBrowse) typing() bool { return c.typingPath }

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
