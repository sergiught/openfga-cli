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

	"charm.land/log/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sergiught/openfga-cli/internal/agentmode"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Config loads before cobra parses flags, so --config is read from argv here
	// (env OPENFGA_CONFIG is honored by LoadFrom when no flag is given).
	scan := scanRoot()
	agent := agentmode.Resolve(agentFlagFromArgs(scan, os.Args[1:]), os.Getenv)
	noColor := agent.Enabled || noColorFromArgs(os.Args[1:], "")
	cfg, err := config.LoadFrom(configPathFromArgs(scan, os.Args[1:]))
	if err != nil {
		hint := configLoadHint(scan, os.Args[1:])
		if machineOutputFromArgs(scan, os.Args[1:], agent.Enabled) {
			reportMachineError(os.Stderr, clierr.WithHint(hint, err), clierr.CodeError, "ofga", false)
			os.Exit(clierr.CodeError)
		}
		// base.New (which builds the profile-aware writers) hasn't run yet, so
		// wrap stderr here too — otherwise this early error leaks ANSI to a pipe
		// and ignores NO_COLOR.
		errw := profileWriter(os.Stderr, noColor)
		output.Errorf(errw, "%s", err.Error())
		if hint != "" {
			output.Hintf(errw, "%s", hint)
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
	// Agent mode is applied again in PersistentPreRunE, but --help exits before
	// that runs, so the construction-time writers have to know it already.
	c.NoColor = agent.Enabled || boolFlagFromArgs(os.Args[1:], "--no-color")
	c.Agent = agent
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
		machine := machineOutputFromArgs(rootCmd, os.Args[1:], agent.Enabled)
		if root.OutputResolved() {
			machine = c.JSON || c.YAML
		}
		if machine {
			if lerr := cfg.LoadErr(); lerr != nil && errors.Is(err, lerr) {
				err = clierr.WithHint("fix or remove "+cfg.Path()+", then try again", err)
			}
			code := clierr.Code(err)
			canceled := ctx.Err() != nil
			if canceled {
				code = clierr.CodeCanceled
			} else if !root.RanCommand() {
				code = clierr.CodeUsage
			}
			reportMachineError(os.Stderr, err, code, cmd.CommandPath(), canceled)
			os.Exit(code)
		}
		if ctx.Err() != nil {
			// A signal (Ctrl-C or SIGTERM) cancelled the request context.
			// signal.NotifyContext cancels with a signalError cause rather than
			// context.Canceled, so the wrapped request error would otherwise be
			// misread as a network failure. The context's own Err() is
			// context.Canceled regardless of cause.
			reportCanceled(root.ErrWriter(), err)
			os.Exit(clierr.CodeCanceled)
		}
		if clierr.IsSilent(err) {
			// The command already printed its own human summary (e.g. model
			// test's "N/Total test(s) failed" line); honor the exit code without
			// printing a second, redundant message or hint.
			os.Exit(clierr.Code(err))
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

// reportCanceled prints the "canceled" message for a Ctrl-C/SIGTERM
// interrupted command. When err carries a clierr.PartialResult (a bulk write
// stopped partway through a batch), its "N of M committed" detail is also
// printed so the user still learns what landed instead of losing that report
// to the cancellation.
func reportCanceled(w io.Writer, err error) {
	output.Errorf(w, "canceled")
	if report := partialReport(err); report != "" {
		output.Hintf(w, "%s", report)
	}
}

// partialReport returns the "N of M committed" detail a clierr.PartialResult
// carries, or "" without one. A Silent error inside it reports its summary,
// since its own message is only a placeholder.
func partialReport(err error) string {
	var partial *clierr.PartialResult
	if !errors.As(err, &partial) {
		return ""
	}
	if s := clierr.SilentSummary(partial); s != "" {
		return s
	}
	return partial.Error()
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

// valueShorthands are the single-letter flags that consume a value, so a
// cluster like "-pdev" is --profile=dev rather than the flags p, d, e and v.
// Anything else in a cluster is treated as a boolean flag, which is what makes
// "-dq" read as --debug --quiet.
const valueShorthands = "pof"

// boolFlagFromArgs reports the final value of a boolean flag as pflag would
// read it, scanning the raw arguments before cobra parses them. shorthands are
// the flag's single-letter forms.
//
// It has to match pflag rather than merely look for the flag, because these
// values decide the log level and color mode before any command runs: a
// grouped "-dq" is a legal way to write --debug --quiet, and a later
// --debug=false must override an earlier -d rather than being ignored.
func boolFlagFromArgs(args []string, name string, shorthands ...byte) bool {
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
			continue
		}
		if len(shorthands) == 0 || len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
			continue
		}
		// A shorthand cluster: "-dq", or "-d=false" for a single one.
		cluster, value, hasValue := strings.Cut(arg[1:], "=")
		for i := range len(cluster) {
			c := cluster[i]
			if slices.Contains(shorthands, c) {
				enabled = true
				if hasValue && i == len(cluster)-1 {
					if parsed, err := strconv.ParseBool(value); err == nil {
						enabled = parsed
					}
				}
				continue
			}
			if strings.IndexByte(valueShorthands, c) >= 0 {
				break // the rest of the cluster is this flag's value
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
// parses them (config must load first). It parses against root's flag arity, so
// a value another flag consumes is not mistaken for --config, and the last
// --config wins, as in pflag. Returns "" when the flag is absent.
func configPathFromArgs(root *cobra.Command, args []string) string {
	fs, _ := parseArgsAgainst(root, args)
	f := fs.Lookup("config")
	if f == nil || !f.Changed {
		return ""
	}
	return f.Value.String()
}

// configLoadHint is the remediation for a config that failed to load, naming
// the file that failed: the --config override when argv has one, else the
// default location. Returns "" when neither resolves.
func configLoadHint(root *cobra.Command, args []string) string {
	path := configPathFromArgs(root, args)
	if path == "" {
		path = config.DefaultPath()
	}
	if path == "" {
		return ""
	}
	return "fix or remove " + path + ", then try again"
}

// logLevel raises verbosity when the preferred debug flag or its legacy
// verbose alias is present.
func logLevel(args []string) log.Level {
	if boolFlagFromArgs(args, "--debug", 'd') || boolFlagFromArgs(args, "--verbose", 'v') {
		return log.DebugLevel
	}
	return log.WarnLevel
}

// machineOutputFromArgs reports whether raw argv selects machine output
// (--json, --yaml, --jq, or -o/--output json|jsonl|yaml). It is the fallback for
// failures that happen before cobra has resolved the output flags: a mistyped
// command, an unknown flag, a config that will not load. The last -o wins and
// is authoritative over --plain, as it is in resolveOutput. With agent set,
// machine output is the default when none of those is given.
//
// argv is read through parseArgsAgainst, so in `--jq -otable` the -otable is
// the jq expression.
func machineOutputFromArgs(root *cobra.Command, args []string, agent bool) bool {
	fs, err := parseArgsAgainst(root, args)
	value := func(name string) string {
		if f := fs.Lookup(name); f != nil && f.Changed {
			return f.Value.String()
		}
		return ""
	}
	on := func(name string) bool {
		v, _ := strconv.ParseBool(value(name))
		return v
	}
	switch value("output") {
	case "json", "jsonl", "yaml":
		return true
	case "plain", "table":
		return false
	}
	if on("plain") {
		return false
	}
	// A bare --jq with no value still asked for machine output.
	var missing *pflag.ValueRequiredError
	jq := fs.Changed("jq") || errors.As(err, &missing) && missing.GetFlag().Name == "jq"
	return agent || jq || on("json") || on("yaml")
}

// agentFlagFromArgs returns the final --agent value in raw argv, or nil when
// the flag is not given (or its value is not a bool). Like
// machineOutputFromArgs, it skips a value another flag consumes: in
// `--profile --agent=false` the --agent=false is the profile name.
func agentFlagFromArgs(root *cobra.Command, args []string) *bool {
	fs, _ := parseArgsAgainst(root, args)
	f := fs.Lookup("agent")
	if f == nil || !f.Changed {
		return nil
	}
	v, err := strconv.ParseBool(f.Value.String())
	if err != nil {
		return nil
	}
	return &v
}

// parseArgsAgainst parses raw argv against the flags of the command it names
// (root when it names none), so a flag takes a value exactly when cobra's
// would: `mappings apply -fojson` is --force -o json. Unknown flags are
// skipped, so the known ones stay readable.
func parseArgsAgainst(root *cobra.Command, args []string) (*pflag.FlagSet, error) {
	cmd := root
	if found, _, err := root.Find(args); err == nil {
		cmd = found
	}
	cmd.InitDefaultHelpFlag()
	// Each flag is mirrored as a string of the same arity, so parsing argv here
	// never runs the real flags' setters.
	fs := pflag.NewFlagSet("", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	mirror := func(f *pflag.Flag) {
		if fs.Lookup(f.Name) == nil {
			fs.StringP(f.Name, f.Shorthand, "", "")
			fs.Lookup(f.Name).NoOptDefVal = f.NoOptDefVal
		}
	}
	cmd.LocalFlags().VisitAll(mirror)
	cmd.InheritedFlags().VisitAll(mirror)
	return fs, fs.Parse(args)
}

// scanRoot builds a command tree only to read flag arity from, for a failure
// that happens before main has built the real one.
func scanRoot() *cobra.Command {
	return base.New(cli.New(log.New(io.Discard), config.New(), version.Resolved())).Command()
}

// reportMachineError writes err to w as the one-line JSON error envelope.
// code is the process exit code main computed; cmdPath names the command for
// the default usage hint.
func reportMachineError(w io.Writer, err error, code int, cmdPath string, canceled bool) {
	env := clierr.NewEnvelope(err)
	body := &env.Error
	switch {
	case canceled:
		body.Code = clierr.KindCanceled
		body.Message = "canceled"
		if report := partialReport(err); report != "" {
			body.Message = "canceled: " + report
		}
	case code == clierr.CodeUsage:
		if !clierr.HasKind(err) {
			body.Code = clierr.KindUsage
		}
		if body.Hint == "" {
			body.Hint = "run '" + cmdPath + " --help' for usage"
		}
	}
	_ = env.WriteLine(w)
}
