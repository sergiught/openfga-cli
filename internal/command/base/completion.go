package base

import (
	"context"
	"time"

	"github.com/spf13/cobra"
)

// compTimeout bounds network-backed completions so a slow or unreachable server
// never hangs the user's shell mid-tab.
const compTimeout = 2 * time.Second

// completeProfiles suggests configured profile names (no network).
func (c *Command) completeProfiles(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return c.cli.Config.ProfileNames(), cobra.ShellCompDirectiveNoFileComp
}

// completeStores suggests store IDs (with names as descriptions) from the API.
func (c *Command) completeStores(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	cl, err := c.cli.Client()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), compTimeout)
	defer cancel()
	var out []string
	for st, err := range cl.Stores.All(ctx, nil) {
		if err != nil {
			break
		}
		out = append(out, st.ID+"\t"+st.Name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeModels suggests authorization model IDs for the resolved store.
func (c *Command) completeModels(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	cl, _, err := c.cli.ClientWithStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), compTimeout)
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
