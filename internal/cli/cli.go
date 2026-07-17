// Package cli holds the shared dependencies threaded through every command:
// the logger, the loaded config, and the global flag overrides. It is the
// single place commands go to obtain a ready-to-use OpenFGA client.
package cli

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"charm.land/log/v2"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/client"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
)

// CLI is the dependency container shared across commands.
type CLI struct {
	Logger    *log.Logger
	Config    *config.Config
	Overrides config.Overrides // populated from persistent flags before Execute

	// Output is the -o/--output mode (json|yaml|plain|table); it is the
	// primary form, with --json/--yaml/--plain kept as aliases that set the
	// corresponding toggles directly.
	Output string
	// JSON toggles machine-readable output across commands.
	JSON bool
	// YAML toggles machine-readable YAML output across commands.
	YAML bool
	// Quiet suppresses incidental success/info output.
	Quiet bool
	// NoInput prevents prompts and the interactive TUI.
	NoInput bool
	// Plain renders unstyled, tab-separated tables.
	Plain bool
	// NoColor disables color regardless of terminal/theme.
	NoColor bool
	// ThemeName, when set via --theme, overrides the configured theme.
	ThemeName string
	// RequestTimeout bounds each HTTP exchange; zero disables the deadline.
	RequestTimeout time.Duration
	// Runtime secret files provide process-scoped credentials without exposing
	// their contents in argv or environment variables.
	APITokenFile     string
	ClientSecretFile string
	PrivateKeyFile   string

	// Version is the build version, injected from main.
	Version string
}

// New builds a CLI with the given logger, config and version.
func New(logger *log.Logger, cfg *config.Config, version string) *CLI {
	return &CLI{
		Logger:         logger,
		Config:         cfg,
		Version:        version,
		RequestTimeout: client.DefaultRequestTimeout,
	}
}

// Resolve merges profile, env and flag overrides into a usable configuration.
func (cli *CLI) Resolve() (config.Resolved, error) {
	r, err := cli.Config.Resolve(cli.Overrides)
	if err == nil {
		emitNotices(r.Notices)
	}
	return r, err
}

// noticeOnce guards resolution advisories so they print at most once per
// process, even though Resolve runs for nearly every command (often twice).
var noticeOnce sync.Once

// emitNotices writes resolution advisories to stderr — never stdout, so machine
// output (e.g. --json) stays clean — and only for the first resolution that
// produced any.
func emitNotices(notices []string) {
	if len(notices) == 0 {
		return
	}
	noticeOnce.Do(func() {
		for _, n := range notices {
			fmt.Fprintln(os.Stderr, n)
		}
	})
}

// Client returns a configured OpenFGA client for the resolved configuration.
func (cli *CLI) Client() (*openfga.Client, error) {
	r, err := cli.Resolve()
	if err != nil {
		return nil, err
	}
	return client.New(r, client.WithTimeout(cli.RequestTimeout))
}

// ClientWithStore returns a client and guarantees a store ID is configured,
// returning a friendly error otherwise. Most commands need a store.
func (cli *CLI) ClientWithStore() (*openfga.Client, config.Resolved, error) {
	r, err := cli.Resolve()
	if err != nil {
		return nil, config.Resolved{}, err
	}
	if r.StoreID == "" {
		return nil, r, clierr.WithCode(clierr.CodeUsage, errors.New("no store selected: pass --store-id, set OPENFGA_STORE_ID, or run `ofga profiles set store_id <id>`"))
	}
	c, err := client.New(r, client.WithTimeout(cli.RequestTimeout))
	if err != nil {
		return nil, r, err
	}
	return c, r, nil
}

// SaveConfig persists the config and logs the location at debug level.
func (cli *CLI) SaveConfig() error {
	if err := cli.Config.Save(); err != nil {
		if config.SaveWasCommitted(err) {
			cli.Logger.Warn("config replaced, but its directory could not be synced; the change may not survive a system crash", "path", cli.Config.Path(), "error", err)
			return nil
		}
		return fmt.Errorf("save config: %w", err)
	}
	cli.Logger.Debug("config saved", "path", cli.Config.Path())
	return nil
}

// SaveConfigWithSecretCleanup persists a profile removal/unset and deletes its
// keyring entries under the same cross-process config lock.
func (cli *CLI) SaveConfigWithSecretCleanup(profile string, all bool, fields ...string) (bool, error) {
	saved, err := cli.Config.SaveWithSecretCleanup(profile, all, fields...)
	if err != nil {
		if saved {
			return true, fmt.Errorf("config saved, but credential cleanup failed: %w; retry safely with `ofga profiles cleanup-credentials`", err)
		}
		return saved, fmt.Errorf("save config: %w", err)
	}
	cli.Logger.Debug("config saved", "path", cli.Config.Path())
	return true, nil
}

// RetrySecretCleanup retries cleanup work durably recorded in the config.
func (cli *CLI) RetrySecretCleanup() (int, error) {
	remaining, err := cli.Config.RetryCredentialCleanup()
	if err != nil {
		return remaining, fmt.Errorf("credential cleanup failed: %w; retry with `ofga profiles cleanup-credentials`", err)
	}
	cli.Logger.Debug("credential cleanup completed", "path", cli.Config.Path())
	return remaining, nil
}
