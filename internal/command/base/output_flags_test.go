package base

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
)

func TestOutputAliasFlags(t *testing.T) {
	a := cli.New(log.New(io.Discard), config.New(), "test")
	root := New(a).Command()

	for _, name := range []string{"json", "yaml", "plain", "no-input", "timeout", "debug", "verbose"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected global --%s flag", name)
		}
	}
}

func TestResolveOutputAliases(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		json       bool
		yaml       bool
		plain      bool
		wantJSON   bool
		wantYAML   bool
		wantPlain  bool
		wantErrSub string
	}{
		{name: "yaml alias", yaml: true, wantYAML: true},
		{name: "output yaml", output: "yaml", wantYAML: true},
		{name: "output overrides alias", output: "json", yaml: true, wantJSON: true},
		{name: "conflicting aliases", json: true, yaml: true, wantErrSub: "cannot combine"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := cli.New(log.New(io.Discard), config.New(), "test")
			a.Output, a.JSON, a.YAML, a.Plain = tt.output, tt.json, tt.yaml, tt.plain
			c := &Command{cli: a}
			err := c.resolveOutput()
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("resolveOutput() error = %v, want containing %q", err, tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if a.JSON != tt.wantJSON || a.YAML != tt.wantYAML || a.Plain != tt.wantPlain {
				t.Fatalf("resolved modes = JSON:%v YAML:%v Plain:%v", a.JSON, a.YAML, a.Plain)
			}
		})
	}
}
