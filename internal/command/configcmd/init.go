package configcmd

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
	"github.com/sergiught/openfga-cli/internal/readlimit"
	"github.com/sergiught/openfga-cli/internal/style"
)

// NewInit builds the top-level `ofga init` onboarding command. On a terminal it
// runs a guided tour that tests the connection and lets you pick a store and
// model; non-interactively it uses flags and defaults, so it is safe in CI.
func NewInit(c *cli.CLI) *cobra.Command {
	var (
		apiURL, storeID, modelID, token string
		tokenStdin, force               bool
	)
	cmd := &cobra.Command{
		Use:   "init [profile]",
		Short: "Set up a connection profile (guided)",
		Long: "Create or update a connection profile and make it active. On a terminal " +
			"it runs a guided tour: it collects the API URL and authentication, tests " +
			"the connection, and lets you pick a store and model from the server. " +
			"Non-interactively it uses the flags and defaults, so it is safe in CI.",
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
			// on-disk config, not the default synthesized on first run. The
			// guided tour folds this warning into its own screens; the headless
			// path below uses a plain confirm (which fails safe without a TTY).
			needsOverwriteConfirm := previousExists && c.Config.Existed() && !force

			if token == "" && tokenStdin {
				b, err := readlimit.All(cmd.InOrStdin(), readlimit.Secret, "token from stdin")
				if err != nil {
					return fmt.Errorf("read token from stdin: %w", err)
				}
				token = strings.TrimSpace(string(b))
			}
			var auth config.Auth
			if wizardEligible(cmd, c, tokenStdin) {
				// Interactive terminal: run the guided tour. It collects the API
				// URL, full auth, and an optional store/model, testing the
				// connection live. Cancelling leaves the config untouched.
				vals, err := runInitWizard(cmd, c, wizardSeed{
					profile:   name,
					overwrite: needsOverwriteConfirm,
					apiURL:    apiURL,
					storeID:   storeID,
					modelID:   modelID,
				})
				if err != nil {
					return err
				}
				if vals == nil {
					output.Infof(cmd.ErrOrStderr(), "setup cancelled; no changes made")
					return nil
				}
				apiURL, storeID, modelID, auth = vals.apiURL, vals.storeID, vals.modelID, vals.auth
			} else {
				// Non-interactive (CI, pipes, --no-input): fall back to flags and
				// defaults, prompting only where a TTY still allows it.
				if needsOverwriteConfirm {
					if err := prompt.Confirm(cmd,
						fmt.Sprintf("profile %q already exists — overwrite it?", name), false); err != nil {
						return err
					}
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
				if token != "" {
					auth = config.Auth{Method: config.AuthAPIToken, Token: token}
				}
			}

			// init is the recovery path: if the existing file was unparseable or
			// an unsupported schema version, replacing it is the whole point, so
			// clear the load error that would otherwise block Save.
			if c.Config.LoadErr() != nil {
				output.Infof(cmd.ErrOrStderr(), "replacing unreadable config: %v", c.Config.LoadErr())
				c.Config.ClearLoadErr()
			}

			p := config.Profile{APIURL: apiURL, StoreID: storeID, ModelID: modelID, Auth: auth}
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

// wizardEligible reports whether the guided tour can run: both stdin and stdout
// must be a terminal, input must be allowed, and no machine-readable output or
// stdin-consuming flag may be in effect (those keep the headless path).
//
// Stdin is checked via the command stream so tests that redirect it stay
// headless, but stdout is checked on os.Stdout directly: the base command wraps
// cmd's stdout in a colorprofile.Writer, and the bubbletea program renders to
// os.Stdout regardless — matching how the playground gates itself.
func wizardEligible(cmd *cobra.Command, c *cli.CLI, tokenStdin bool) bool {
	if c.NoInput || c.JSON || c.YAML || tokenStdin {
		return false
	}
	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(in.Fd()) {
		return false
	}
	return term.IsTerminal(os.Stdout.Fd())
}

// runInitWizard launches the tour and returns the collected values, or nil if
// the user cancelled (esc/ctrl+c) without reaching the final save.
func runInitWizard(cmd *cobra.Command, c *cli.CLI, seed wizardSeed) (*wizardValues, error) {
	m := newWizard(cmd.Context(), c.RequestTimeout, seed)
	final, err := tea.NewProgram(m, tea.WithContext(cmd.Context())).Run()
	if err != nil {
		return nil, err
	}
	fm, ok := final.(*wizardModel)
	if !ok || fm.cancelled || !fm.done {
		return nil, nil
	}
	return &fm.values, nil
}
