// Package base provides the root `ofga` command: persistent flags, the help
// banner, and registration of every top-level sub-command.
package base

import (
	"fmt"
	"os"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/command/api"
	"github.com/sergiught/openfga-cli/internal/command/assertions"
	"github.com/sergiught/openfga-cli/internal/command/model"
	"github.com/sergiught/openfga-cli/internal/command/playground"
	"github.com/sergiught/openfga-cli/internal/command/profiles"
	"github.com/sergiught/openfga-cli/internal/command/query"
	"github.com/sergiught/openfga-cli/internal/command/store"
	"github.com/sergiught/openfga-cli/internal/command/tuple"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/theme"
)

// Command is the root command.
type Command struct {
	cli  *cli.CLI
	cmd  *cobra.Command
	outW *colorprofile.Writer
	errW *colorprofile.Writer
}

// New constructs the root command and wires persistent flags + sub-commands.
func New(cli *cli.CLI) *Command {
	c := &Command{cli: cli}

	c.cmd = &cobra.Command{
		Use:   "ofga",
		Short: "A modern CLI & TUI for OpenFGA",
		Long:  banner(cli.Version),
		Example: `# Launch the interactive TUI
ofga

# List stores
ofga stores list

# Run a check against the active store
ofga query check user:anne viewer document:roadmap

# Explore the latest model as a graph
ofga model graph

# Send a raw API request using the active profile's auth
ofga api GET /stores`,
		// SilenceUsage starts false so cobra prints usage for flag/arg errors
		// (which occur before PersistentPreRunE); PersistentPreRunE then flips it
		// on so a later runtime/API error doesn't dump usage. SilenceErrors is on
		// because main formats and prints the error itself.
		SilenceErrors: true,
		Version:       cli.Version,
		// No Args validator: cobra's default lets a bare `ofga` launch the TUI
		// while still rejecting an unknown first token with a "Did you mean…?"
		// suggestion (cobra.NoArgs would suppress that suggestion).
		// Bare `ofga` launches the TUI (clig.dev: lead with the primary value).
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				// Unreachable once cobra's arg validation runs, but guards against
				// a future Args override silently routing typos into the TUI.
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return playground.Run(cmd.Context(), cli)
		},
		// Resolve color + theme + output mode before any command renders.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Past flag/arg validation now: silence usage so a runtime error
			// (network, API, etc.) prints just the message, not the whole usage.
			cmd.SilenceUsage = true
			c.applyEnvironment()
			return nil
		},
	}

	// lipgloss v2 always emits full-fidelity truecolor ANSI; downsampling for
	// non-TTY output (pipes) or NO_COLOR now happens here, at the writer layer,
	// rather than at Render time as lipgloss v1 did. This covers all cobra
	// output — help, usage, and the banner baked into Long above.
	c.outW = colorprofile.NewWriter(os.Stdout, os.Environ())
	c.errW = colorprofile.NewWriter(os.Stderr, os.Environ())
	c.cmd.SetOut(c.outW)
	c.cmd.SetErr(c.errW)
	// Styled help across the whole command tree (cobra reuses the root's func).
	c.cmd.SetHelpFunc(c.helpFunc)
	// cobra's `--help` flag short-circuits before PersistentPreRunE runs, so
	// applyEnvironment's NO_COLOR handling below never fires for `--help`.
	// Force the fully-stripping profile from the env var here too, so
	// `NO_COLOR=1 ofga --help` is byte-clean even on a TTY.
	if os.Getenv("NO_COLOR") != "" && os.Getenv("FORCE_COLOR") == "" {
		c.outW.Profile = colorprofile.NoTTY
		c.errW.Profile = colorprofile.NoTTY
	}

	pf := c.cmd.PersistentFlags()
	pf.StringVarP(&cli.Overrides.Profile, "profile", "p", "", "configuration profile to use")
	pf.StringVar(&cli.Overrides.StoreID, "store", "", "store ID (overrides profile/env)")
	pf.StringVar(&cli.Overrides.ModelID, "model", "", "authorization model ID (overrides profile/env)")
	pf.BoolVar(&cli.JSON, "json", false, "output machine-readable JSON")
	pf.BoolVar(&cli.Plain, "plain", false, "output unstyled, tab-separated rows (grep/awk friendly)")
	pf.BoolVarP(&cli.Quiet, "quiet", "q", false, "suppress incidental output")
	pf.BoolVar(&cli.NoColor, "no-color", false, "disable colored output")
	pf.StringVar(&cli.ThemeName, "theme", "", "color theme ("+themeList()+")")
	// Registered so `--verbose`/`-v` is a known flag and appears in help; its
	// value is read from os.Args in main.logLevel, which must set the log level
	// before cobra parses (to cover errors during early config loading).
	pf.BoolP("verbose", "v", false, "enable debug logging")

	c.RegisterSubCommands()
	return c
}

// applyEnvironment resolves the color profile, theme, and output toggles from
// flags, environment (NO_COLOR, FORCE_COLOR, TERM=dumb) and config.
func (c *Command) applyEnvironment() {
	a := c.cli
	output.Quiet = a.Quiet
	output.Plain = a.Plain
	output.Interactive = term.IsTerminal(os.Stdout.Fd())

	// --plain is an explicit machine-output mode: always unstyled and
	// tab-separated regardless of FORCE_COLOR or a TTY, so scripts get a
	// deterministic, escape-free stream.
	if a.Plain {
		style.Apply(theme.Mono())
		c.outW.Profile = colorprofile.NoTTY
		c.errW.Profile = colorprofile.NoTTY
		return
	}

	noColor := a.NoColor ||
		os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"
	force := os.Getenv("FORCE_COLOR") != ""

	if noColor && !force {
		style.Apply(theme.Mono())
		// colorprofile's own NO_COLOR handling only strips color params,
		// leaving attribute codes (bold, etc.) intact on a TTY. Force the
		// fully-stripping profile so NO_COLOR output has zero escape bytes,
		// matching the pre-v2 behavior.
		c.outW.Profile = colorprofile.NoTTY
		c.errW.Profile = colorprofile.NoTTY
		return
	}

	name := a.Config.Theme
	if a.ThemeName != "" {
		name = a.ThemeName
	}
	if name == "" || !style.SetTheme(name) {
		style.Apply(theme.Default())
	}
}

// Command returns the root cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

// ErrWriter returns the profile-aware writer wrapping stderr, so callers
// outside cobra's own output path (e.g. main's final error print) can reuse
// the same NO_COLOR/pipe-aware downsampling.
func (c *Command) ErrWriter() *colorprofile.Writer { return c.errW }

// RegisterSubCommands adds all top-level commands.
func (c *Command) RegisterSubCommands() {
	c.cmd.AddCommand(
		profiles.New(c.cli).Command(),
		store.New(c.cli).Command(),
		model.New(c.cli).Command(),
		tuple.New(c.cli).Command(),
		query.New(c.cli).Command(),
		assertions.New(c.cli).Command(),
		api.New(c.cli).Command(),
	)
}

func themeList() string {
	names := theme.Names()
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

func banner(version string) string {
	logo := style.Gradient("ofga")
	tag := style.Subtitle.Render("a modern CLI & TUI for OpenFGA")
	ver := style.Faint.Render(fmt.Sprintf("version %s", version))
	return fmt.Sprintf("%s — %s\n%s\n\nManage stores, authorization models, relationship tuples,\nrun checks, and explore everything interactively with `ofga`.", logo, tag, ver)
}
