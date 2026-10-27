package base

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sergiught/openfga-cli/internal/cmdmeta"
	"github.com/sergiught/openfga-cli/internal/output"
)

// catalog is the machine-readable description of the whole CLI.
type catalog struct {
	GlobalFlags []flagInfo    `json:"global_flags" yaml:"global_flags"`
	Commands    []commandInfo `json:"commands"     yaml:"commands"`
}

type commandInfo struct {
	Path            string     `json:"path"             yaml:"path"`
	Short           string     `json:"short"            yaml:"short"`
	Usage           string     `json:"usage"            yaml:"usage"`
	Aliases         []string   `json:"aliases"          yaml:"aliases"`
	Flags           []flagInfo `json:"flags"            yaml:"flags"`
	Examples        []string   `json:"examples"         yaml:"examples"`
	RequiresStore   bool       `json:"requires_store"   yaml:"requires_store"`
	RequiresModel   bool       `json:"requires_model"   yaml:"requires_model"`
	Mutates         bool       `json:"mutates"          yaml:"mutates"`
	SupportsDryRun  bool       `json:"supports_dry_run" yaml:"supports_dry_run"`
	InteractiveOnly bool       `json:"interactive_only" yaml:"interactive_only"`
}

type flagInfo struct {
	Name       string   `json:"name"                yaml:"name"`
	Shorthand  string   `json:"shorthand,omitempty" yaml:"shorthand,omitempty"`
	Type       string   `json:"type"                yaml:"type"`
	Default    string   `json:"default"             yaml:"default"`
	Usage      string   `json:"usage"               yaml:"usage"`
	Required   bool     `json:"required"            yaml:"required"`
	Repeatable bool     `json:"repeatable"          yaml:"repeatable"`
	Enum       []string `json:"enum,omitempty"      yaml:"enum,omitempty"`
}

func newFlagInfo(f *pflag.Flag) flagInfo {
	typ := f.Value.Type()
	return flagInfo{
		Name:       f.Name,
		Shorthand:  f.Shorthand,
		Type:       typ,
		Default:    f.DefValue,
		Usage:      f.Usage,
		Required:   len(f.Annotations[cobra.BashCompOneRequiredFlag]) > 0 && f.Annotations[cobra.BashCompOneRequiredFlag][0] == "true",
		Repeatable: strings.HasSuffix(typ, "Slice") || strings.HasSuffix(typ, "Array"),
		Enum:       cmdmeta.Enum(f),
	}
}

func buildCatalog(root *cobra.Command) catalog {
	global := root.PersistentFlags()
	cat := catalog{GlobalFlags: []flagInfo{}, Commands: []commandInfo{}}
	global.VisitAll(func(f *pflag.Flag) {
		if !f.Hidden {
			cat.GlobalFlags = append(cat.GlobalFlags, newFlagInfo(f))
		}
	})

	for _, cmd := range cmdmeta.Leaves(root) {
		meta, _ := cmdmeta.Get(cmd)
		info := commandInfo{
			Path:            cmd.CommandPath(),
			Short:           cmd.Short,
			Usage:           strings.TrimSpace(strings.TrimPrefix(cmd.Use, cmd.Name())),
			Aliases:         append([]string{}, cmd.Aliases...),
			Flags:           []flagInfo{},
			Examples:        []string{},
			RequiresStore:   meta.RequiresStore,
			RequiresModel:   meta.RequiresModel,
			Mutates:         meta.Mutates,
			InteractiveOnly: meta.InteractiveOnly,
		}
		for _, line := range strings.Split(cmd.Example, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				info.Examples = append(info.Examples, line)
			}
		}
		// Flags() holds local and inherited flags once cobra merged them;
		// skip root's persistent set, which is listed once under global_flags.
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden || f.Name == "help" || global.Lookup(f.Name) != nil {
				return
			}
			if f.Name == "dry-run" {
				info.SupportsDryRun = true
			}
			info.Flags = append(info.Flags, newFlagInfo(f))
		})
		cat.Commands = append(cat.Commands, info)
	}
	return cat
}

// commandsCmd prints a catalog of every runnable command with its flags and
// metadata, for scripts and tools that need to discover the CLI.
func (c *Command) commandsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "commands",
		Short: "List every command, its flags and metadata",
		Long: "Print a catalog of every runnable command: path, usage, flags, examples and whether it " +
			"needs a store or model, mutates state, supports --dry-run or is interactive only. " +
			"A command mutates when it changes server state (stores, models, tuples, assertions), local CLI config or files in the working tree; " +
			"an output file named with a flag such as --output-file or --cursor-file doesn't count. " +
			"Use --json or --yaml for the full catalog (global flags are listed once); " +
			"the default is a table of commands.",
		Example: "  ofga commands\n  ofga commands --json | jq -r '.commands[] | select(.mutates) | .path'",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cat := buildCatalog(cmd.Root())
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, cat)
			}
			rows := make([][]string, 0, len(cat.Commands))
			for _, ci := range cat.Commands {
				rows = append(rows, []string{ci.Path, ci.Short})
			}
			return output.Table(cmd.OutOrStdout(), []string{"COMMAND", "DESCRIPTION"}, rows)
		},
	}
	return cmdmeta.Set(cmd, cmdmeta.Meta{})
}
