package configcmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/theme"
)

// NewTheme builds the top-level `ofga theme` command. The theme is a global
// setting (not per-profile), so it lives here rather than under `profiles`.
func NewTheme(c *cli.CLI) *cobra.Command {
	return &cobra.Command{
		Use:       "theme [name]",
		Short:     "Show or set the color theme",
		Long:      "With no argument, lists available themes and marks the current one. With a name, sets and saves the global theme.",
		Example:   "  ofga theme\n  ofga theme dracula",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: theme.Names(),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := c.Config
			current := cfg.Theme
			if current == "" {
				current = theme.Default().Name
			}
			if len(args) == 0 {
				if c.JSON || c.YAML {
					return output.Emit(cmd.OutOrStdout(), c.YAML, map[string]any{"current": current, "available": theme.Names()})
				}
				for _, n := range theme.Names() {
					marker := "  "
					if n == current {
						marker = style.Success.Render(style.IconDot) + " "
					}
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n", marker, style.Value.Render(n)); err != nil {
						return err
					}
				}
				return nil
			}
			name := args[0]
			if !style.SetTheme(name) {
				return fmt.Errorf("unknown theme %q (available: %s)", name, strings.Join(theme.Names(), ", "))
			}
			previous := cfg.Theme
			cfg.Theme = name
			if err := c.SaveConfig(); err != nil {
				cfg.Theme = previous
				return err
			}
			if c.JSON || c.YAML {
				return output.Emit(cmd.OutOrStdout(), c.YAML, map[string]string{"theme": name})
			}
			if output.Plain {
				return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"theme", name}})
			}
			output.Successf(cmd.ErrOrStderr(), "theme set to %s", style.Bold.Render(name))
			return nil
		},
	}
}
