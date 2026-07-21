package model

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
)

// watchConfig carries the run knobs `model test --watch` reuses on every re-run.
type watchConfig struct {
	run            string
	parallel       int
	dedupe         bool
	explain        string
	coverage       bool
	coverageDetail bool
}

// watchExtensions are the workspace file types whose change triggers a re-run.
var watchExtensions = map[string]bool{
	".fga": true, ".mod": true, ".yaml": true, ".yml": true,
	".json": true, ".jsonl": true, ".csv": true,
}

// runWatch runs the workspace once, then re-runs it whenever a relevant file
// under the workspace root changes, until the context is cancelled (Ctrl-C).
func runWatch(cmd *cobra.Command, a *cli.CLI, path string, wsOpts modeltest.WorkspaceOptions, cfg watchConfig) error {
	ctx := cmd.Context()

	root, err := resolveWatchRoot(path, wsOpts)
	if err != nil {
		return err
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("start file watcher: %w", err)
	}
	defer w.Close()
	if err := addDirsRecursive(w, root); err != nil {
		return fmt.Errorf("watch %s: %w", root, err)
	}

	changed := make(chan struct{}, 1)
	watchErrs := make(chan error, 1)
	go debounceEvents(ctx, w, changed, watchErrs)

	runOnceForWatch(cmd, a, root, path, wsOpts, cfg)
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(cmd.OutOrStdout())
			return ctx.Err()
		case err := <-watchErrs:
			fmt.Fprintln(cmd.ErrOrStderr(), style.Failure.Render("● file watcher: "+err.Error()))
		case <-changed:
			// A change may have created new subdirectories; watch them too.
			if err := addDirsRecursive(w, root); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), style.Failure.Render("● file watcher: "+err.Error()))
			}
			runOnceForWatch(cmd, a, root, path, wsOpts, cfg)
		}
	}
}

func resolveWatchRoot(path string, wsOpts modeltest.WorkspaceOptions) (string, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(root); statErr == nil && !info.IsDir() {
		root = filepath.Dir(root)
	}
	if discovered, discoverErr := modeltest.FindWorkspaceRoot(path); discoverErr == nil {
		root = discovered
	} else if ws, loadErr := modeltest.LoadWorkspaceWith(path, wsOpts); loadErr == nil {
		root = ws.Root
	}
	return root, nil
}

// runOnceForWatch clears the screen, prints a header, then loads and runs the
// workspace, rendering the human report. Errors are printed and swallowed so the
// watch loop keeps going (a bad edit shouldn't stop the watcher).
func runOnceForWatch(cmd *cobra.Command, a *cli.CLI, root, path string, wsOpts modeltest.WorkspaceOptions, cfg watchConfig) {
	out := cmd.OutOrStdout()
	// Only clear the screen for an interactive terminal; `--watch > log` must
	// not have escape sequences spewed into the redirected file.
	if output.Interactive {
		fmt.Fprint(out, "\033[2J\033[H") // clear screen + cursor home
	}
	header := fmt.Sprintf("watching %s — Ctrl-C to stop (%s)", root, time.Now().Format("15:04:05"))
	fmt.Fprintf(out, "%s\n\n", style.Faint.Render(header))

	ws, err := modeltest.LoadWorkspaceWith(path, wsOpts)
	if err != nil {
		fmt.Fprintln(out, style.Failure.Render("● load workspace: "+err.Error()))
		return
	}

	var serverOpts map[string]any
	if ws.Manifest != nil {
		serverOpts = ws.Manifest.Server
	}
	eng, err := modeltest.NewEmbeddedEngine(serverOpts)
	if err != nil {
		fmt.Fprintln(out, style.Failure.Render("● start engine: "+err.Error()))
		return
	}
	defer eng.Close()

	res, err := modeltest.Run(cmd.Context(), ws, modeltest.Options{
		Run:      cfg.run,
		Parallel: cfg.parallel,
		Dedupe:   cfg.dedupe,
		Engine:   eng,
		Explain:  cfg.explain,
		Coverage: cfg.coverage,
	})
	if err != nil {
		fmt.Fprintln(out, style.Failure.Render("● "+err.Error()))
		return
	}
	_ = renderTestResults(cmd, a, res, cfg.explain, cfg.coverageDetail, false)
}

// addDirsRecursive registers root and every subdirectory (skipping hidden dirs
// like .git and node_modules) with the watcher.
func addDirsRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than aborting the walk
		}
		if !d.IsDir() {
			return nil
		}
		if p != root {
			if base := filepath.Base(p); strings.HasPrefix(base, ".") || base == "node_modules" {
				return filepath.SkipDir
			}
		}
		_ = w.Add(p)
		return nil
	})
}

// debounceEvents coalesces a burst of file events (editors write several times
// per save) into a single signal after a short quiet period.
func debounceEvents(ctx context.Context, w *fsnotify.Watcher, changed chan<- struct{}, watchErrs chan<- error) {
	const quiet = 200 * time.Millisecond
	var timer *time.Timer
	var timerC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if !watchExtensions[strings.ToLower(filepath.Ext(ev.Name))] {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(quiet)
				timerC = timer.C
			} else {
				timer.Reset(quiet)
			}
		case <-timerC:
			select {
			case changed <- struct{}{}:
			default: // a re-run is already pending; drop this one
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			select {
			case watchErrs <- err:
			default:
			}
		}
	}
}
