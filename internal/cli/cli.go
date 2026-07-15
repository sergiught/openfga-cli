// Package cli holds the shared dependencies threaded through every command:
// the logger, the loaded config, and the global flag overrides. It is the
// single place commands go to obtain a ready-to-use OpenFGA client.
package cli

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/client"
	"github.com/sergiught/openfga-cli/internal/config"
)

// CLI is the dependency container shared across commands.
type CLI struct {
	Logger    *log.Logger
	Config    *config.Config
	Overrides config.Overrides // populated from persistent flags before Execute

	// Output is the -o/--output mode (json|yaml|plain|table); it is the
	// primary form, with --json/--plain kept as aliases that set JSON/Plain
	// directly.
	Output string
	// JSON toggles machine-readable output across commands.
	JSON bool
	// YAML toggles machine-readable YAML output across commands (-o yaml).
	// It parallels JSON: any command that supports --json also supports YAML
	// via output.Emit, without a separate --yaml boolean flag.
	YAML bool
	// Quiet suppresses incidental success/info output.
	Quiet bool
	// Plain renders unstyled, tab-separated tables.
	Plain bool
	// NoColor disables color regardless of terminal/theme.
	NoColor bool
	// ThemeName, when set via --theme, overrides the configured theme.
	ThemeName string

	// Version is the build version, injected from main.
	Version string
}

// New builds a CLI with the given logger, config and version.
func New(logger *log.Logger, cfg *config.Config, version string) *CLI {
	return &CLI{Logger: logger, Config: cfg, Version: version}
}

// Resolve merges profile, env and flag overrides into a usable configuration.
func (cli *CLI) Resolve() (config.Resolved, error) {
	return cli.Config.Resolve(cli.Overrides)
}

// Client returns a configured OpenFGA client for the resolved configuration.
func (cli *CLI) Client() (*openfga.Client, error) {
	r, err := cli.Resolve()
	if err != nil {
		return nil, err
	}
	return client.New(r)
}

// ClientWithStore returns a client and guarantees a store ID is configured,
// returning a friendly error otherwise. Most commands need a store.
func (cli *CLI) ClientWithStore() (*openfga.Client, config.Resolved, error) {
	r, err := cli.Resolve()
	if err != nil {
		return nil, config.Resolved{}, err
	}
	if r.StoreID == "" {
		return nil, r, errors.New("no store selected: pass --store, set OPENFGA_STORE_ID, or run `ofga profiles set store_id <id>`")
	}
	c, err := client.New(r)
	if err != nil {
		return nil, r, err
	}
	return c, r, nil
}

// SaveConfig persists the config and logs the location at debug level.
func (cli *CLI) SaveConfig() error {
	if err := cli.Config.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	cli.Logger.Debug("config saved", "path", cli.Config.Path())
	return nil
}
