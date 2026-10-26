// Package mapping implements the `ofga mappings` command group, which authors
// openfga/mapper mapping files.
package mapping

import (
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/cmdmeta"
)

// Command is the `mappings` group.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the group and registers its subcommands.
func New(c *cli.CLI) *Command {
	g := &Command{cli: c}
	cmd := &cobra.Command{
		Use:     "mappings [file]",
		Aliases: []string{"mapping"},
		Short:   "Open the mapping workspace, or validate, test and apply mapping files",
		Long: "Work with openfga/mapper mapping files: YAML rules that turn events from an " +
			"identity provider into OpenFGA relationship tuples.\n\n" +
			"With no subcommand this opens the interactive workspace, which needs a terminal. " +
			"There you edit the rules against sample events with the resulting tuples shown live, " +
			"check them against a model, run the tests, and apply events to the store. " +
			"Give it a mapping file to edit that file, or a path that does not exist yet to start a " +
			"new mapping there; without one it shows a start screen " +
			"with the mapping files in this directory, the ones you opened recently, and two ways " +
			"to start a new one: from Auth0 events, or blank. In scripts use the subcommands.",
		Example: `# Pick a file from the start screen
ofga mappings

# Edit a mapping file
ofga mappings auth0.yaml

# Start a new mapping file
ofga mappings sync/new.yaml

# Check a mapping file in a script
ofga mappings validate auth0.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: g.runWorkspace,
	}
	// The workspace needs a terminal and can write files and apply events.
	cmdmeta.Set(cmd, cmdmeta.Meta{Mutates: true, InteractiveOnly: true})
	g.cmd = cmd
	// The root's threshold: a direct SuggestionsFor (looksLikeTypo) otherwise
	// uses 0 and misses distance-1 typos.
	cmd.SuggestionsMinimumDistance = 2
	g.RegisterSubCommands()
	return g
}

// RegisterSubCommands attaches every `mappings` subcommand.
func (c *Command) RegisterSubCommands() {
	c.cmd.AddCommand(
		cmdmeta.Set(c.newValidateCmd(), cmdmeta.Meta{}),
		cmdmeta.Set(c.newTestCmd(), cmdmeta.Meta{Mutates: true}),
		cmdmeta.Set(c.newApplyCmd(), cmdmeta.Meta{RequiresStore: true, Mutates: true}),
	)
}

// Command returns the cobra command for registration on the root.
func (c *Command) Command() *cobra.Command { return c.cmd }
