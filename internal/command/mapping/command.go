// Package mapping implements the `ofga mapping` command group, which authors
// openfga/mapper mapping files.
package mapping

import (
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
)

// Command is the `mapping` group.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the group and registers its subcommands.
func New(c *cli.CLI) *Command {
	cmd := &cobra.Command{
		Use:     "mapping",
		Aliases: []string{"mappings"},
		Short:   "Author mapping files that turn events into FGA tuples",
		Long: "Author openfga/mapper mapping files: YAML rules that turn events from an " +
			"identity provider into OpenFGA relationship tuples.",
		RunE: c.GroupRunE,
	}
	g := &Command{cli: c, cmd: cmd}
	g.RegisterSubCommands()
	return g
}

// RegisterSubCommands attaches every `mapping` subcommand.
func (c *Command) RegisterSubCommands() {
	c.cmd.AddCommand(c.newInitCmd())
}

// Command returns the cobra command for registration on the root.
func (c *Command) Command() *cobra.Command { return c.cmd }
