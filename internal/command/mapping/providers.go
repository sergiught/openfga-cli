package mapping

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	snapauth0 "github.com/sergiught/openfga-cli/internal/mapping/snapshot/auth0"
)

// providerEntry is how a command reaches one identity provider: its snapshot
// types (known without credentials), its flags, and a constructor that reads
// them.
type providerEntry struct {
	Label    string // "Auth0", for display before a provider exists
	Types    func() []snapshot.Type
	AddFlags func(fs *pflag.FlagSet)
	New      func(cmd *cobra.Command) (snapshot.Provider, error)
}

// providers are the --provider values. Tests add "fake".
var providers = map[string]providerEntry{"auth0": auth0Entry()}

// now is the clock, which tests pin.
var now = time.Now

// providerNames lists the registered --provider values, sorted.
func providerNames() []string { return slices.Sorted(maps.Keys(providers)) }

// addProviderFlags registers --provider and every provider's own flags.
func addProviderFlags(cmd *cobra.Command, provider *string) {
	cmd.Flags().StringVar(provider, "provider", "", "identity provider to read ("+strings.Join(providerNames(), ", ")+")")
	for _, name := range providerNames() {
		if add := providers[name].AddFlags; add != nil {
			add(cmd.Flags())
		}
	}
}

func auth0Entry() providerEntry {
	return providerEntry{
		Label: "Auth0",
		Types: snapauth0.Types,
		AddFlags: func(fs *pflag.FlagSet) {
			fs.String("auth0-domain", "", "Auth0 tenant domain (else $AUTH0_DOMAIN)")
			fs.String("auth0-client-id", "", "Auth0 machine-to-machine client ID (else $AUTH0_CLIENT_ID)")
			fs.String("auth0-client-secret-file", "", "file holding the client secret (else $AUTH0_CLIENT_SECRET)")
			fs.String("auth0-tenant", "", "tenant name for display (default: the first label of the domain)")
		},
		New: func(cmd *cobra.Command) (snapshot.Provider, error) {
			cfg, err := auth0Config(cmd)
			if err != nil {
				return nil, err
			}
			return snapauth0.New(cmd.Context(), cfg)
		},
	}
}

// auth0Config builds the Auth0 settings from flags, falling back to the
// environment. The secret never comes from a flag value.
func auth0Config(cmd *cobra.Command) (snapauth0.Config, error) {
	get := func(flag, env string) (string, error) {
		if v, _ := cmd.Flags().GetString(flag); v != "" {
			return v, nil
		}
		if v := os.Getenv(env); v != "" {
			return v, nil
		}
		return "", clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--%s or %s is required", flag, env))
	}
	var cfg snapauth0.Config
	var err error
	if cfg.Domain, err = get("auth0-domain", "AUTH0_DOMAIN"); err != nil {
		return cfg, err
	}
	if cfg.ClientID, err = get("auth0-client-id", "AUTH0_CLIENT_ID"); err != nil {
		return cfg, err
	}
	cfg.Tenant, _ = cmd.Flags().GetString("auth0-tenant")
	if file, _ := cmd.Flags().GetString("auth0-client-secret-file"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return cfg, clierr.WithCode(clierr.CodeUsage, fmt.Errorf("read --auth0-client-secret-file: %w", err))
		}
		if cfg.ClientSecret = strings.TrimSpace(string(data)); cfg.ClientSecret == "" {
			return cfg, clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--auth0-client-secret-file %s is empty", file))
		}
	} else if cfg.ClientSecret = os.Getenv("AUTH0_CLIENT_SECRET"); cfg.ClientSecret == "" {
		return cfg, clierr.WithCode(clierr.CodeUsage, errors.New("--auth0-client-secret-file or AUTH0_CLIENT_SECRET is required"))
	}
	return cfg, nil
}
