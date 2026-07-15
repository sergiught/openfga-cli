// Package configcmd implements `ofga config`: inspect where ofga's
// configuration lives and what it resolves to.
package configcmd

import (
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/output"
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
			"Profile metadata is stored in a TOML file (mode 0600). Tokens, client\n" +
			"secrets, and private keys are stored separately in the OS keyring; the\n" +
			"TOML file contains only managed-secret markers. Environment variables\n" +
			"remain available for ephemeral CI overrides. Run `ofga config path` to\n" +
			"see the file location.",
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
			path := c.cli.Config.Path()
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]string{"path": path})
			}
			_, err := cmd.OutOrStdout().Write([]byte(path + "\n"))
			return err
		},
	}
}
