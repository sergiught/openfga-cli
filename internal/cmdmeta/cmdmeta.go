// Package cmdmeta attaches machine-readable metadata to cobra commands and
// flags, so tooling (the command catalog) can describe the CLI without
// parsing help text.
package cmdmeta

import (
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	keyRequiresStore   = "ofga.requires_store"
	keyRequiresModel   = "ofga.requires_model"
	keyMutates         = "ofga.mutates"
	keyInteractiveOnly = "ofga.interactive_only"
	keyEnum            = "ofga.enum"
)

// Meta describes what a leaf command needs and does.
type Meta struct {
	// RequiresStore: the command resolves a store ID and fails without one.
	RequiresStore bool
	// RequiresModel: the command resolves a model ID and fails without one.
	// A command that falls back to the latest model is false.
	RequiresModel bool
	// Mutates: the command changes server state (stores, models, tuples,
	// assertions), local CLI config or files in the working tree (such as a
	// scaffolded workspace). An output file the caller names with a flag such
	// as --output-file or --cursor-file doesn't count. Dry runs and
	// unconfirmed runs still count as mutating commands.
	Mutates bool
	// InteractiveOnly: the command is a TUI entry point.
	InteractiveOnly bool
}

// Set writes all four metadata annotations on cmd and returns it.
func Set(cmd *cobra.Command, m Meta) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[keyRequiresStore] = strconv.FormatBool(m.RequiresStore)
	cmd.Annotations[keyRequiresModel] = strconv.FormatBool(m.RequiresModel)
	cmd.Annotations[keyMutates] = strconv.FormatBool(m.Mutates)
	cmd.Annotations[keyInteractiveOnly] = strconv.FormatBool(m.InteractiveOnly)
	return cmd
}

// Get reads the metadata back. ok is false when any key is missing or
// malformed.
func Get(cmd *cobra.Command) (Meta, bool) {
	var m Meta
	for key, dst := range map[string]*bool{
		keyRequiresStore:   &m.RequiresStore,
		keyRequiresModel:   &m.RequiresModel,
		keyMutates:         &m.Mutates,
		keyInteractiveOnly: &m.InteractiveOnly,
	} {
		v, err := strconv.ParseBool(cmd.Annotations[key])
		if err != nil {
			return Meta{}, false
		}
		*dst = v
	}
	return m, true
}

// SetEnum records the closed set of values flag accepts. The flag must exist.
func SetEnum(cmd *cobra.Command, flag string, values ...string) {
	f := cmd.Flags().Lookup(flag)
	if f == nil {
		f = cmd.PersistentFlags().Lookup(flag)
	}
	if f == nil {
		panic("cmdmeta: SetEnum on unknown flag --" + flag)
	}
	if f.Annotations == nil {
		f.Annotations = map[string][]string{}
	}
	f.Annotations[keyEnum] = append([]string(nil), values...)
}

// Enum returns the flag's accepted values, or nil when it has none recorded.
func Enum(f *pflag.Flag) []string {
	if f == nil {
		return nil
	}
	return f.Annotations[keyEnum]
}

// Leaves returns the commands an agent can run: runnable, non-hidden commands
// under root, skipping cobra's help and completion commands. Pure groups (a
// runnable parent that only prints help and carries no metadata) are left out.
// A parent that carries metadata is kept, since it does real work itself: a TUI
// entry point such as bare `ofga`, or `model test` with its subcommands.
func Leaves(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden {
			return
		}
		if c != root && (c.Name() == "help" || c.Name() == "completion") {
			return
		}
		_, annotated := Get(c)
		if c.Runnable() && (!c.HasAvailableSubCommands() || annotated) {
			out = append(out, c)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return out
}
