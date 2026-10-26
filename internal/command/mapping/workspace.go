package mapping

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/command/mapping/tui"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
)

// runWorkspace is bare `ofga mappings [file]`: the full-screen workspace, opened
// on file or on the start screen.
func (c *Command) runWorkspace(cmd *cobra.Command, args []string) error {
	// Checked first: a typo'd subcommand should hear "unknown command", not
	// "needs a terminal".
	if len(args) == 1 && c.looksLikeTypo(args[0]) {
		return c.cli.GroupRunE(cmd, args)
	}
	if c.cli.Agent.Enabled {
		return c.cli.Agent.Refuse("the mappings workspace")
	}
	if !interactive(cmd, c.cli) {
		return clierr.WithCode(clierr.CodeUsage, errors.New(
			"ofga mappings opens an interactive workspace and needs a terminal; "+
				"in scripts use 'ofga mappings validate', 'test' or 'apply'"))
	}

	icons.Apply(icons.Parse(c.cli.Config.IconsMode()))

	opts := c.workspaceOptions(cmd, args)
	app, err := tui.NewApp(cmd.Context(), opts)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(app, tea.WithContext(cmd.Context())).Run()
	return err
}

// looksLikeTypo reports whether arg is no existing path and is close to a
// subcommand's name. A file named like a subcommand still opens: the path wins.
func (c *Command) looksLikeTypo(arg string) bool {
	if _, err := os.Stat(arg); err == nil {
		return false
	}
	return len(c.cmd.SuggestionsFor(arg)) > 0
}

// workspaceOptions gathers what the tui needs from the CLI's configuration.
func (c *Command) workspaceOptions(cmd *cobra.Command, args []string) tui.Options {
	opts := tui.Options{Dir: ".", Version: c.cli.Version}
	if len(args) == 1 {
		opts.Path = args[0]
	}
	if cwd, err := os.Getwd(); err == nil {
		opts.Dir = cwd
	}
	if p := config.DefaultPath(); p != "" {
		opts.RecentFile = filepath.Join(filepath.Dir(p), "mappings-recent.json")
	}
	// A config that does not resolve gives no store option and the workspace
	// itself needs none; the Apply pane says why instead.
	r, err := c.cli.Resolve()
	switch {
	case err != nil:
		opts.ApplyErr = clierr.Friendly(err)
	case r.StoreID != "":
		opts.Store = fmt.Sprintf("profile %s, store %s", r.Profile, r.StoreID)
		opts.LoadStoreModel = c.storeModel
		opts.Apply, opts.ApplyErr = c.applyTarget(cmd, r.Profile)
	}
	return opts
}

// interactive reports whether a full-screen UI can run: structured output and
// --no-input rule it out, and both ends of the pipe must be a terminal.
// Mirrors configcmd's gate so the commands behave alike.
func interactive(cmd *cobra.Command, c *cli.CLI) bool {
	if c.NoInput || c.JSON || c.YAML {
		return false
	}
	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(in.Fd()) {
		return false
	}
	return term.IsTerminal(os.Stdout.Fd())
}

// applyTarget is the Apply pane's store, or why the store that is set cannot
// be used, such as a malformed ID. Building the client does no network I/O;
// the store's name, tuples and writes are only fetched when the pane asks.
func (c *Command) applyTarget(cmd *cobra.Command, profile string) (*tui.ApplyTarget, string) {
	cl, r, err := c.cli.ClientWithStore()
	if err != nil {
		return nil, clierr.Friendly(err)
	}
	flags := resumeFlags(cmd)
	sc := storeClient{cl}
	return &tui.ApplyTarget{
		StoreID: r.StoreID,
		Profile: profile,
		Name: func(ctx context.Context) string {
			s, err := cl.Stores.Get(ctx, r.StoreID)
			if err != nil {
				return ""
			}
			return s.Name
		},
		Reader: sc,
		Writer: sc,
		Hints:  applyHints,
		Resume: func(path, eventsPath string, from int) string {
			return resumeLine(path, eventsPath, from, flags)
		},
	}, ""
}
