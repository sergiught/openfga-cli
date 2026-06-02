package base

import (
	"io"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/sergiught/openfga-cli/internal/app"
	"github.com/sergiught/openfga-cli/internal/config"
)

func TestPlaygroundSubcommandRemoved(t *testing.T) {
	a := app.New(log.New(io.Discard), config.New(), "test")
	root := New(a).Command()
	for _, c := range root.Commands() {
		if c.Name() == "playground" {
			t.Fatal("playground subcommand should have been removed; bare `ofga` launches the TUI")
		}
	}
}
