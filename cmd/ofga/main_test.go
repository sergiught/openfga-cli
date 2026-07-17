package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"charm.land/log/v2"
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

func TestMainFileRunsStandalone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
