// Command ofga is a modern CLI and TUI for OpenFGA.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"

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

// ignoreBrokenPipeSignal makes writes to a closed stdout pipe return EPIPE on
// Unix instead of terminating the process. Keep this in main.go so the
// documented `go run cmd/ofga/main.go` invocation compiles that file alone.
func ignoreBrokenPipeSignal() {
	switch runtime.GOOS {
	case "windows", "plan9", "js", "wasip1":
		return
	default:
		signal.Ignore(syscall.Signal(13)) // SIGPIPE on Unix.
	}
}

func main() {
	ignoreBrokenPipeSignal()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Config loads before cobra parses flags, so --config is read from argv here
	// (env OPENFGA_CONFIG is honored by LoadFrom when no flag is given).
	noColor := noColorFromArgs(os.Args[1:], "")
	cfg, err := config.LoadFrom(configPathFromArgs(os.Args[1:]))
	if err != nil {
		// base.New (which builds the profile-aware writers) hasn't run yet, so
		// wrap stderr here too — otherwise this early error leaks ANSI to a pipe
		// and ignores NO_COLOR.
		errw := profileWriter(os.Stderr, noColor)
		output.Errorf(errw, "%s", err.Error())
		if path := config.DefaultPath(); path != "" {
			output.Hintf(errw, "fix or remove %s, then try again", path)
		}
		os.Exit(clierr.CodeError)
	}
	noColor = noColor || cfg.Theme == "mono"

	// charmbracelet/log probes terminal foreground/background colors when its
	// writer is a TTY. Wrap stderr before constructing it in no-color modes so
	// even `--no-color --help` cannot emit OSC/CSI terminal queries.
	var logWriter io.Writer = os.Stderr
	if noColor {
		logWriter = profileWriter(os.Stderr, true)
	}
	logger := log.NewWithOptions(logWriter, log.Options{
		ReportTimestamp: false,
		Level:           logLevel(os.Args[1:]),
		Prefix:          "ofga",
	})
	icons.Apply(icons.Parse(cfg.IconsMode()))

	c := cli.New(logger, cfg, version.Resolved())
	c.NoColor = boolFlagFromArgs(os.Args[1:], "--no-color")
	c.Plain = boolFlagFromArgs(os.Args[1:], "--plain")
	c.ThemeName = valueFlagFromArgs(os.Args[1:], "--theme")

	root := base.New(c)
	rootCmd := root.Command()
	rootCmd.SetContext(ctx)
	cmd, err := rootCmd.ExecuteC()
	if err != nil {
		if clierr.IsIgnorableBrokenPipe(err) {
			return
		}
		// pflag swallows the token after an unknown long flag as its value, so a
		// mistyped global flag before a subcommand path (`ofga --debu stores list`)
		// surfaces as a misleading "unknown command". If an unrecognized flag
		// actually preceded it, report that instead.
		if strings.HasPrefix(err.Error(), "unknown command") {
			if flag := base.FirstUnknownFlag(rootCmd, os.Args[1:]); flag != "" {
				err = clierr.WithCode(clierr.CodeUsage, fmt.Errorf("unknown flag: %s", flag))
			}
		}
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
			// A deliberate abort has nothing more to show under debug logging;
			// only hint for
			// genuine failures where extra detail could help.
			output.Hintf(root.ErrWriter(), "run with -d/--debug for more detail")
		}
		os.Exit(code)
	}
}

func profileWriter(w io.Writer, noColor bool) *colorprofile.Writer {
	if noColor {
		return &colorprofile.Writer{Forward: w, Profile: colorprofile.NoTTY}
	}
	pw := colorprofile.NewWriter(w, os.Environ())
	if base.ForceColor() {
		// colorprofile's writers only honor CLICOLOR_FORCE; upgrade so FORCE_COLOR
		// forces color on this early pre-cobra path too, matching base.New.
		pw.Profile = colorprofile.TrueColor
	}
	return pw
}

func noColorFromArgs(args []string, configuredTheme string) bool {
	if boolFlagFromArgs(args, "--no-color") || boolFlagFromArgs(args, "--plain") ||
		valueFlagFromArgs(args, "--theme") == "mono" || configuredTheme == "mono" {
		return true
	}
	noColorEnv := os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"
	return base.ForceMono() || (noColorEnv && !base.ForceColor())
}

func boolFlagFromArgs(args []string, name string) bool {
	enabled := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == name {
			enabled = true
			continue
		}
		if value, ok := strings.CutPrefix(arg, name+"="); ok {
			if parsed, err := strconv.ParseBool(value); err == nil {
				enabled = parsed
			}
		}
	}
	return enabled
}

func valueFlagFromArgs(args []string, name string) string {
	var value string
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if arg == name && i+1 < len(args) {
			value = args[i+1]
			continue
		}
		if parsed, ok := strings.CutPrefix(arg, name+"="); ok {
			value = parsed
		}
	}
	return value
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

// logLevel raises verbosity when the preferred debug flag or its legacy
// verbose alias is present.
func logLevel(args []string) log.Level {
	if boolFlagFromArgs(args, "--debug") || boolFlagFromArgs(args, "--verbose") ||
		slices.Contains(args, "-d") || slices.Contains(args, "-v") {
		return log.DebugLevel
	}
	return log.WarnLevel
}
