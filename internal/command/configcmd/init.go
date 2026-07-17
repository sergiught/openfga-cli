package configcmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
	"github.com/sergiught/openfga-cli/internal/readlimit"
	"github.com/sergiught/openfga-cli/internal/style"
)

// NewInit builds the top-level `ofga init` onboarding command. On a terminal it
// prompts for missing values; non-interactively it uses flags and defaults, so
// it is safe to run in CI.
func NewInit(c *cli.CLI) *cobra.Command {
	var (
		apiURL, storeID, modelID, token string
		tokenStdin, force               bool
	)
	cmd := &cobra.Command{
		Use:   "init [profile]",
		Short: "Set up a connection profile (guided)",
		Long: "Create or update a connection profile and make it active. On a terminal " +
			"it prompts for any values not given as flags; non-interactively it uses the " +
			"flags and defaults, so it is safe in CI.",
		Example: `  ofga init
  ofga init prod --api-url https://fga.example.com --token-stdin < token.txt
  ofga init prod --api-url https://fga.example.com --store-id 01STORE --model-id 01MODEL`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "default"
			if len(args) == 1 {
				name = args[0]
			}
			// Refuse a token passed literally on argv: it leaks to `ps` and
			// shell history. Consistent with `profiles set token`.
			if token != "" {
				return fmt.Errorf("refusing to read the token from --token (it would leak to `ps` and shell history); use --token-stdin")
			}
			previous, previousExists := c.Config.Get(name)
			previousActive := c.Config.Active
			// Only guard against overwrite when the profile came from a real
			// on-disk config, not the default synthesized on first run.
			if _, exists := c.Config.Get(name); exists && c.Config.Existed() && !force {
				if err := prompt.Confirm(cmd,
					fmt.Sprintf("profile %q already exists — overwrite it?", name), false); err != nil {
					return err
				}
			}

			if token == "" && tokenStdin {
				b, err := readlimit.All(cmd.InOrStdin(), readlimit.Secret, "token from stdin")
				if err != nil {
					return fmt.Errorf("read token from stdin: %w", err)
				}
				token = strings.TrimSpace(string(b))
			}
			if apiURL == "" {
				apiURL = prompt.Ask(cmd, "OpenFGA API URL", config.DefaultAPIURL)
			}
			if token == "" && !tokenStdin {
				token = prompt.AskSecret(cmd, "API token (leave blank for none)")
			}
			if storeID == "" {
				storeID = prompt.Ask(cmd, "Store ID (optional)", "")
			}
			if modelID == "" {
				modelID = prompt.Ask(cmd, "Authorization Model ID (optional)", "")
			}

			// init is the recovery path: if the existing file was unparseable or
			// an unsupported schema version, replacing it is the whole point, so
			// clear the load error that would otherwise block Save.
			if c.Config.LoadErr() != nil {
				output.Infof(cmd.ErrOrStderr(), "replacing unreadable config: %v", c.Config.LoadErr())
				c.Config.ClearLoadErr()
			}

			p := config.Profile{APIURL: apiURL, StoreID: storeID, ModelID: modelID}
			if token != "" {
				p.Auth = config.Auth{Method: config.AuthAPIToken, Token: token}
			}
			c.Config.Set(name, p)
			_ = c.Config.Use(name)
			var obsoleteSecrets []string
			if previousExists {
				obsoleteSecrets = previous.Auth.ConfiguredSecretFields()
			}
			saved, err := c.SaveConfigWithSecretCleanup(name, false, obsoleteSecrets...)
			if err != nil {
				if !saved {
					_ = c.Config.Use(previousActive)
					if previousExists {
						c.Config.Set(name, previous)
					} else {
						c.Config.Active = ""
						_ = c.Config.Remove(name)
					}
					c.Config.Active = previousActive
				}
				return err
			}

			if c.JSON || c.YAML {
				return output.Emit(cmd.OutOrStdout(), c.YAML, map[string]any{"profile": name, "active": true})
			}
			if output.Plain {
				return output.KeyValues(cmd.OutOrStdout(), [][2]string{{"profile", name}, {"active", "true"}})
			}
			output.Successf(cmd.ErrOrStderr(), "configured profile %s (now active)", style.Bold.Render(name))
			output.Infof(cmd.ErrOrStderr(), "next: run `ofga stores list` to check the connection")
			return nil
		},
	}
	f := cmd.Flags()
	// Profile-scoped names matching the global override names and `profiles add`.
	f.StringVar(&apiURL, "api-url", "", "API URL to save in the profile (default "+config.DefaultAPIURL+")")
	f.StringVar(&storeID, "store-id", "", "store ID to save in the profile")
	f.StringVar(&modelID, "model-id", "", "authorization model ID to save in the profile")
	f.StringVar(&token, "token", "", "rejected: use --token-stdin")
	f.BoolVar(&tokenStdin, "token-stdin", false, "read the API token from stdin")
	f.BoolVarP(&force, "force", "f", false, "overwrite an existing profile without prompting")
	_ = f.MarkHidden("token")
	return cmd
}
