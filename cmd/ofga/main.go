// Command ofga is a modern CLI and TUI for OpenFGA.
package main

import (
	"context"
	"os"
	"os/signal"
	"slices"

	"github.com/charmbracelet/log"

	"github.com/sergiught/openfga-cli/internal/app"
	"github.com/sergiught/openfga-cli/internal/command/base"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/ui/icons"
)

// version is overridden at build time via -ldflags "-X main.version=v1.2.3".
var version = "dev"

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
		logger.Fatal("failed to load config", "error", err)
	}
	icons.Apply(icons.Parse(cfg.IconsMode()))

	a := app.New(logger, cfg, version)

	root := base.New(a)
	if err := root.Command().ExecuteContext(ctx); err != nil {
		logger.Debugf("command failed: %+v", err)
		output.Errorf(root.ErrWriter(), "%s", err.Error())
		os.Exit(1)
	}
}

// logLevel raises verbosity when --verbose or -v is present.
func logLevel(args []string) log.Level {
	if slices.Contains(args, "--verbose") || slices.Contains(args, "-v") {
		return log.DebugLevel
	}
	return log.WarnLevel
}
