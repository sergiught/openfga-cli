package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
)

func TestRawGlobalFlagParsing(t *testing.T) {
	if !boolFlagFromArgs([]string{"stores", "list", "--no-color"}, "--no-color") {
		t.Fatal("bool flag after subcommand was not found")
	}
	if boolFlagFromArgs([]string{"--no-color=false"}, "--no-color") {
		t.Fatal("explicit false bool flag was treated as enabled")
	}
	if boolFlagFromArgs([]string{"--no-color", "--no-color=false"}, "--no-color") {
		t.Fatal("last repeated bool flag did not win")
	}
	if boolFlagFromArgs([]string{"--", "--no-color"}, "--no-color") {
		t.Fatal("flag after terminator was parsed")
	}
	if got := valueFlagFromArgs([]string{"stores", "--theme", "mono"}, "--theme"); got != "mono" {
		t.Fatalf("theme = %q", got)
	}
	if got := valueFlagFromArgs([]string{"--theme=mono", "--theme", "dark"}, "--theme"); got != "dark" {
		t.Fatalf("repeated theme = %q", got)
	}
}

func TestNoColorFromArgsOverridesForcedColor(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	if !noColorFromArgs([]string{"--no-color", "--help"}, "") {
		t.Fatal("--no-color must override FORCE_COLOR")
	}
	if noColorFromArgs([]string{"--help"}, "") {
		t.Fatal("FORCE_COLOR without an explicit override should remain enabled")
	}
	if !noColorFromArgs([]string{"--help"}, "mono") {
		t.Fatal("configured mono theme should disable terminal probing")
	}
}

func TestDebugFlagsEnableDebugLogging(t *testing.T) {
	for _, flag := range []string{"--debug", "--debug=true", "-d", "--verbose", "-v"} {
		if got := logLevel([]string{flag}); got != log.DebugLevel {
			t.Fatalf("logLevel(%q) = %v, want debug", flag, got)
		}
	}
}

// The log level is decided from the raw arguments before cobra parses them, so
// this pre-parse has to agree with pflag: grouped shorthands are a legal way to
// write -d, and a later explicit value overrides an earlier one.
func TestDebugFlagParsingMatchesPflag(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"-dq"}, true},                  // grouped with --quiet
		{[]string{"-qd"}, true},                  // ...in either order
		{[]string{"-vq"}, true},                  // the --verbose alias groups too
		{[]string{"-d", "--debug=false"}, false}, // last value wins
		{[]string{"--debug=false", "-d"}, true},  // ...in either order
		{[]string{"-d=false"}, false},            // shorthand with an explicit value
		{[]string{"-pdefault"}, false},           // --profile=default, not a d flag
		{[]string{"-p", "dev", "stores", "list"}, false},
		{[]string{"--", "-d"}, false}, // after the terminator
		{[]string{"stores", "list"}, false},
	}
	for _, tc := range cases {
		got := logLevel(tc.args) == log.DebugLevel
		if got != tc.want {
			t.Errorf("logLevel(%q) debug = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestReportCanceledPrintsPartialCommitDetail(t *testing.T) {
	var buf bytes.Buffer
	inner := fmt.Errorf("tuples 26-50 failed after 25 of 100 tuple(s) were committed: %w", context.Canceled)
	reportCanceled(&buf, clierr.WithPartialResult(inner))

	out := buf.String()
	if !strings.Contains(out, "canceled") {
		t.Errorf("output should mention canceled: %q", out)
	}
	if !strings.Contains(out, "25 of 100 tuple(s) were committed") {
		t.Errorf("output should surface the partial-commit detail: %q", out)
	}
}

func TestReportCanceledPrintsSilentSummary(t *testing.T) {
	var buf bytes.Buffer
	reportCanceled(&buf, clierr.WithPartialResult(clierr.Silent(clierr.CodeError, "wrote 2, failed 1 of 3 tuple(s)")))
	if out := buf.String(); !strings.Contains(out, "wrote 2, failed 1 of 3 tuple(s)") || strings.Contains(out, "(handled)") {
		t.Errorf("want the silent summary, got %q", out)
	}
}

func TestReportCanceledWithoutPartialResultPrintsOnlyCanceled(t *testing.T) {
	var buf bytes.Buffer
	reportCanceled(&buf, context.Canceled)

	out := buf.String()
	if !strings.Contains(out, "canceled") {
		t.Errorf("output should mention canceled: %q", out)
	}
	if strings.Contains(out, "committed") {
		t.Errorf("output should not fabricate partial-commit detail: %q", out)
	}
}

func TestMainFileRunsStandalone(t *testing.T) {
	// Generous, because this is a hang guard rather than a performance budget:
	// go run links the whole binary from a cache the -race test run never warms,
	// and on a loaded CI runner that alone can outlast a tight timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "main.go", "--help")
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run main.go --help: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "USAGE") {
		t.Fatalf("standalone help missing usage:\n%s", out)
	}
}

func TestMachineOutputFromArgs(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"stores", "list", "--json"}, true}, {[]string{"-o", "json"}, true}, {[]string{"-ojson"}, true},
		{[]string{"--output=yaml"}, true}, {[]string{"-o=json"}, true}, {[]string{"--jq", ".a"}, true},
		{[]string{"--jq=.a"}, true}, {[]string{"-o", "jsonl"}, true}, {[]string{"-o", "table"}, false}, {[]string{"--json", "--plain"}, false},
		{[]string{"--", "--json"}, false}, {[]string{"stores", "list"}, false}, {nil, false},
		// The last -o wins, as it does for pflag.
		{[]string{"-o", "table", "-o", "json"}, true}, {[]string{"-o", "json", "--output=table"}, false},
		{[]string{"--plain", "-o", "json"}, true},
		// -o inside a shorthand cluster, but not inside another flag's value.
		{[]string{"-qojson"}, true}, {[]string{"-qo", "json"}, true}, {[]string{"-pojson"}, false},
		// A bare --jq whose value is missing still asked for machine output.
		{[]string{"stores", "list", "--jq"}, true},
		// A value a flag consumes is not itself a flag.
		{[]string{"stores", "list", "--jq", "-otable", "--json"}, true},
		{[]string{"stores", "list", "--profile", "-ojson"}, false},
		// Shorthand arity comes from the command: -f is --force here.
		{[]string{"mappings", "apply", "-fojson", "--bogus"}, true},
		// Unknown flags and commands still leave the output flags readable.
		{[]string{"stores", "list", "--bogus", "-o", "json"}, true},
		{[]string{"stors", "--json"}, true},
	}
	root := scanRoot()
	for _, tt := range tests {
		if got := machineOutputFromArgs(root, tt.args, false); got != tt.want {
			t.Errorf("machineOutputFromArgs(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
	// Agent mode makes machine output the default; --plain and -o table still win.
	agentTests := []struct {
		args []string
		want bool
	}{
		{[]string{"stores", "list"}, true}, {[]string{"--plain"}, false}, {[]string{"-o", "table"}, false},
	}
	for _, tt := range agentTests {
		if got := machineOutputFromArgs(root, tt.args, true); got != tt.want {
			t.Errorf("machineOutputFromArgs(%q, agent) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestAgentFlagFromArgs(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		args []string
		want *bool
	}{
		{[]string{"stores", "list"}, nil},
		{[]string{"--agent"}, &yes},
		{[]string{"--agent=false"}, &no},
		{[]string{"--agent", "--agent=false"}, &no},
		{[]string{"--agent", "--"}, &yes},
		{[]string{"--", "--agent"}, nil},
		{[]string{"stores", "list", "--agent=bogus"}, nil},
		// A value another flag consumes is not the --agent flag.
		{[]string{"--profile", "--agent=false", "nosuchcmd"}, nil},
		{[]string{"stores", "list", "--jq", "--agent"}, nil},
		// Unknown commands and flags still leave --agent readable.
		{[]string{"stors", "--bogus", "--agent=false"}, &no},
	}
	root := scanRoot()
	for _, tt := range tests {
		got := agentFlagFromArgs(root, tt.args)
		if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
			t.Errorf("agentFlagFromArgs(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestConfigLoadHintNamesTheFileThatFailed(t *testing.T) {
	if got := configLoadHint([]string{"stores", "list", "--config", "/elsewhere/ofga.toml"}); got != "fix or remove /elsewhere/ofga.toml, then try again" {
		t.Errorf("explicit --config hint = %q", got)
	}
	if got := configLoadHint([]string{"--config=/elsewhere/ofga.toml"}); !strings.Contains(got, "/elsewhere/ofga.toml") {
		t.Errorf("explicit --config= hint = %q", got)
	}
	if path := config.DefaultPath(); path != "" {
		if got := configLoadHint(nil); got != "fix or remove "+path+", then try again" {
			t.Errorf("default hint = %q", got)
		}
	}
}

func TestReportMachineErrorCanceledKeepsSilentSummary(t *testing.T) {
	err := clierr.WithPartialResult(clierr.Silent(clierr.CodeError, "wrote 2, failed 1 of 3 tuple(s)"))
	var buf bytes.Buffer
	reportMachineError(&buf, err, clierr.CodeCanceled, "ofga tuples write", true)
	if !strings.Contains(buf.String(), `"message":"canceled: wrote 2, failed 1 of 3 tuple(s)"`) {
		t.Fatalf("partial-result summary lost: %s", buf.String())
	}

	buf.Reset()
	reportMachineError(&buf, clierr.WithPartialResult(errors.New("wrote 1 of 2")), clierr.CodeCanceled, "ofga tuples write", true)
	if !strings.Contains(buf.String(), `"message":"canceled: wrote 1 of 2"`) {
		t.Fatalf("plain partial result lost: %s", buf.String())
	}
}

func TestReportMachineErrorKeepsExplicitKind(t *testing.T) {
	explicit := clierr.WithCode(clierr.CodeUsage, clierr.WithKind(clierr.KindValidation, errors.New("x")))
	var buf bytes.Buffer
	reportMachineError(&buf, explicit, clierr.CodeUsage, "ofga query batch-check", false)
	if !strings.Contains(buf.String(), `"code":"validation"`) {
		t.Fatalf("explicit kind clobbered: %s", buf.String())
	}

	buf.Reset()
	reportMachineError(&buf, clierr.WithCode(clierr.CodeUsage, errors.New("x")), clierr.CodeUsage, "ofga query batch-check", false)
	if !strings.Contains(buf.String(), `"code":"usage"`) {
		t.Fatalf("plain usage error not usage: %s", buf.String())
	}
}
