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
	"github.com/sergiught/openfga-cli/internal/command/configcmd"
	"github.com/sergiught/openfga-cli/internal/command/model"
	"github.com/sergiught/openfga-cli/internal/command/playground"
	"github.com/sergiught/openfga-cli/internal/command/profiles"
	"github.com/sergiught/openfga-cli/internal/command/query"
	"github.com/sergiught/openfga-cli/internal/command/store"
	"github.com/sergiught/openfga-cli/internal/command/tuple"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/theme"
	"github.com/sergiught/openfga-cli/internal/version"
)

// Command is the root command.
type Command struct {
	cli  *cli.CLI
	cmd  *cobra.Command
	outW *colorprofile.Writer
	errW *colorprofile.Writer
	// ranCommand records whether execution got past flag/arg validation into
	// PersistentPreRunE. When it is false after an error, the failure was a bad
	// invocation (unknown flag/command, wrong arg count) rather than a runtime
	// error, so main can exit with CodeUsage.
	ranCommand bool
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
		// Silence cobra's own usage dump and error line: it resolves usage to
		// stdout (SetOut below), leaking a 50-line help block into scripts that
		// capture stdout on failure. main prints the error to stderr instead, and
		// adds a concise "run --help" hint on bad invocations (clig.dev:
		// diagnostics on stderr).
		SilenceUsage:  true,
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
			// The TUI needs an interactive terminal; without one (piped, CI, no
			// TTY) print help instead of launching and hanging.
			if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
				return cmd.Help()
			}
			return playground.Run(cmd.Context(), cli)
		},
		// Resolve color + theme + output mode before any command renders.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Validate -o/--output before marking the command as run, so a bad
			// value is treated as a usage error (exit 2), like other bad flags.
			if err := c.resolveOutput(); err != nil {
				return err
			}
			// Reaching here means flag/arg validation passed, so any later error
			// is a runtime failure rather than a bad invocation.
			c.ranCommand = true
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
	// Both `--version` and `ofga version` render the same full build line.
	c.cmd.SetVersionTemplate("ofga " + version.String() + "\n")
	// cobra's `--help` flag short-circuits before PersistentPreRunE runs, so
	// applyEnvironment's NO_COLOR handling below never fires for `--help`.
	// Force the fully-stripping profile from the env var here too, so
	// `NO_COLOR=1 ofga --help` is byte-clean even on a TTY.
	if os.Getenv("NO_COLOR") != "" && os.Getenv("FORCE_COLOR") == "" {
		c.outW.Profile = colorprofile.NoTTY
		c.errW.Profile = colorprofile.NoTTY
	} else if forceColor() {
		// FORCE_COLOR (documented in the banner) must force color even through a
		// pipe, matching CLICOLOR_FORCE. colorprofile's writers only honor
		// CLICOLOR_FORCE, so upgrade the profile here so `--help` (which
		// short-circuits before PersistentPreRunE) is colored too.
		c.outW.Profile = colorprofile.TrueColor
		c.errW.Profile = colorprofile.TrueColor
	}

	pf := c.cmd.PersistentFlags()
	pf.StringVarP(&cli.Overrides.Profile, "profile", "p", "", "configuration profile to use")
	pf.StringVar(&cli.Overrides.APIURL, "api-url", "", "OpenFGA API URL (overrides profile/env)")
	pf.StringVar(&cli.Overrides.StoreID, "store", "", "store ID (overrides profile/env)")
	pf.StringVar(&cli.Overrides.ModelID, "model", "", "authorization model ID (overrides profile/env)")
	pf.String("config", "", "path to the config file (overrides OPENFGA_CONFIG)")
	pf.StringVarP(&cli.Output, "output", "o", "", "output format: json, plain or table")
	pf.BoolVar(&cli.JSON, "json", false, "output machine-readable JSON (alias for --output json)")
	pf.BoolVar(&cli.Plain, "plain", false, "output unstyled, tab-separated rows (alias for --output plain)")
	pf.BoolVarP(&cli.Quiet, "quiet", "q", false, "suppress incidental output")
	pf.BoolVar(&cli.NoColor, "no-color", false, "disable colored output")
	pf.StringVar(&cli.ThemeName, "theme", "", "color theme ("+themeList()+")")
	// Registered so `--verbose`/`-v` is a known flag and appears in help; its
	// value is read from os.Args in main.logLevel, which must set the log level
	// before cobra parses (to cover errors during early config loading).
	pf.BoolP("verbose", "v", false, "enable debug logging")

	_ = c.cmd.RegisterFlagCompletionFunc("profile", c.completeProfiles)
	_ = c.cmd.RegisterFlagCompletionFunc("store", c.completeStores)
	_ = c.cmd.RegisterFlagCompletionFunc("model", c.completeModels)
	_ = c.cmd.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"json", "plain", "table"}, cobra.ShellCompDirectiveNoFileComp
	})

	c.RegisterSubCommands()
	return c
}

// applyEnvironment resolves the color profile, theme, and output toggles from
// flags, environment (NO_COLOR, FORCE_COLOR, TERM=dumb) and config.
// resolveOutput maps the -o/--output flag onto the JSON/Plain toggles. When set
// it is authoritative (so `-o table` overrides a stray --json), which removes
// the "what if both?" ambiguity. An unset -o leaves --json/--plain as parsed.
func (c *Command) resolveOutput() error {
	switch c.cli.Output {
	case "":
		// With no authoritative -o, the --json/--plain booleans stand as parsed.
		// Passing both is contradictory, so reject it rather than silently
		// letting one win.
		if c.cli.JSON && c.cli.Plain {
			return fmt.Errorf("cannot use --json and --plain together; pick one (or use -o json|plain|table)")
		}
		return nil
	case "json":
		c.cli.JSON, c.cli.Plain = true, false
	case "plain":
		c.cli.JSON, c.cli.Plain = false, true
	case "table":
		c.cli.JSON, c.cli.Plain = false, false
	default:
		return fmt.Errorf("invalid --output %q: want json, plain or table", c.cli.Output)
	}
	return nil
}

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
	force := forceColor()

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
	if force {
		// Force full-color output even when writing to a pipe (colorprofile
		// would otherwise downgrade a non-TTY to NoTTY).
		c.outW.Profile = colorprofile.TrueColor
		c.errW.Profile = colorprofile.TrueColor
	}

	name := a.Config.Theme
	if a.ThemeName != "" {
		name = a.ThemeName
	}
	if name == "" || !style.SetTheme(name) {
		style.Apply(theme.Default())
	}
}

// forceColor reports whether color should be forced on regardless of TTY
// detection. CLICOLOR_FORCE is honored by colorprofile's writers directly; this
// covers the documented FORCE_COLOR variable, which they ignore.
func forceColor() bool { return os.Getenv("FORCE_COLOR") != "" }

// Command returns the root cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

// ErrWriter returns the profile-aware writer wrapping stderr, so callers
// outside cobra's own output path (e.g. main's final error print) can reuse
// the same NO_COLOR/pipe-aware downsampling.
func (c *Command) ErrWriter() *colorprofile.Writer { return c.errW }

// RanCommand reports whether execution reached PersistentPreRunE, i.e. flag and
// argument validation passed. When it is false after Execute returns an error,
// the failure was a bad invocation and main exits with CodeUsage.
func (c *Command) RanCommand() bool { return c.ranCommand }

// versionCmd prints the full build info (version, commit, date). Under --json
// it emits a machine-readable object so scripts can parse the build.
func (c *Command) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print version and build information",
		Args:    cobra.NoArgs,
		Example: "  ofga version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), map[string]string{
					"version": version.Version,
					"commit":  version.Commit,
					"built":   version.Date,
				})
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ofga "+version.String())
			return nil
		},
	}
}

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
		configcmd.New(c.cli).Command(),
		configcmd.NewInit(c.cli),
		configcmd.NewTheme(c.cli),
		c.versionCmd(),
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
