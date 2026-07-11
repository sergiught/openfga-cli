// Package base provides the root `ofga` command: persistent flags, the help
// banner, and registration of every top-level sub-command.
package base

import (
	"fmt"
	"os"

	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/app"
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
	app  *app.App
	cmd  *cobra.Command
	outW *colorprofile.Writer
	errW *colorprofile.Writer
}

// New constructs the root command and wires persistent flags + sub-commands.
func New(a *app.App) *Command {
	c := &Command{app: a}

	c.cmd = &cobra.Command{
		Use:   "ofga",
		Short: "A modern CLI & TUI for OpenFGA",
		Long:  banner(a.Version),
		Example: `  ofga                       launch the interactive TUI
  ofga store list            list stores
  ofga query check user:anne viewer document:roadmap
  ofga model graph           visualize the latest model`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       a.Version,
		Args:          cobra.NoArgs,
		// Bare `ofga` launches the TUI (clig.dev: lead with the primary value).
		RunE: func(cmd *cobra.Command, _ []string) error {
			return playground.Run(cmd.Context(), a)
		},
		// Resolve color + theme + output mode before any command renders.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
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
	// cobra's `--help` flag short-circuits before PersistentPreRunE runs, so
	// applyEnvironment's NO_COLOR handling below never fires for `--help`.
	// Force the fully-stripping profile from the env var here too, so
	// `NO_COLOR=1 ofga --help` is byte-clean even on a TTY.
	if os.Getenv("NO_COLOR") != "" && os.Getenv("FORCE_COLOR") == "" {
		c.outW.Profile = colorprofile.NoTTY
		c.errW.Profile = colorprofile.NoTTY
	}

	pf := c.cmd.PersistentFlags()
	pf.StringVarP(&a.Overrides.Profile, "profile", "p", "", "configuration profile to use")
	pf.StringVar(&a.Overrides.APIURL, "api-url", "", "OpenFGA API URL (overrides profile/env)")
	pf.StringVar(&a.Overrides.StoreID, "store", "", "store ID (overrides profile/env)")
	pf.StringVar(&a.Overrides.ModelID, "model", "", "authorization model ID (overrides profile/env)")
	pf.StringVar(&a.Overrides.APIToken, "token", "", "API bearer token (use OPENFGA_API_TOKEN to avoid leaking via ps)")
	pf.BoolVar(&a.JSON, "json", false, "output machine-readable JSON")
	pf.BoolVar(&a.Plain, "plain", false, "output unstyled, tab-separated rows (grep/awk friendly)")
	pf.BoolVarP(&a.Quiet, "quiet", "q", false, "suppress incidental output")
	pf.BoolVar(&a.NoColor, "no-color", false, "disable colored output")
	pf.StringVar(&a.ThemeName, "theme", "", "color theme ("+themeList()+")")
	pf.BoolP("verbose", "v", false, "enable debug logging")

	c.RegisterSubCommands()
	return c
}

// applyEnvironment resolves the color profile, theme, and output toggles from
// flags, environment (NO_COLOR, FORCE_COLOR, TERM=dumb) and config.
func (c *Command) applyEnvironment() {
	a := c.app
	output.Quiet = a.Quiet
	output.Plain = a.Plain

	noColor := a.NoColor || a.Plain ||
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
		profiles.New(c.app).Command(),
		store.New(c.app).Command(),
		model.New(c.app).Command(),
		tuple.New(c.app).Command(),
		query.New(c.app).Command(),
		assertions.New(c.app).Command(),
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
