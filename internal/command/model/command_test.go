package model

import (
	"testing"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

func TestListRejectsNegativeMaxBeforeClientCreation(t *testing.T) {
	cmd := (&Command{}).listCmd()
	cmd.SetArgs([]string{"--limit=-1"})
	if err := cmd.Execute(); clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("exit code = %d, want usage; err=%v", clierr.Code(err), err)
	}
}

func TestModelDryRunShorthand(t *testing.T) {
	cmd := (&Command{}).writeCmd()
	if got := cmd.Flags().Lookup("dry-run").Shorthand; got != "n" {
		t.Fatalf("--dry-run shorthand = %q, want n", got)
	}
}
