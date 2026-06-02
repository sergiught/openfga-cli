// Package contextcmd implements `ofga context`: manage named connection
// profiles (contexts) — list, switch, inspect, create, edit and remove them.
package contextcmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/app"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/theme"
)

// Command is the `context` command group.
type Command struct {
	app *app.App
	cmd *cobra.Command
}

// New builds the context command group.
func New(a *app.App) *Command {
	c := &Command{app: a}
	c.cmd = &cobra.Command{
		Use:     "context",
		Aliases: []string{"ctx", "config"},
		Short:   "Manage connection profiles (contexts)",
		Long:    "Manage named connection profiles. Each profile stores an API URL, optional store and authorization-model IDs, and an optional API token.",
	}
	c.RegisterSubCommands()
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

// RegisterSubCommands wires the context sub-commands.
func (c *Command) RegisterSubCommands() {
	c.cmd.AddCommand(
		c.listCmd(),
		c.currentCmd(),
		c.showCmd(),
		c.useCmd(),
		c.setCmd(),
		c.addCmd(),
		c.removeCmd(),
		c.themeCmd(),
	)
}

func (c *Command) themeCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "theme [name]",
		Short:     "Show or set the color theme",
		Long:      "With no argument, lists available themes and marks the current one. With a name, sets and saves the global theme.",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: theme.Names(),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := c.app.Config
			current := cfg.Theme
			if current == "" {
				current = theme.Default().Name
			}
			if len(args) == 0 {
				if c.app.JSON {
					return output.JSON(cmd.OutOrStdout(), map[string]any{"current": current, "available": theme.Names()})
				}
				for _, n := range theme.Names() {
					marker := "  "
					if n == current {
						marker = style.Success.Render(style.IconDot) + " "
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n", marker, style.Value.Render(n))
				}
				return nil
			}
			name := args[0]
			if !style.SetTheme(name) {
				return fmt.Errorf("unknown theme %q (available: %s)", name, strings.Join(theme.Names(), ", "))
			}
			cfg.Theme = name
			if err := c.app.SaveConfig(); err != nil {
				return err
			}
			output.Successf(cmd.OutOrStdout(), "theme set to %s", style.Bold.Render(name))
			return nil
		},
	}
}

func (c *Command) listCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all profiles",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := c.app.Config
			if c.app.JSON {
				return output.JSON(cmd.OutOrStdout(), map[string]any{
					"active":   cfg.Active,
					"profiles": cfg.Profiles,
				})
			}
			rows := [][]string{}
			for _, name := range cfg.ProfileNames() {
				p, _ := cfg.Get(name)
				active := ""
				if name == cfg.Active {
					active = style.Success.Render("●")
				}
				rows = append(rows, []string{active, name, p.APIURL, orDash(p.StoreID), orDash(p.ModelID), tokenState(p.APIToken)})
			}
			output.Table(cmd.OutOrStdout(),
				[]string{"", "PROFILE", "API URL", "STORE", "MODEL", "TOKEN"}, rows)
			return nil
		},
	}
}

func (c *Command) currentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the active profile name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.app.JSON {
				return output.JSON(cmd.OutOrStdout(), map[string]string{"active": c.app.Config.Active})
			}
			fmt.Fprintln(cmd.OutOrStdout(), c.app.Config.Active)
			return nil
		},
	}
}

func (c *Command) showCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [profile]",
		Short: "Show a profile's resolved values (token masked)",
		Long:  "Show the values of a profile. With no argument, shows the fully resolved active configuration after applying env and flag overrides.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				p, ok := c.app.Config.Get(args[0])
				if !ok {
					return fmt.Errorf("%w: %q", config.ErrNoProfile, args[0])
				}
				if c.app.JSON {
					return output.JSON(cmd.OutOrStdout(), p)
				}
				output.KeyValues(cmd.OutOrStdout(), [][2]string{
					{"profile", args[0]},
					{"api_url", p.APIURL},
					{"store_id", orDash(p.StoreID)},
					{"model_id", orDash(p.ModelID)},
					{"api_token", tokenState(p.APIToken)},
				})
				return nil
			}
			r, err := c.app.Resolve()
			if err != nil {
				return err
			}
			if c.app.JSON {
				return output.JSON(cmd.OutOrStdout(), map[string]string{
					"profile":   r.Profile,
					"api_url":   r.APIURL,
					"store_id":  r.StoreID,
					"model_id":  r.ModelID,
					"api_token": tokenState(r.APIToken),
				})
			}
			output.KeyValues(cmd.OutOrStdout(), [][2]string{
				{"profile (active)", r.Profile},
				{"api_url", r.APIURL},
				{"store_id", orDash(r.StoreID)},
				{"model_id", orDash(r.ModelID)},
				{"api_token", tokenState(r.APIToken)},
			})
			return nil
		},
	}
}

func (c *Command) useCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <profile>",
		Short: "Switch the active profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.app.Config.Use(args[0]); err != nil {
				return err
			}
			if err := c.app.SaveConfig(); err != nil {
				return err
			}
			output.Successf(cmd.OutOrStdout(), "switched to profile %s", style.Bold.Render(args[0]))
			return nil
		},
	}
}

func (c *Command) setCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a field on a profile",
		Long:  "Set a field on the active profile (or --profile). Valid keys: api_url, store_id, model_id, api_token.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := c.app.Config.Active
			if c.app.Overrides.Profile != "" {
				name = c.app.Overrides.Profile
			}
			p, ok := c.app.Config.Get(name)
			if !ok {
				return fmt.Errorf("%w: %q", config.ErrNoProfile, name)
			}
			key, val := strings.ToLower(args[0]), args[1]
			switch key {
			case "api_url", "url":
				p.APIURL = val
			case "store_id", "store":
				p.StoreID = val
			case "model_id", "model":
				p.ModelID = val
			case "api_token", "token":
				p.APIToken = val
			default:
				return fmt.Errorf("unknown key %q (valid: api_url, store_id, model_id, api_token)", args[0])
			}
			c.app.Config.Set(name, p)
			if err := c.app.SaveConfig(); err != nil {
				return err
			}
			output.Successf(cmd.OutOrStdout(), "set %s on profile %s", style.Key.Render(key), style.Bold.Render(name))
			return nil
		},
	}
}

func (c *Command) addCmd() *cobra.Command {
	var (
		apiURL, storeID, modelID, token string
		activate                        bool
	)
	cmd := &cobra.Command{
		Use:   "add <profile>",
		Short: "Create a new profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, exists := c.app.Config.Get(name); exists {
				return fmt.Errorf("profile %q already exists", name)
			}
			if apiURL == "" {
				apiURL = config.DefaultAPIURL
			}
			c.app.Config.Set(name, config.Profile{
				APIURL:   apiURL,
				StoreID:  storeID,
				ModelID:  modelID,
				APIToken: token,
			})
			if activate {
				_ = c.app.Config.Use(name)
			}
			if err := c.app.SaveConfig(); err != nil {
				return err
			}
			output.Successf(cmd.OutOrStdout(), "created profile %s", style.Bold.Render(name))
			if activate {
				output.Infof(cmd.OutOrStdout(), "now the active profile")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&apiURL, "api-url", "", "API URL (default "+config.DefaultAPIURL+")")
	f.StringVar(&storeID, "store", "", "store ID")
	f.StringVar(&modelID, "model", "", "authorization model ID")
	f.StringVar(&token, "token", "", "API bearer token")
	f.BoolVar(&activate, "use", false, "switch to this profile after creating it")
	return cmd
}

func (c *Command) removeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <profile>",
		Aliases: []string{"rm", "delete"},
		Short:   "Delete a profile",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.app.Config.Remove(args[0]); err != nil {
				return err
			}
			if err := c.app.SaveConfig(); err != nil {
				return err
			}
			output.Successf(cmd.OutOrStdout(), "removed profile %s", style.Bold.Render(args[0]))
			return nil
		},
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func tokenState(tok string) string {
	if tok == "" {
		return "—"
	}
	if len(tok) <= 8 {
		return "••••"
	}
	return tok[:3] + "…" + tok[len(tok)-3:]
}
