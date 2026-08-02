// Package cli holds the shared dependencies threaded through every command:
// the logger, the loaded config, and the global flag overrides. It is the
// single place commands go to obtain a ready-to-use OpenFGA client.
package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sync"
	"time"

	"charm.land/log/v2"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/apilog"
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
	// TraceWriter receives the --debug request trace. Nil means os.Stderr; it
	// exists so tests (and any future --debug-file) can capture the trace
	// without touching the process's stderr.
	TraceWriter io.Writer
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

// tracing reports whether -d/--debug asked for a request trace.
func (cli *CLI) tracing() bool {
	return cli.Logger != nil && cli.Logger.GetLevel() <= log.DebugLevel
}

// clientOptions returns the options every command's client is built with. Under
// --debug it adds a TraceWriter so each API exchange is printed to stderr as it
// completes, rather than the command failing with only a one-line Go error to
// go on.
func (cli *CLI) clientOptions() []client.Option {
	opts := []client.Option{client.WithTimeout(cli.RequestTimeout)}
	if cli.tracing() {
		w := cli.TraceWriter
		if w == nil {
			w = os.Stderr
		}
		opts = append(opts, client.WithCapture(apilog.NewTraceWriter(w)))
	}
	return opts
}

// traceResolved prints the configuration a command actually resolved, so a
// "wrong store" or "wrong profile" bug is visible without guessing which of the
// flag, environment and profile layers won. Secrets are never printed — only
// which auth method was selected.
func (cli *CLI) traceResolved(r config.Resolved) {
	if !cli.tracing() {
		return
	}
	method := r.Auth.Method
	if method == "" {
		method = config.AuthNone
	}
	cli.Logger.Debug("resolved configuration",
		"profile", r.Profile,
		"api_url", redactedURL(r.APIURL),
		"store_id", orUnset(r.StoreID),
		"model_id", orUnset(r.ModelID),
		"auth", method,
		"timeout", cli.RequestTimeout,
	)
}

func orUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

// redactedURL masks a password embedded in an API URL
// (http://user:pass@host), which url.URL.String — and so plain logging of the
// resolved value — would otherwise print in full.
func redactedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, hasPassword := u.User.Password(); !hasPassword {
		return raw
	}
	return u.Redacted()
}

// Client returns a configured OpenFGA client for the resolved configuration.
func (cli *CLI) Client() (*openfga.Client, error) {
	r, err := cli.Resolve()
	if err != nil {
		return nil, err
	}
	cli.traceResolved(r)
	return client.New(r, cli.clientOptions()...)
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
	cli.traceResolved(r)
	c, err := client.New(r, cli.clientOptions()...)
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
