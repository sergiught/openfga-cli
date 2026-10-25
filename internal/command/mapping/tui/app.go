package tui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/style"
)

// App is the program: the start screen, then the workspace.
type App struct {
	ctx  context.Context
	opts Options

	// start is the screen shown until a file is chosen; nil once ws is set.
	start *startScreen
	ws    *workspace

	width, height int
}

// NewApp starts on the start screen, or straight in the workspace when
// opts.Path names a file. A path that does not exist opens a new mapping bound
// to it, as the start screen's New blank does. An error means that file could
// not be read, or could not be created where it was named.
func NewApp(ctx context.Context, opts Options) (*App, error) {
	a := &App{ctx: ctx, opts: opts}
	if opts.Path == "" {
		a.start = newStartScreen(ctx, opts)
		return a, nil
	}
	o, fresh, err := openArg(ctx, opts.Path)
	if err != nil {
		return nil, err
	}
	// A fresh path joins the recent list when its first save creates it.
	if !fresh {
		_ = rememberRecent(opts.RecentFile, opts.Path)
	}
	a.ws = newWorkspace(ctx, opts, o)
	return a, nil
}

// openArg opens the file argument: fresh when it does not exist yet. A path
// that cannot be opened or created is a usage error naming what is wrong.
func openArg(ctx context.Context, path string) (o opened, fresh bool, err error) {
	_, lerr := os.Lstat(path)
	if lerr == nil {
		// A link whose target is gone would only fail at the first save.
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			target, _ := os.Readlink(path)
			return opened{}, false, clierr.WithCode(clierr.CodeUsage, fmt.Errorf("%s is a broken symlink to %s",
				style.SanitizeTerminal(filepath.Base(path)), style.SanitizeTerminal(target)))
		}
		o, err := openPath(ctx, path)
		return o, false, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return opened{}, false, err
	}
	// The nearest ancestor that exists decides: a file there is no folder, and
	// the next one down the path is the first that is missing.
	child, dir := abs, filepath.Dir(abs)
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return opened{}, false, clierr.WithCode(clierr.CodeUsage, fmt.Errorf(
					"%s is a file, not a folder", style.SanitizeTerminal(filepath.Base(dir))))
			}
			break
		}
		up := filepath.Dir(dir)
		if up == dir {
			return opened{}, false, lerr
		}
		child, dir = dir, up
	}
	if child != abs {
		return opened{}, false, clierr.WithCode(clierr.CodeUsage, fmt.Errorf(
			"%s does not exist: create it first, or pass a path in an existing folder",
			style.SanitizeTerminal(filepath.Base(child))))
	}
	if !errors.Is(lerr, fs.ErrNotExist) {
		return opened{}, false, lerr
	}
	return opened{path: abs, doc: &mapping.Document{Rules: []mapping.Rule{{}}}}, true, nil
}

func (a *App) Init() tea.Cmd {
	if a.ws != nil {
		return a.ws.Init()
	}
	return a.start.Init()
}

// Update gives every message to the screen that is up. The workspace's command
// goes back untouched: tea.ExecProcess (the editor) and tea.Quit only work as
// the program receives them.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m, ok := msg.(tea.WindowSizeMsg); ok {
		a.width, a.height = m.Width, m.Height
	}
	if a.ws != nil {
		return a, a.ws.Update(msg)
	}
	cmd := a.start.Update(msg)
	if a.start.chosen == nil {
		return a, cmd
	}

	// The start screen is dropped for good and the workspace has no way back
	// to it, so only one workspace ever exists. That matters because its
	// background jobs (the store model fetch, the Tests badge run) count
	// generations from 0: a late result from an earlier workspace could
	// otherwise pass for the new one's. If a way back is ever added, drop the
	// old workspace's messages instead.
	a.ws = newWorkspace(a.ctx, a.opts, *a.start.chosen)
	// The toasts carry over: both models number theirs from 1, so a start
	// screen toast's expiry still in flight would otherwise close the
	// workspace's toast of the same number.
	a.ws.toasts = a.start.toasts
	a.start = nil
	// The new workspace has not seen the window yet.
	return a, tea.Batch(cmd, a.ws.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height}), a.ws.Init())
}

func (a *App) View() tea.View {
	var s string
	if a.ws != nil {
		s = a.ws.View()
	} else {
		s = a.start.View()
	}
	v := tea.NewView(s)
	v.AltScreen = true
	v.BackgroundColor = style.BgBase
	return v
}
