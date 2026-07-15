package base

import (
	"io"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
)

func TestGlobalIDFlags(t *testing.T) {
	a := cli.New(log.New(io.Discard), config.New(), "test")
	root := New(a).Command()

	for _, name := range []string{"store-id", "model-id"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected global --%s flag", name)
		}
	}
	for _, name := range []string{"store", "model"} {
		if root.PersistentFlags().Lookup(name) != nil {
			t.Errorf("legacy global --%s flag must not be registered", name)
		}
	}
}
