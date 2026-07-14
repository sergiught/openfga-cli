// Command ofga is a modern CLI and TUI for OpenFGA.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"slices"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/log"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/command/base"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
	"github.com/sergiught/openfga-cli/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	logger := log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: false,
		Level:           logLevel(os.Args[1:]),
		Prefix:          "ofga",
	})

	// Config loads before cobra parses flags, so --config is read from argv here
	// (env OPENFGA_CONFIG is honored by LoadFrom when no flag is given).
	cfg, err := config.LoadFrom(configPathFromArgs(os.Args[1:]))
	if err != nil {
		// base.New (which builds the profile-aware writers) hasn't run yet, so
		// wrap stderr here too — otherwise this early error leaks ANSI to a pipe
		// and ignores NO_COLOR.
		errw := colorprofile.NewWriter(os.Stderr, os.Environ())
		output.Errorf(errw, "%s", err.Error())
		if path := config.DefaultPath(); path != "" {
			output.Hintf(errw, "fix or remove %s, then try again", path)
		}
		os.Exit(clierr.CodeError)
	}
	icons.Apply(icons.Parse(cfg.IconsMode()))

	c := cli.New(logger, cfg, version.Resolved())

	root := base.New(c)
	rootCmd := root.Command()
	rootCmd.SetContext(ctx)
	cmd, err := rootCmd.ExecuteC()
	if err != nil {
		logger.Debugf("command failed: %+v", err)
		if ctx.Err() != nil {
			// A signal (Ctrl-C) cancelled the request context. signal.NotifyContext
			// cancels with a signalError cause rather than context.Canceled, so the
			// wrapped request error would otherwise be misread as a network failure.
			// The context's own Err() is context.Canceled regardless of cause.
			output.Errorf(root.ErrWriter(), "canceled")
			os.Exit(clierr.CodeCanceled)
		}
		output.Errorf(root.ErrWriter(), "%s", clierr.Friendly(err))
		code := clierr.Code(err)
		if !root.RanCommand() || code == clierr.CodeUsage {
			// The error came from flag/arg validation, not the command body: a
			// bad invocation. Point at usage on stderr and exit CodeUsage so
			// scripts can tell a mistyped command from a runtime failure.
			// cobra validates required flags after PersistentPreRunE marks the
			// command as run, so those (and RunE-level usage errors) surface via
			// the CodeUsage classification rather than RanCommand.
			code = clierr.CodeUsage
			output.Hintf(root.ErrWriter(), "run '%s --help' for usage", cmd.CommandPath())
		} else if !errors.Is(err, prompt.ErrAborted) && logger.GetLevel() > log.DebugLevel {
			// A deliberate abort has nothing more to show under -v; only hint for
			// genuine failures where extra detail could help.
			output.Hintf(root.ErrWriter(), "run with -v for more detail")
		}
		os.Exit(code)
	}
}

// configPathFromArgs extracts a --config value from raw args, before cobra
// parses them (config must load first). Returns "" when the flag is absent.
func configPathFromArgs(args []string) string {
	for i, a := range args {
		// Stop at the `--` terminator: anything after it is a positional
		// argument, not a flag, so a literal "--config" there isn't ours.
		if a == "--" {
			break
		}
		if a == "--config" && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--config="); ok {
			return v
		}
	}
	return ""
}

// logLevel raises verbosity when --verbose or -v is present.
func logLevel(args []string) log.Level {
	if slices.Contains(args, "--verbose") || slices.Contains(args, "-v") {
		return log.DebugLevel
	}
	return log.WarnLevel
}
