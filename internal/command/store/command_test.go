package store

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

func TestListRejectsNegativeMaxBeforeClientCreation(t *testing.T) {
	cmd := (&Command{}).listCmd()
	cmd.SetArgs([]string{"--max-results=-1"})
	if err := cmd.Execute(); clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("exit code = %d, want usage; err=%v", clierr.Code(err), err)
	}
}

func TestStoreDryRunShorthand(t *testing.T) {
	c := &Command{}
	for _, cmd := range []*cobra.Command{c.createCmd(), c.deleteCmd()} {
		if got := cmd.Flags().Lookup("dry-run").Shorthand; got != "n" {
			t.Errorf("%s --dry-run shorthand = %q, want n", cmd.Name(), got)
		}
	}
}
