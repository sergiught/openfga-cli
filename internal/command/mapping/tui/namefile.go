package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/ui/field"
)

// nameFile asks what to call a new mapping file. It closes with the absolute
// path, or with nothing on esc.
type nameFile struct {
	s    *Session
	dir  string
	form *field.Form
}

// newNameFile asks for a file name (default "mapping.yaml" in dir); an existing
// file is refused inline. Closes with the absolute path.
func newNameFile(s *Session, dir string) *nameFile {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	c := &nameFile{s: s, dir: dir}
	f := field.New("File name", mapping.DefaultFile).WithValidate(func(v string) error {
		p := c.resolve(v)
		// Lstat, so a dangling symlink also counts: saving through it would
		// write somewhere the user did not name.
		if _, err := os.Lstat(p); err == nil {
			return fmt.Errorf("%s already exists", style.SanitizeTerminal(p))
		}
		return nil
	})
	c.form = field.NewForm(f)
	c.form.SetValues([]string{mapping.DefaultFile})
	return c
}

// resolve is the absolute path a typed name stands for.
func (c *nameFile) resolve(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		v = mapping.DefaultFile
	}
	if !filepath.IsAbs(v) {
		v = filepath.Join(c.dir, v)
	}
	return filepath.Clean(v)
}

func (c *nameFile) Init() tea.Cmd { return c.form.Init() }

func (c *nameFile) Update(msg tea.Msg) (component, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		c.s.post(doneMsg{})
		return c, nil
	}
	cmd := c.form.Update(msg)
	if c.form.Completed() {
		c.s.post(doneMsg{result: c.resolve(c.form.Values()[0])})
	}
	return c, cmd
}

func (c *nameFile) View(width, height int) string {
	c.form.SetWidth(width)
	return c.form.View()
}

func (c *nameFile) Keys() []string { return []string{"↵ create", "esc back"} }

func (c *nameFile) heading() (string, string) {
	return "Name the file", "Nothing is written until you save."
}

func (c *nameFile) typing() bool { return true }
