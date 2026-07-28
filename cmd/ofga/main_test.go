package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/clierr"
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
