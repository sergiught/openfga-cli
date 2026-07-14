// Package profiles implements `ofga profiles`: manage named connection
// profiles — list, switch, inspect, create, edit and remove them.
package profiles

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
	"github.com/sergiught/openfga-cli/internal/style"
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
		Use:     "profiles",
		Aliases: []string{"profile"},
		RunE:    cli.GroupRunE,
		Short:   "Manage connection profiles",
		Long:    "Manage named connection profiles. Each profile stores an API URL, optional store and authorization-model IDs, and an optional API token.",
	}
	c.RegisterSubCommands()
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

// activeProfile returns the effective active profile the same way
// config.Resolve does: a --profile flag beats OPENFGA_PROFILE/FGA_PROFILE, which
// beats the file's active_profile.
func (c *Command) activeProfile() string {
	name := c.cli.Config.Active
	if v := os.Getenv("OPENFGA_PROFILE"); v != "" {
		name = v
	} else if v := os.Getenv("FGA_PROFILE"); v != "" {
		name = v
	}
	if c.cli.Overrides.Profile != "" {
		name = c.cli.Overrides.Profile
	}
	return name
}

// warnLoadErr surfaces a deferred config parse/version error on stderr for the
// read-only inspection commands, which still operate on defaults so the user
// can look around even when their real file is broken.
func warnLoadErr(cmd *cobra.Command, cfg *config.Config) {
	if err := cfg.LoadErr(); err != nil {
		output.Errorf(cmd.ErrOrStderr(), "warning: %v", err)
	}
}

// completeNames suggests configured profile names for the first positional arg.
func (c *Command) completeNames(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return c.cli.Config.ProfileNames(), cobra.ShellCompDirectiveNoFileComp
}

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
	)
}

func (c *Command) listCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all profiles",
		Example: "  ofga profiles list",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := c.cli.Config
			warnLoadErr(cmd, cfg)
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
		Use:     "current",
		Short:   "Show the active profile name",
		Example: "  ofga profiles current",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			warnLoadErr(cmd, c.cli.Config)
			active := c.activeProfile()
			if _, ok := c.cli.Config.Get(active); !ok {
				output.Errorf(cmd.ErrOrStderr(),
					"active profile %q does not exist (set via --profile or OPENFGA_PROFILE?)", active)
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), map[string]string{"active": active})
			}
			fmt.Fprintln(cmd.OutOrStdout(), active)
			return nil
		},
	}
}

func (c *Command) showCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "show [profile]",
		Aliases:           []string{"get"},
		ValidArgsFunction: c.completeNames,
		Short:             "Show a profile's resolved values (token masked)",
		Example: `  ofga profiles show
  ofga profiles show prod`,
		Long: "Show the values of a profile. With no argument, shows the fully resolved active configuration after applying env and flag overrides.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			warnLoadErr(cmd, c.cli.Config)
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
				// Never emit the token (masked or raw) into machine output;
				// report only whether one is configured.
				return output.JSON(cmd.OutOrStdout(), map[string]any{
					"profile":   r.Profile,
					"api_url":   r.APIURL,
					"store_id":  r.StoreID,
					"model_id":  r.ModelID,
					"auth":      authName(r.Auth),
					"has_token": r.APIToken() != "",
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
		Use:               "use <profile>",
		ValidArgsFunction: c.completeNames,
		Short:             "Switch the active profile",
		Example:           "  ofga profiles use prod",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.cli.Config.Use(args[0]); err != nil {
				return err
			}
			if err := c.cli.SaveConfig(); err != nil {
				return err
			}
			output.Successf(cmd.ErrOrStderr(), "switched to profile %s", style.Bold.Render(args[0]))
			return nil
		},
	}
}

func (c *Command) setCmd() *cobra.Command {
	var (
		valueFile  string
		valueStdin bool
	)
	cmd := &cobra.Command{
		Use:   "set <key> [value]",
		Short: "Set a field on a profile",
		Long: "Set a field on the active profile (or --profile).\n\n" +
			"Connection: api_url, store_id, model_id.\n" +
			"Auth: auth_method (none|api_token|client_credentials|private_key_jwt), token,\n" +
			"client_id, client_secret, token_url, audience, api_audience, key_file,\n" +
			"signing_method, key_id, scopes (space-separated).\n\n" +
			"For secrets (token, client_secret) prefer --value-file or --value-stdin so\n" +
			"the value never appears in `ps` output or your shell history.",
		Example: `  ofga profiles set api_url http://localhost:8080
  ofga profiles set auth_method api_token
  ofga profiles set token --value-stdin < token.txt
  ofga profiles set client_secret --value-file ./secret`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := c.cli.Config.Active
			if c.cli.Overrides.Profile != "" {
				name = c.cli.Overrides.Profile
			}
			p, ok := c.cli.Config.Get(name)
			if !ok {
				return fmt.Errorf("%w: %q", config.ErrNoProfile, name)
			}
			var literal string
			if len(args) == 2 {
				if k := strings.ToLower(args[0]); k == "token" || k == "api_token" || k == "client_secret" {
					return fmt.Errorf("refusing to read %s from the command line (it would leak to `ps` and shell history); use --value-file or --value-stdin", k)
				}
				literal = args[1]
			} else if valueFile == "" && !valueStdin {
				return errors.New("value required: pass it as an argument, or use --value-file/--value-stdin")
			}
			val, err := readSecret(cmd.InOrStdin(), literal, valueFile, valueStdin)
			if err != nil {
				return err
			}
			key := strings.ToLower(args[0])
			switch key {
			case "api_url", "url":
				p.APIURL = val
			case "store_id", "store":
				p.StoreID = val
			case "model_id", "model":
				p.ModelID = val
			case "auth_method", "auth":
				switch val {
				case config.AuthNone, config.AuthAPIToken, config.AuthClientCredentials, config.AuthPrivateKeyJWT:
				default:
					return fmt.Errorf("invalid auth_method %q (use none, api_token, client_credentials or private_key_jwt)", val)
				}
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
			output.Successf(cmd.ErrOrStderr(), "set %s on profile %s", style.Key.Render(key), style.Bold.Render(name))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&valueFile, "value-file", "", "read the value from a file instead of an argument")
	f.BoolVar(&valueStdin, "value-stdin", false, "read the value from stdin instead of an argument")
	return cmd
}

func (c *Command) addCmd() *cobra.Command {
	var (
		apiURL, storeID, modelID        string
		token, tokenFile                string
		tokenStdin, activate            bool
		authMethod                      string
		clientID, clientSecret          string
		clientSecretFile                string
		clientSecretStdin               bool
		tokenURL, audience, apiAudience string
		scopes                          []string
		keyFile, signingMethod, keyID   string
	)
	cmd := &cobra.Command{
		Use:     "add <profile>",
		Aliases: []string{"create"},
		Short:   "Create a new profile",
		Long: "Create a named connection profile. The auth method defaults to a bearer\n" +
			"token when --token* is given, otherwise none. For OAuth flows pass\n" +
			"--auth-method client_credentials or private_key_jwt with their fields.",
		Example: `  ofga profiles add dev --api-url http://localhost:8080 --use
  ofga profiles add prod --api-url https://fga.example.com --token-stdin < token.txt
  ofga profiles add ci --auth-method client_credentials \
    --client-id abc --client-secret-stdin --token-url https://issuer/oauth/token \
    --audience https://api.fga.example.com`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, exists := c.cli.Config.Get(name); exists {
				return fmt.Errorf("profile %q already exists", name)
			}
			// Refuse secrets passed literally on argv: they leak to `ps` and
			// shell history. Consistent with `profiles set token`.
			if token != "" {
				return errors.New("refusing to read the token from --token (it would leak to `ps` and shell history); use --token-file or --token-stdin")
			}
			if clientSecret != "" {
				return errors.New("refusing to read the client secret from --client-secret (it would leak to `ps` and shell history); use --client-secret-file or --client-secret-stdin")
			}
			token, err := readSecret(cmd.InOrStdin(), token, tokenFile, tokenStdin)
			if err != nil {
				return err
			}
			secret, err := readSecret(cmd.InOrStdin(), clientSecret, clientSecretFile, clientSecretStdin)
			if err != nil {
				return err
			}
			if apiURL == "" {
				apiURL = config.DefaultAPIURL
			}

			method := authMethod
			if method == "" {
				if token != "" {
					method = config.AuthAPIToken
				} else {
					method = config.AuthNone
				}
			}
			p := config.Profile{APIURL: apiURL, StoreID: storeID, ModelID: modelID}
			switch method {
			case config.AuthNone:
			case config.AuthAPIToken:
				p.Auth = config.Auth{Method: config.AuthAPIToken, Token: token}
			case config.AuthClientCredentials:
				p.Auth = config.Auth{Method: method, ClientID: clientID, ClientSecret: secret,
					TokenURL: tokenURL, Audience: audience, Scopes: scopes}
			case config.AuthPrivateKeyJWT:
				p.Auth = config.Auth{Method: method, ClientID: clientID, TokenURL: tokenURL,
					Audience: audience, APIAudience: apiAudience, KeyFile: keyFile,
					SigningMethod: signingMethod, KeyID: keyID}
			default:
				return fmt.Errorf("invalid auth_method %q (use none, api_token, client_credentials or private_key_jwt)", method)
			}

			c.cli.Config.Set(name, p)
			if activate {
				_ = c.cli.Config.Use(name)
			}
			if err := c.cli.SaveConfig(); err != nil {
				return err
			}
			output.Successf(cmd.ErrOrStderr(), "created profile %s", style.Bold.Render(name))
			if activate {
				output.Infof(cmd.ErrOrStderr(), "now the active profile")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&apiURL, "api-url", "", "API URL (default "+config.DefaultAPIURL+")")
	// Named --store-id/--model-id (not --store/--model) so they don't shadow the
	// global persistent --store/--model overrides on this command.
	f.StringVar(&storeID, "store-id", "", "store ID to save in the profile")
	f.StringVar(&modelID, "model-id", "", "authorization model ID to save in the profile")
	f.StringVar(&authMethod, "auth-method", "", "none | api_token | client_credentials | private_key_jwt")
	f.StringVar(&token, "token", "", "API bearer token (prefer --token-file/--token-stdin)")
	f.StringVar(&tokenFile, "token-file", "", "read the API token from a file")
	f.BoolVar(&tokenStdin, "token-stdin", false, "read the API token from stdin")
	// OAuth (client_credentials / private_key_jwt).
	f.StringVar(&clientID, "client-id", "", "OAuth client ID")
	f.StringVar(&clientSecret, "client-secret", "", "OAuth client secret (prefer --client-secret-file/-stdin)")
	f.StringVar(&clientSecretFile, "client-secret-file", "", "read the client secret from a file")
	f.BoolVar(&clientSecretStdin, "client-secret-stdin", false, "read the client secret from stdin")
	f.StringVar(&tokenURL, "token-url", "", "OAuth token endpoint URL")
	f.StringVar(&audience, "audience", "", "OAuth audience")
	f.StringSliceVar(&scopes, "scopes", nil, "OAuth scopes (comma-separated)")
	f.StringVar(&apiAudience, "api-audience", "", "API audience (private_key_jwt)")
	f.StringVar(&keyFile, "key-file", "", "PEM signing key path (private_key_jwt)")
	f.StringVar(&signingMethod, "signing-method", "", "JWT signing method, e.g. RS256 (private_key_jwt)")
	f.StringVar(&keyID, "key-id", "", "signing key ID (private_key_jwt)")
	f.BoolVar(&activate, "use", false, "switch to this profile after creating it")
	return cmd
}

func (c *Command) removeCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:               "remove <profile>",
		Aliases:           []string{"rm", "delete"},
		ValidArgsFunction: c.completeNames,
		Short:             "Delete a profile",
		Example:           "  ofga profiles remove old --force",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, ok := c.cli.Config.Get(args[0]); !ok {
				return fmt.Errorf("%w: %q", config.ErrNoProfile, args[0])
			}
			// Config.Remove guards the file's active_profile; also refuse the
			// profile selected via --profile or OPENFGA_PROFILE, which would
			// otherwise leave the session pointing at a deleted profile.
			if args[0] == c.activeProfile() {
				return fmt.Errorf("cannot remove the active profile %q; switch first", args[0])
			}
			if err := prompt.Confirm(cmd,
				fmt.Sprintf("remove profile %s and its saved credentials", args[0]), force); err != nil {
				return err
			}
			if err := c.cli.Config.Remove(args[0]); err != nil {
				return err
			}
			if err := c.cli.SaveConfig(); err != nil {
				return err
			}
			output.Successf(cmd.ErrOrStderr(), "removed profile %s", style.Bold.Render(args[0]))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip the confirmation prompt")
	return cmd
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

// readSecret returns a value from exactly one of: a literal (an argument or
// flag), a file, or stdin. It lets tokens and client secrets be supplied
// without appearing as command-line arguments, where they leak into `ps` output
// and shell history. Trailing whitespace/newlines — common in files and in
// `echo secret | ofga …` — are trimmed.
func readSecret(stdin io.Reader, literal, file string, fromStdin bool) (string, error) {
	sources := 0
	for _, set := range []bool{literal != "", file != "", fromStdin} {
		if set {
			sources++
		}
	}
	if sources > 1 {
		return "", errors.New("provide the value only once (as an argument, --*-file, or --*-stdin)")
	}
	switch {
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	case fromStdin:
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read secret from stdin: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	default:
		return literal, nil
	}
}

// tokenState reports whether a secret is set without leaking any of its
// characters — a fixed mask, never a plaintext fragment of the real value.
func tokenState(tok string) string {
	if tok == "" {
		return "—"
	}
	return "••••••••"
}
