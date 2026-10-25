package tui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
	uilist "github.com/sergiught/openfga-cli/internal/ui/list"
	"github.com/sergiught/openfga-cli/internal/ui/shell"
	"github.com/sergiught/openfga-cli/internal/ui/toast"
)

// Row ids that are not files. Headings have no id: they are not choices.
const (
	newEventsID = "__new_events__"
	newBlankID  = "__new_blank__"
	// headingFilter is what a heading row matches a filter on, which is
	// nothing a person types.
	headingFilter = "\x00"
)

// startStep is what the start screen is waiting on from a child it opened.
type startStep int

const (
	stepIdle      startStep = iota
	stepBrowse              // the file browser, for a path
	stepFork                // the rule fork, for an Auth0 event
	stepNameAuth0           // a name for the file the fork's rules go in
	stepNameBlank           // a name for an empty mapping
)

// start is the root of the start screen: recent files, mapping files found in
// the working folder, and the two ways to begin a new one. It closes with the
// opened file, or with nothing to quit.
type start struct {
	s    *Session
	opts Options
	dir  string
	list *uilist.List
	step startStep
	// recent is what the list was built from, so a missing file can be dropped
	// from the screen without re-reading the list.
	recent []string
}

func newStart(s *Session, opts Options) *start {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	l := uilist.New()
	l.SetFilterPlaceholder("filter files")
	l.SetCompact(true)
	c := &start{s: s, opts: opts, dir: dir, list: l, recent: loadRecent(opts.RecentFile)}
	c.fill()
	return c
}

// fill builds the rows. Everything that came off the disk is sanitized for
// display; Item.ID keeps the real path.
func (c *start) fill() {
	var items []uilist.Item
	heading := func(text string) {
		items = append(items, uilist.Item{TitleText: style.Faint.Render(text), Filter: headingFilter})
	}
	add := func(title, id string) {
		items = append(items, uilist.Item{TitleText: title, Filter: title, ID: id})
	}

	seen := map[string]bool{}
	if len(c.recent) > 0 {
		heading("RECENT")
	}
	for _, p := range c.recent {
		seen[p] = true
		add(fileRow(style.SanitizeTerminal(p), p), p)
	}

	var found []foundFile
	for _, f := range findMappings(c.dir) {
		if abs := filepath.Join(c.dir, f.path); !seen[abs] {
			found = append(found, f)
		}
	}
	if len(found) > 0 {
		heading("FOUND HERE")
	}
	for _, f := range found {
		add(fmt.Sprintf("%s · %s", style.SanitizeTerminal(f.path), mapping.Plural(f.rules, "rule")), filepath.Join(c.dir, f.path))
	}

	heading("NEW")
	add("New from Auth0 events", newEventsID)
	add("New blank mapping", newBlankID)

	for i := range items {
		items[i].Index = i
	}
	c.list.SetItems(items)
	c.skipHeading(1)
}

// fileRow is "path · N rules", or the path alone for a file that is not a
// mapping (it still opens, in load-error mode, which says why).
func fileRow(shown, path string) string {
	if n, ok := mappingRules(path); ok {
		return fmt.Sprintf("%s · %s", shown, mapping.Plural(n, "rule"))
	}
	return shown
}

// skipHeading moves the cursor off a heading row, in direction dir, or the
// other way when it runs out of rows.
func (c *start) skipHeading(dir int) {
	for _, d := range []int{dir, -dir} {
		for {
			it, ok := c.list.Selected()
			if !ok || it.ID != "" || it.Filter != headingFilter {
				return
			}
			// Select does not clamp, so the ends are checked here.
			next := c.list.Model.Index() + d
			if next < 0 || next >= len(c.list.Model.VisibleItems()) {
				break
			}
			c.list.SelectIndex(next)
		}
	}
}

func (c *start) Update(msg tea.Msg) (component, tea.Cmd) {
	switch m := msg.(type) {
	case closedMsg:
		c.childClosed(m.result)
		return c, nil
	case tea.KeyPressMsg:
		return c, c.key(m)
	}
	return c, c.list.Update(msg)
}

func (c *start) key(k tea.KeyPressMsg) tea.Cmd {
	if c.list.SettingFilter() {
		cmd := c.list.Update(k)
		c.list.ResyncFilter()
		return cmd
	}
	switch k.String() {
	case "esc", "q", "ctrl+c":
		c.s.post(doneMsg{})
		return nil
	case "o":
		c.step = stepBrowse
		c.s.post(openMsg{c: newFileBrowse(c.s, "Open a mapping", c.dir, []string{".yaml", ".yml"})})
		return nil
	case "enter":
		it, ok := c.list.Selected()
		if !ok || it.Filter == headingFilter {
			return nil
		}
		switch it.ID {
		case newEventsID:
			c.step = stepFork
			c.s.post(openMsg{c: newRuleFork(c.s, false)})
		case newBlankID:
			c.step = stepNameBlank
			c.s.post(openMsg{c: newNameFile(c.s, c.dir)})
		default:
			c.open(it.ID)
		}
		return nil
	}
	cmd := c.list.Update(k)
	switch k.String() {
	case "up", "k", "pgup", "home":
		c.skipHeading(-1)
	default:
		c.skipHeading(1)
	}
	return cmd
}

// open loads path and closes with it. A file that is gone is a toast and
// leaves the recent list; the YAML failing to parse is not an error here, the
// workspace opens in load-error mode and says why.
func (c *start) open(path string) {
	o, err := openPath(c.s.Ctx, path)
	if err != nil {
		text := fmt.Sprintf("could not open %s: %v", path, err)
		if errors.Is(err, fs.ErrNotExist) {
			text = path + " is gone"
		}
		c.s.post(toastMsg{text: style.SanitizeTerminal(text), err: true})
		if errors.Is(err, fs.ErrNotExist) {
			c.recent = slices.DeleteFunc(c.recent, func(p string) bool { return p == path })
			c.fill()
		}
		return
	}
	// Best effort: the recent list is a convenience, as in loadRecent.
	_ = rememberRecent(c.opts.RecentFile, path)
	c.s.post(doneMsg{result: o})
}

// childClosed is a child the start screen opened going away, with what it
// chose or nothing.
func (c *start) childClosed(result any) {
	step := c.step
	c.step = stepIdle
	switch r := result.(type) {
	case string:
		switch step {
		case stepBrowse:
			c.open(r)
		case stepNameAuth0:
			d := *c.s.Doc
			*c.s.Doc = mapping.Document{}
			c.s.post(doneMsg{result: opened{path: r, doc: &d, auth0: true}})
		case stepNameBlank:
			c.s.post(doneMsg{result: opened{path: r, doc: &mapping.Document{Rules: []mapping.Rule{{}}}}})
		}
	case RuleAdded:
		c.step = stepNameAuth0
		c.s.post(openMsg{c: newNameFile(c.s, c.dir)})
	default:
		// Backed out of the fork or of the name: whatever the fork added goes
		// with it.
		*c.s.Doc = mapping.Document{}
	}
}

func (c *start) View(width, height int) string {
	c.list.SetSize(width, max(height, 1))
	return fitHeight(c.list.View(), max(height, 1))
}

func (c *start) Keys() []string {
	return []string{"↵ open", "o browse", "/ filter", "esc quit"}
}

func (c *start) heading() (string, string) {
	return "Mappings", "Open a mapping file, or start a new one."
}

func (c *start) filtering() bool { return c.list.SettingFilter() }

// startScreen hosts start and the components it opens: the program until a
// file is chosen.
type startScreen struct {
	opts   Options
	st     *Stack
	sh     *shell.Shell
	toasts toast.Model
	width  int
	height int

	// chosen is what the screen closed with, and quit says it closed with
	// nothing; App reads chosen, and tea.Quit ends the program for quit.
	chosen *opened
	quit   bool
}

func newStartScreen(ctx context.Context, opts Options) *startScreen {
	s := NewSession(ctx, &mapping.Document{}, nil)
	return &startScreen{opts: opts, st: newStack(s, newStart(s, opts)), sh: shell.New(), toasts: toast.New()}
}

func (h *startScreen) Init() tea.Cmd { return h.st.Init() }

func (h *startScreen) Update(msg tea.Msg) tea.Cmd {
	toastCmd := h.toasts.Update(msg)
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		h.width, h.height = m.Width, m.Height
		h.sh.SetSize(m.Width, m.Height)
		return toastCmd
	case tea.KeyPressMsg:
		// ctrl+c quits from anywhere, whatever child has the keys.
		if m.String() == "ctrl+c" {
			h.quit = true
			return tea.Quit
		}
	}
	cmd, out := h.st.Update(msg)
	cmds := []tea.Cmd{toastCmd, cmd}
	for _, t := range out.Toasts {
		lvl := toast.Info
		if t.Err {
			lvl = toast.Error
		}
		cmds = append(cmds, h.toasts.Push(lvl, t.Text))
	}
	// There is no model on this screen to show: say so instead of leaving the
	// recipe's m key looking dead.
	if slices.Contains(out.Requests, any(RequestModel)) {
		cmds = append(cmds, h.toasts.Push(toast.Info, "the model is chosen once a mapping is open"))
	}
	if out.Closed {
		if o, ok := out.Result.(opened); ok {
			h.chosen = &o
		} else {
			h.quit = true
			cmds = append(cmds, tea.Quit)
		}
	}
	return tea.Batch(cmds...)
}

func (h *startScreen) View() string {
	if h.width < minWidth || h.height < minHeight {
		return lipgloss.Place(h.width, h.height, lipgloss.Center, lipgloss.Center,
			style.Faint.Render("terminal too small — need at least 60×16"))
	}
	h.sh.SetSidebar(nil, nil, "")
	h.sh.SetBrand("", h.opts.Version)
	h.sh.SetMain(strings.Join(h.st.Titles(), " › "), h.body())
	h.sh.SetStatus(shell.Status{Keys: h.st.Keys()})
	h.sh.SetDialog("", "")
	h.sh.SetToast(h.toasts.View())
	h.sh.SetFocus(shell.FocusPanel)
	return h.sh.View()
}

func (h *startScreen) body() string {
	mw, mh := h.sh.MainSize()
	if h.sh.Collapsed() {
		mh--
	}
	mh = max(mh, 1)
	var head string
	if _, sub := h.st.Heading(); sub != "" {
		head = style.Subtitle.Width(mw).Render(sub) + "\n\n"
	}
	rows := max(mh-lipgloss.Height(head)+1, 1)
	return fitHeight(head+h.st.View(mw, rows), mh)
}
