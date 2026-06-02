// Package command defines the shared Command abstraction used by every ofga
// command, mirroring the structure of task-pilot-cli: each command exposes its
// cobra command, registers its sub-commands, and implements its run logic.
package command

import "github.com/spf13/cobra"

// Command is implemented by every ofga (sub)command.
type Command interface {
	// Command returns the underlying cobra command.
	Command() *cobra.Command
	// RegisterSubCommands wires child commands onto this command.
	RegisterSubCommands()
}
