package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"
)

// completionTimeout bounds a network-backed completion so a slow or
// unreachable server never hangs the user's shell mid-tab.
const completionTimeout = 2 * time.Second

// CompleteModelIDs suggests authorization model IDs for the resolved store,
// for a command whose first positional argument is an optional model ID. It
// lives here so every such command completes the same way instead of each
// growing its own copy.
func (cli *CLI) CompleteModelIDs(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cl, _, err := cli.ClientWithStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()
	var out []string
	for m, err := range cl.AuthorizationModels.All(ctx, nil) {
		if err != nil {
			break
		}
		out = append(out, m.ID)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
