// Package playground implements `ofga playground`: a full-screen interactive
// TUI for exploring a store — picking models, browsing tuples, running live
// checks, and visualizing the authorization model as a graph.
package playground

import (
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/app"
)

// Command is the `playground` command.
type Command struct {
	app *app.App
	cmd *cobra.Command
}

// New builds the playground command.
func New(a *app.App) *Command {
	c := &Command{app: a}
	c.cmd = &cobra.Command{
		Use:     "playground",
		Aliases: []string{"play", "tui"},
		Short:   "Launch the interactive TUI",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Run(cmd.Context(), c.app)
		},
	}
	c.RegisterSubCommands()
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

// RegisterSubCommands is a no-op; playground has no sub-commands.
func (c *Command) RegisterSubCommands() {}
