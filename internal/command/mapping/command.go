// Package mapping implements the `ofga mappings` command group, which checks,
// tests and applies openfga/mapper mapping files.
package mapping

import (
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
)

// Command is the `mappings` group.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the group and registers its subcommands.
func New(c *cli.CLI) *Command {
	g := &Command{cli: c}
	g.cmd = &cobra.Command{
		Use:     "mappings",
		Aliases: []string{"mapping"},
		RunE:    c.GroupRunE,
		Short:   "Validate, test and apply mapping files",
		Long: "Work with openfga/mapper mapping files: YAML rules that turn events from an " +
			"identity provider into OpenFGA relationship tuples.",
		Example: `# Check a mapping file
ofga mappings validate auth0.yaml`,
		// The group itself takes no flags; let unknown ones pass through so a
		// typo'd subcommand carrying a flag reports "unknown command" rather
		// than "unknown flag".
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
	}
	g.RegisterSubCommands()
	return g
}

// RegisterSubCommands attaches every `mappings` subcommand.
func (c *Command) RegisterSubCommands() {
	c.cmd.AddCommand(c.newValidateCmd(), c.newTestCmd(), c.newApplyCmd())
}

// Command returns the cobra command for registration on the root.
func (c *Command) Command() *cobra.Command { return c.cmd }
