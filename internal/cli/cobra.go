package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// GroupRunE is the RunE for a command group (a command whose only job is to
// hold sub-commands). Without it, a group like `ofga stores` is not runnable,
// so cobra prints help and exits 0 even for a mistyped subcommand like
// `ofga stores delet` — a typo'd destructive command appears to succeed. As a
// RunE the group is reached with the stray token as args: no args prints help
// (the natural `ofga stores` behavior), a stray token is rejected with the same
// "unknown command … Did you mean" suggestion the root gives.
//
// It is a method on *CLI purely so command constructors can reference it as
// `cli.GroupRunE` even though their parameter shadows the package name; it uses
// no receiver state.
func (*CLI) GroupRunE(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	// cobra's root typo path defaults SuggestionsMinimumDistance to 2, but a
	// direct SuggestionsFor call uses the zero value, which misses distance-1
	// typos like "lst" → "list". Match the root's threshold.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
		msg += "\n\nDid you mean this?\n"
		for _, s := range suggestions {
			msg += "\t" + s + "\n"
		}
	}
	// A mistyped subcommand is a bad invocation, so exit CodeUsage (2) like the
	// root does, even though this runs after PersistentPreRunE.
	return clierr.WithCode(clierr.CodeUsage, errors.New(msg))
}
