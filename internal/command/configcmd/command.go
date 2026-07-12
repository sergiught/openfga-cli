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
