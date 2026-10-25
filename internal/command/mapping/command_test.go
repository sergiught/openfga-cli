package mapping

import (
	"io"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
)

func TestGroupHasItsSubcommands(t *testing.T) {
	cmd := New(cli.New(log.New(io.Discard), config.New(), "test")).Command()
	if cmd.Use != "mappings [file]" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	if len(cmd.Aliases) != 1 || cmd.Aliases[0] != "mapping" {
		t.Fatalf("Aliases = %v", cmd.Aliases)
	}
	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	if strings.Join(names, ",") != "apply,test,validate" {
		t.Fatalf("subcommands = %v", names)
	}
}
