package base

import (
	"io"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
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

// --jq only takes effect on the structured path, so every unstructured mode has
// to be rejected rather than silently ignoring the filter. -o plain was missed
// once: the command printed its table and the filter never ran.
func TestResolveOutputRejectsJQWithUnstructuredModes(t *testing.T) {
	defer func(j string) { output.JQ = j }(output.JQ)

	for _, tt := range []struct {
		name   string
		output string
		plain  bool
	}{
		{"--plain", "", true},
		{"-o plain", "plain", false},
		{"-o table", "table", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output.JQ = ".[].id"
			a := cli.New(log.New(io.Discard), config.New(), "test")
			a.Output, a.Plain = tt.output, tt.plain
			c := &Command{cli: a}
			err := c.resolveOutput()
			if err == nil {
				t.Fatalf("--jq with %s should be rejected, got JSON:%v Plain:%v", tt.name, a.JSON, a.Plain)
			}
			if !strings.Contains(err.Error(), "--jq needs structured output") {
				t.Fatalf("error = %v, want the structured-output message", err)
			}
		})
	}
}

// --jq on its own selects JSON, and leaves an explicit --yaml alone.
func TestResolveOutputJQImpliesJSON(t *testing.T) {
	defer func(j string) { output.JQ = j }(output.JQ)
	output.JQ = ".[].id"

	a := cli.New(log.New(io.Discard), config.New(), "test")
	c := &Command{cli: a}
	if err := c.resolveOutput(); err != nil {
		t.Fatal(err)
	}
	if !a.JSON {
		t.Fatalf("--jq should imply JSON, got JSON:%v YAML:%v Plain:%v", a.JSON, a.YAML, a.Plain)
	}

	b := cli.New(log.New(io.Discard), config.New(), "test")
	b.YAML = true
	c2 := &Command{cli: b}
	if err := c2.resolveOutput(); err != nil {
		t.Fatal(err)
	}
	if !b.YAML || b.JSON {
		t.Fatalf("--jq --yaml should stay YAML, got JSON:%v YAML:%v", b.JSON, b.YAML)
	}
}

// An invalid filter must be rejected before any request is issued.
func TestResolveOutputRejectsInvalidJQ(t *testing.T) {
	defer func(j string) { output.JQ = j }(output.JQ)
	output.JQ = ".["

	c := &Command{cli: cli.New(log.New(io.Discard), config.New(), "test")}
	if err := c.resolveOutput(); err == nil {
		t.Fatal("an invalid --jq filter should be rejected")
	}
}
