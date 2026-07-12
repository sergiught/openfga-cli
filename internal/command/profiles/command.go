// Package profiles implements `ofga profiles`: manage named connection
// profiles — list, switch, inspect, create, edit and remove them.
package profiles

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
	"github.com/sergiught/openfga-cli/internal/theme"
)

// Command is the `context` command group.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the context command group.
func New(cli *cli.CLI) *Command {
	c := &Command{cli: cli}
	c.cmd = &cobra.Command{
		Use:   "profiles",
		Short: "Manage connection profiles",
		Long:  "Manage named connection profiles. Each profile stores an API URL, optional store and authorization-model IDs, and an optional API token.",
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
			cfg := c.cli.Config
			current := cfg.Theme
			if current == "" {
				current = theme.Default().Name
			}
			if len(args) == 0 {
				if c.cli.JSON {
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
			if err := c.cli.SaveConfig(); err != nil {
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
			cfg := c.cli.Config
			if c.cli.JSON {
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
				rows = append(rows, []string{active, name, p.APIURL, orDash(p.StoreID), orDash(p.ModelID), authMethod(p)})
			}
			output.Table(cmd.OutOrStdout(),
				[]string{"", "PROFILE", "API URL", "STORE", "MODEL", "AUTH"}, rows)
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
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), map[string]string{"active": c.cli.Config.Active})
			}
			fmt.Fprintln(cmd.OutOrStdout(), c.cli.Config.Active)
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
				p, ok := c.cli.Config.Get(args[0])
				if !ok {
					return fmt.Errorf("%w: %q", config.ErrNoProfile, args[0])
				}
				if c.cli.JSON {
					return output.JSON(cmd.OutOrStdout(), p)
				}
				rows := [][2]string{
					{"profile", args[0]},
					{"api_url", p.APIURL},
					{"store_id", orDash(p.StoreID)},
					{"model_id", orDash(p.ModelID)},
				}
				output.KeyValues(cmd.OutOrStdout(), append(rows, authRows(p.ResolvedAuth())...))
				return nil
			}
			r, err := c.cli.Resolve()
			if err != nil {
				return err
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), map[string]string{
					"profile":   r.Profile,
					"api_url":   r.APIURL,
					"store_id":  r.StoreID,
					"model_id":  r.ModelID,
					"auth":      authName(r.Auth),
					"api_token": tokenState(r.APIToken()),
				})
			}
			rows := [][2]string{
				{"profile (active)", r.Profile},
				{"api_url", r.APIURL},
				{"store_id", orDash(r.StoreID)},
				{"model_id", orDash(r.ModelID)},
			}
			output.KeyValues(cmd.OutOrStdout(), append(rows, authRows(r.Auth)...))
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
			if err := c.cli.Config.Use(args[0]); err != nil {
				return err
			}
			if err := c.cli.SaveConfig(); err != nil {
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
		Long: "Set a field on the active profile (or --profile).\n\n" +
			"Connection: api_url, store_id, model_id.\n" +
			"Auth: auth_method (none|api_token|client_credentials|private_key_jwt), token,\n" +
			"client_id, client_secret, token_url, audience, api_audience, key_file,\n" +
			"signing_method, key_id, scopes (space-separated).",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := c.cli.Config.Active
			if c.cli.Overrides.Profile != "" {
				name = c.cli.Overrides.Profile
			}
			p, ok := c.cli.Config.Get(name)
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
			case "auth_method", "auth":
				p.Auth.Method, p.APIToken = val, ""
			case "api_token", "token":
				p.Auth.Method, p.Auth.Token, p.APIToken = config.AuthAPIToken, val, ""
			case "client_id":
				p.Auth.ClientID = val
			case "client_secret":
				p.Auth.ClientSecret = val
			case "token_url":
				p.Auth.TokenURL = val
			case "audience":
				p.Auth.Audience = val
			case "api_audience":
				p.Auth.APIAudience = val
			case "key_file":
				p.Auth.KeyFile = val
			case "signing_method":
				p.Auth.SigningMethod = val
			case "key_id":
				p.Auth.KeyID = val
			case "scopes":
				p.Auth.Scopes = strings.Fields(val)
			default:
				return fmt.Errorf("unknown key %q (see `ofga profiles set --help`)", args[0])
			}
			c.cli.Config.Set(name, p)
			if err := c.cli.SaveConfig(); err != nil {
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
			if _, exists := c.cli.Config.Get(name); exists {
				return fmt.Errorf("profile %q already exists", name)
			}
			if apiURL == "" {
				apiURL = config.DefaultAPIURL
			}
			c.cli.Config.Set(name, config.Profile{
				APIURL:   apiURL,
				StoreID:  storeID,
				ModelID:  modelID,
				APIToken: token,
			})
			if activate {
				_ = c.cli.Config.Use(name)
			}
			if err := c.cli.SaveConfig(); err != nil {
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
			if err := c.cli.Config.Remove(args[0]); err != nil {
				return err
			}
			if err := c.cli.SaveConfig(); err != nil {
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

// authName returns a profile auth method label, defaulting to "none".
func authName(a config.Auth) string {
	if a.Method == "" {
		return config.AuthNone
	}
	return a.Method
}

// authMethod returns a profile's effective auth method label (folding the
// legacy top-level token into api_token).
func authMethod(p config.Profile) string { return authName(p.ResolvedAuth()) }

// authRows renders an auth config as key/value rows for `profiles show`, with
// secrets masked.
func authRows(a config.Auth) [][2]string {
	rows := [][2]string{{"auth", authName(a)}}
	switch a.Method {
	case config.AuthAPIToken:
		rows = append(rows, [2]string{"token", tokenState(a.Token)})
	case config.AuthClientCredentials:
		rows = append(rows,
			[2]string{"client_id", orDash(a.ClientID)},
			[2]string{"client_secret", tokenState(a.ClientSecret)},
			[2]string{"token_url", orDash(a.TokenURL)},
			[2]string{"audience", orDash(a.Audience)},
		)
		if len(a.Scopes) > 0 {
			rows = append(rows, [2]string{"scopes", strings.Join(a.Scopes, " ")})
		}
	case config.AuthPrivateKeyJWT:
		rows = append(rows,
			[2]string{"client_id", orDash(a.ClientID)},
			[2]string{"token_url", orDash(a.TokenURL)},
			[2]string{"audience", orDash(a.Audience)},
			[2]string{"api_audience", orDash(a.APIAudience)},
			[2]string{"key_file", orDash(a.KeyFile)},
			[2]string{"signing_method", orDash(a.SigningMethod)},
		)
		if a.KeyID != "" {
			rows = append(rows, [2]string{"key_id", a.KeyID})
		}
		if len(a.Scopes) > 0 {
			rows = append(rows, [2]string{"scopes", strings.Join(a.Scopes, " ")})
		}
	}
	return rows
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
