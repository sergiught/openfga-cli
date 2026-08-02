package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// RejectFlagAliasConflict errors when both an alias and the flag it aliases
// were set explicitly. The pair writes one variable, so whichever appears last
// on the command line silently wins — `--max-results 10 --limit 5` reads as a
// limit of 5 with no complaint. Saying so beats guessing which one was meant.
func RejectFlagAliasConflict(cmd *cobra.Command, primary, alias string) error {
	if cmd.Flags().Changed(primary) && cmd.Flags().Changed(alias) {
		return clierr.WithCode(clierr.CodeUsage,
			fmt.Errorf("--%s and --%s are the same flag; pass only one", primary, alias))
	}
	return nil
}

// ParseTimestamp validates an RFC 3339 timestamp flag locally, so a value the
// server would reject (or, worse, quietly misread) is reported as a usage error
// naming the expected shape. It returns the value unchanged so callers can pass
// the original string on the wire.
func ParseTimestamp(flag, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return "", clierr.WithCode(clierr.CodeUsage, fmt.Errorf(
			"--%s must be an RFC 3339 timestamp like 2026-01-31T15:04:05Z (got %q)", flag, value))
	}
	return value, nil
}
