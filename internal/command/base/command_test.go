package base

import (
	"io"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
)

func TestPlaygroundSubcommandRemoved(t *testing.T) {
	a := cli.New(log.New(io.Discard), config.New(), "test")
	root := New(a).Command()
	for _, c := range root.Commands() {
		if c.Name() == "playground" {
			t.Fatal("playground subcommand should have been removed; bare `ofga` launches the TUI")
		}
	}
}

func TestExampleLinesPreserveRelativeIndentation(t *testing.T) {
	lines := exampleLines("  ofga profiles add ci \\\n    --client-id abc \\\n    --token-url https://issuer.example")
	if len(lines) != 3 {
		t.Fatalf("exampleLines returned %d lines", len(lines))
	}
	if lines[0].text != "ofga profiles add ci \\" {
		t.Fatalf("first line = %q", lines[0].text)
	}
	if lines[1].text != "  --client-id abc \\" || lines[2].text != "  --token-url https://issuer.example" {
		t.Fatalf("continuation indentation lost: %#v", lines)
	}
}
