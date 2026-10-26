package mapping

import (
	"bytes"
	"testing"

	"github.com/sergiught/openfga-cli/internal/cli"
)

// run executes a `mappings` subcommand with args against a fresh CLI, returning stderr.
func run(t *testing.T, c *cli.CLI, args ...string) (string, error) {
	t.Helper()
	root := New(c).Command()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	root.SilenceUsage = true
	root.SilenceErrors = true
	err := root.Execute()
	return errb.String() + out.String(), err
}
