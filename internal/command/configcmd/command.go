// Package configcmd implements `ofga config`: inspect where ofga's
// configuration lives and what it resolves to.
package configcmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
)

// Command is the `config` command group.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the config command group.
func New(c *cli.CLI) *Command {
	cmd := &Command{cli: c}
	cmd.cmd = &cobra.Command{
		Use:   "config",
		Short: "Inspect ofga's configuration",
		Long: "Inspect ofga's configuration.\n\n" +
			"Profiles are stored in a plaintext TOML file (mode 0600), so any API\n" +
			"tokens or client secrets saved in a profile are readable by your user.\n" +
			"Prefer environment variables (OPENFGA_API_TOKEN, OPENFGA_CLIENT_SECRET)\n" +
			"or --token-stdin/--value-stdin in CI. Run `ofga config path` to see the\n" +
			"file location.",
		RunE: c.GroupRunE,
	}
	cmd.cmd.AddCommand(cmd.pathCmd())
	return cmd
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

func (c *Command) pathCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "path",
		Short:   "Print the path to the config file",
		Example: "  ofga config path",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), c.cli.Config.Path())
			return nil
		},
	}
}
