// Package base provides the root `ofga` command: persistent flags, the help
// banner, and registration of every top-level sub-command.
package base

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
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
	"github.com/sergiught/openfga-cli/internal/readlimit"
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
ofga api GET /stores

# Enable shell completion for this Bash session
source <(ofga completion bash)`,
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
			return c.runPlayground(cmd, args)
		},
		// Resolve color + theme + output mode before any command renders.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if cli.RequestTimeout < 0 {
				return fmt.Errorf("--timeout must be non-negative")
			}
			// Validate -o/--output before marking the command as run, so a bad
			// value is treated as a usage error (exit 2), like other bad flags.
			if err := c.resolveOutput(); err != nil {
				return err
			}
			// Reaching here means flag/arg validation passed, so any later error
			// is a runtime failure rather than a bad invocation.
			c.ranCommand = true
			if err := c.applySecretFiles(); err != nil {
				return err
			}
			c.applyEnvironment()
			return nil
		},
	}

	// lipgloss v2 always emits full-fidelity truecolor ANSI; downsampling for
	// non-TTY output (pipes) or NO_COLOR now happens here, at the writer layer,
	// rather than at Render time as lipgloss v1 did. This covers all cobra
	// output — help, usage, and the banner baked into Long above.
	noColorAtConstruction := cli.NoColor || cli.Plain || cli.ThemeName == "mono" || cli.Config.Theme == "mono" ||
		((os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb") && !ForceColor()) ||
		ForceMono()
	if noColorAtConstruction {
		c.outW = &colorprofile.Writer{Forward: os.Stdout, Profile: colorprofile.NoTTY}
		c.errW = &colorprofile.Writer{Forward: os.Stderr, Profile: colorprofile.NoTTY}
	} else {
		c.outW = colorprofile.NewWriter(os.Stdout, os.Environ())
		c.errW = colorprofile.NewWriter(os.Stderr, os.Environ())
	}
	c.cmd.SetOut(c.outW)
	c.cmd.SetErr(c.errW)
	// Styled help across the whole command tree (cobra reuses the root's func).
	c.cmd.SetHelpFunc(c.helpFunc)
	// Both `--version` and `ofga version` render the same full build line.
	c.cmd.SetVersionTemplate("ofga " + version.String() + "\n")
	// cobra's `--help` flag short-circuits before PersistentPreRunE runs, so
	// retain the construction-time decision on the writers themselves. Explicit
	// --no-color/--plain/mono always wins over a force-color environment.
	if noColorAtConstruction {
		c.outW.Profile = colorprofile.NoTTY
		c.errW.Profile = colorprofile.NoTTY
	} else if ForceColor() {
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
	pf.StringVar(&cli.Overrides.StoreID, "store-id", "", "store ID (overrides profile/env)")
	pf.StringVar(&cli.Overrides.ModelID, "model-id", "", "authorization model ID (overrides profile/env)")
	pf.String("config", "", "path to the config file (overrides OPENFGA_CONFIG)")
	pf.StringVar(&cli.APITokenFile, "auth-token-file", "", "read the process-scoped API token from a file")
	pf.StringVar(&cli.ClientSecretFile, "auth-client-secret-file", "", "read the process-scoped OAuth client secret from a file")
	pf.StringVar(&cli.PrivateKeyFile, "auth-private-key-file", "", "read the process-scoped private-key JWT signing key from a file")
	pf.StringVarP(&cli.Output, "output", "o", "", "output format: json, yaml, plain or table")
	pf.BoolVar(&cli.JSON, "json", false, "output machine-readable JSON (alias for --output json)")
	pf.BoolVar(&cli.YAML, "yaml", false, "output machine-readable YAML (alias for --output yaml)")
	pf.BoolVar(&cli.Plain, "plain", cli.Plain, "output unstyled, tab-separated rows (alias for --output plain)")
	pf.BoolVarP(&cli.Quiet, "quiet", "q", false, "suppress incidental output")
	pf.BoolVar(&cli.NoInput, "no-input", false, "never prompt or launch the interactive TUI")
	pf.BoolVar(&cli.NoColor, "no-color", cli.NoColor, "disable colored output")
	pf.StringVar(&cli.ThemeName, "theme", cli.ThemeName, "color theme ("+themeList()+")")
	pf.DurationVar(&cli.RequestTimeout, "timeout", cli.RequestTimeout, "per-request timeout (0 disables)")
	// Registered so debug flags are known and appear in help; their values are
	// value is read from os.Args in main.logLevel, which must set the log level
	// before cobra parses (to cover errors during early config loading).
	pf.BoolP("debug", "d", false, "enable debug logging")
	pf.BoolP("verbose", "v", false, "alias for --debug")

	_ = c.cmd.RegisterFlagCompletionFunc("profile", c.completeProfiles)
	_ = c.cmd.RegisterFlagCompletionFunc("store-id", c.completeStores)
	_ = c.cmd.RegisterFlagCompletionFunc("model-id", c.completeModels)
	_ = c.cmd.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"json", "yaml", "plain", "table"}, cobra.ShellCompDirectiveNoFileComp
	})

	c.RegisterSubCommands()
	return c
}

// applyEnvironment resolves the color profile, theme, and output toggles from
// flags, environment (NO_COLOR, FORCE_COLOR, TERM=dumb) and config.
// resolveOutput maps the -o/--output flag onto the JSON/YAML/Plain toggles.
// When set it is authoritative (so `-o table` overrides a stray --json), which
// removes the "what if both?" ambiguity. An unset -o leaves the boolean output
// aliases as parsed.
func (c *Command) resolveOutput() error {
	switch c.cli.Output {
	case "":
		// With no authoritative -o, the boolean aliases stand as parsed.
		// Multiple aliases are contradictory, so reject them rather than
		// silently letting one win.
		selected := 0
		for _, enabled := range []bool{c.cli.JSON, c.cli.YAML, c.cli.Plain} {
			if enabled {
				selected++
			}
		}
		if selected > 1 {
			return fmt.Errorf("cannot combine --json, --yaml, and --plain; pick one (or use -o json|yaml|plain|table)")
		}
		return nil
	case "json":
		c.cli.JSON, c.cli.YAML, c.cli.Plain = true, false, false
	case "yaml":
		c.cli.JSON, c.cli.YAML, c.cli.Plain = false, true, false
	case "plain":
		c.cli.JSON, c.cli.YAML, c.cli.Plain = false, false, true
	case "table":
		c.cli.JSON, c.cli.YAML, c.cli.Plain = false, false, false
	default:
		return fmt.Errorf("invalid --output %q: want json, yaml, plain or table", c.cli.Output)
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

	noColor := os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" || ForceMono()
	force := ForceColor()

	if a.NoColor || (noColor && !force) {
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

func (c *Command) applySecretFiles() error {
	read := func(path, label string) (string, error) {
		if path == "" {
			return "", nil
		}
		data, err := readlimit.File(path, readlimit.Secret, label)
		if err != nil {
			return "", err
		}
		if warning := readlimit.SecretPermissionWarning(path, label); warning != "" {
			fmt.Fprintln(c.cmd.ErrOrStderr(), warning)
		}
		value := strings.TrimSpace(string(data))
		if value == "" {
			return "", fmt.Errorf("%s is empty", label)
		}
		return value, nil
	}
	var err error
	if c.cli.Overrides.APIToken, err = read(c.cli.APITokenFile, "API token file"); err != nil {
		return err
	}
	if c.cli.Overrides.ClientSecret, err = read(c.cli.ClientSecretFile, "client secret file"); err != nil {
		return err
	}
	if c.cli.Overrides.PrivateKey, err = read(c.cli.PrivateKeyFile, "private key file"); err != nil {
		return err
	}
	return nil
}

// ForceColor and ForceMono interpret the documented FORCE_COLOR variable, which
// colorprofile's writers ignore (they honor only CLICOLOR_FORCE). Following the
// widely-adopted convention (npm/chalk/supports-color), FORCE_COLOR=0/false/no/off
// disables color and any other non-empty value forces it on; unset is no opinion.
// ForceColor reports whether color is explicitly forced on.
func ForceColor() bool { f := colorForce(); return f != nil && *f }

// ForceMono reports whether color is explicitly forced off.
func ForceMono() bool { f := colorForce(); return f != nil && !*f }

// colorForce returns the FORCE_COLOR intent: nil when unset/empty, else a pointer
// to true (force on) or false (force off).
func colorForce() *bool {
	v, ok := os.LookupEnv("FORCE_COLOR")
	if !ok || v == "" {
		return nil
	}
	on := true
	switch strings.ToLower(v) {
	case "0", "false", "no", "off":
		on = false
	}
	return &on
}

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
// or --yaml it emits a machine-readable object so scripts can parse the build.
func (c *Command) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print version and build information",
		Args:    cobra.NoArgs,
		Example: "  ofga version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]string{
					"version": version.Resolved(),
					"commit":  version.Commit,
					"built":   version.Date,
				})
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "ofga "+version.String())
			return err
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
		c.playgroundCmd(),
		c.versionCmd(),
	)
}

func (c *Command) playgroundCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "playground",
		Short: "Launch the interactive playground",
		Args:  cobra.NoArgs,
		RunE:  c.runPlayground,
	}
}

func (c *Command) runPlayground(cmd *cobra.Command, _ []string) error {
	// The TUI needs an interactive terminal; without one (piped, CI, no TTY)
	// bare `ofga` prints concise guidance instead of launching and hanging.
	// The explicit `ofga playground` command fails so automation cannot mistake
	// "the TUI never ran" for success.
	if c.cli.NoInput || !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		if cmd.Name() == "playground" {
			return clierr.WithCode(clierr.CodeUsage,
				fmt.Errorf("playground requires an interactive terminal and cannot be used with --no-input"))
		}
		return c.conciseHelp(cmd)
	}
	return playground.Run(cmd.Context(), c.cli)
}

func (c *Command) conciseHelp(cmd *cobra.Command) error {
	_, err := fmt.Fprintf(cmd.OutOrStdout(), `%s

Usage:
  ofga [command] [flags]

Examples:
  ofga init
  ofga stores list
  ofga query check user:anne viewer document:roadmap

Run "ofga --help" for the full command and environment reference.
`, cmd.Short)
	return err
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
