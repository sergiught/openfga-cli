package main

import "testing"

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
