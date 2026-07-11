// Package app holds the shared dependencies threaded through every command:
// the logger, the loaded config, and the global flag overrides. It is the
// single place commands go to obtain a ready-to-use OpenFGA client.
package app

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/client"
	"github.com/sergiught/openfga-cli/internal/config"
)

// App is the dependency container shared across commands.
type App struct {
	Logger    *log.Logger
	Config    *config.Config
	Overrides config.Overrides // populated from persistent flags before Execute

	// JSON toggles machine-readable output across commands.
	JSON bool
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

// New builds an App with the given logger, config and version.
func New(logger *log.Logger, cfg *config.Config, version string) *App {
	return &App{Logger: logger, Config: cfg, Version: version}
}

// Resolve merges profile, env and flag overrides into a usable configuration.
func (a *App) Resolve() (config.Resolved, error) {
	return a.Config.Resolve(a.Overrides)
}

// Client returns a configured OpenFGA client for the resolved configuration.
func (a *App) Client() (*openfga.Client, error) {
	r, err := a.Resolve()
	if err != nil {
		return nil, err
	}
	return client.New(r)
}

// ClientWithStore returns a client and guarantees a store ID is configured,
// returning a friendly error otherwise. Most commands need a store.
func (a *App) ClientWithStore() (*openfga.Client, config.Resolved, error) {
	r, err := a.Resolve()
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
func (a *App) SaveConfig() error {
	if err := a.Config.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	a.Logger.Debug("config saved", "path", a.Config.Path())
	return nil
}
