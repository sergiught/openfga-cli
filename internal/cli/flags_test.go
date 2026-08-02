package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

func aliasCmd() *cobra.Command {
	var n int
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().IntVar(&n, "max-results", 0, "")
	cmd.Flags().IntVar(&n, "limit", 0, "alias")
	return cmd
}

// The two flags write one variable, so passing both let the later one win
// silently. That is a coin toss the user never asked for.
func TestRejectFlagAliasConflict(t *testing.T) {
	cmd := aliasCmd()
	if err := cmd.ParseFlags([]string{"--max-results", "10", "--limit", "5"}); err != nil {
		t.Fatal(err)
	}
	err := RejectFlagAliasConflict(cmd, "max-results", "limit")
	if err == nil {
		t.Fatal("passing both a flag and its alias should be rejected")
	}
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Errorf("exit code = %d, want usage (%d)", got, clierr.CodeUsage)
	}
}

func TestRejectFlagAliasConflictAllowsEitherAlone(t *testing.T) {
	for _, args := range [][]string{{"--max-results", "10"}, {"--limit", "5"}, nil} {
		cmd := aliasCmd()
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		if err := RejectFlagAliasConflict(cmd, "max-results", "limit"); err != nil {
			t.Errorf("ParseFlags(%v) then conflict check = %v, want nil", args, err)
		}
	}
}

func TestParseTimestamp(t *testing.T) {
	if _, err := ParseTimestamp("start-time", ""); err != nil {
		t.Errorf("an unset timestamp should be accepted: %v", err)
	}
	if got, err := ParseTimestamp("start-time", "2026-01-31T15:04:05Z"); err != nil || got != "2026-01-31T15:04:05Z" {
		t.Errorf("ParseTimestamp(valid) = %q, %v; want the value unchanged", got, err)
	}

	// "yesterday" was previously sent to the server verbatim.
	_, err := ParseTimestamp("start-time", "yesterday")
	if err == nil {
		t.Fatal("a non-RFC3339 timestamp should be rejected locally")
	}
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Errorf("exit code = %d, want usage (%d)", got, clierr.CodeUsage)
	}
	if !strings.Contains(err.Error(), "RFC 3339") {
		t.Errorf("error = %q, want it to name the expected format", err)
	}
}
