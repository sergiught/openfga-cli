// Command ofga is a modern CLI and TUI for OpenFGA.
package main

import (
	"context"
	"os"
	"os/signal"
	"slices"
	"strings"

	"github.com/charmbracelet/log"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/command/base"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
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
		output.Errorf(os.Stderr, "%s", err.Error())
		if path := config.DefaultPath(); path != "" {
			output.Hintf(os.Stderr, "fix or remove %s, then try again", path)
		}
		os.Exit(clierr.CodeError)
	}
	icons.Apply(icons.Parse(cfg.IconsMode()))

	c := cli.New(logger, cfg, version.Version)

	root := base.New(c)
	rootCmd := root.Command()
	rootCmd.SetContext(ctx)
	cmd, err := rootCmd.ExecuteC()
	if err != nil {
		logger.Debugf("command failed: %+v", err)
		output.Errorf(root.ErrWriter(), "%s", clierr.Friendly(err))
		code := clierr.Code(err)
		if !root.RanCommand() {
			// The error came from flag/arg validation, not the command body: a
			// bad invocation. Point at usage on stderr and exit CodeUsage so
			// scripts can tell a mistyped command from a runtime failure.
			code = clierr.CodeUsage
			output.Hintf(root.ErrWriter(), "run '%s --help' for usage", cmd.CommandPath())
		} else if logger.GetLevel() > log.DebugLevel {
			output.Hintf(root.ErrWriter(), "run with -v for more detail")
		}
		os.Exit(code)
	}
}

// configPathFromArgs extracts a --config value from raw args, before cobra
// parses them (config must load first). Returns "" when the flag is absent.
func configPathFromArgs(args []string) string {
	for i, a := range args {
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
