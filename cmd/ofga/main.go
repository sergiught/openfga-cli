// Command ofga is a modern CLI and TUI for OpenFGA.
package main

import (
	"context"
	"os"
	"os/signal"
	"slices"

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

	cfg, err := config.Load()
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

// logLevel raises verbosity when --verbose or -v is present.
func logLevel(args []string) log.Level {
	if slices.Contains(args, "--verbose") || slices.Contains(args, "-v") {
		return log.DebugLevel
	}
	return log.WarnLevel
}
