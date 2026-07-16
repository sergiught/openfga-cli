package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// buildResolvable returns a Config with active profile "dev" whose client_secret
// is keyring-managed (sentinel on the stored profile).
func buildResolvable(t *testing.T) *Config {
	return &Config{
		path:   filepath.Join(t.TempDir(), "config.toml"),
		Active: "dev",
		Profiles: map[string]Profile{
			"dev": {APIURL: "http://localhost:8080", Auth: Auth{
				Method: AuthClientCredentials, ClientID: "id",
				ClientSecret: keyringSentinel, TokenURL: "http://idp/token",
			}},
		},
	}
}

func TestResolveReadsSecretFromKeyring(t *testing.T) {
	keyring.MockInit()
	c := buildResolvable(t)
	_ = scopedSecretSet(c.path, "dev", "client_secret", "FROM-KEYRING")
	r, err := c.Resolve(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Auth.ClientSecret != "FROM-KEYRING" {
		t.Fatalf("resolved secret = %q", r.Auth.ClientSecret)
	}
}

func TestEnvOverridesKeyring(t *testing.T) {
	keyring.MockInit()
	t.Setenv("OPENFGA_CLIENT_SECRET", "FROM-ENV")
	c := buildResolvable(t)
	_ = scopedSecretSet(c.path, "dev", "client_secret", "FROM-KEYRING")
	r, _ := c.Resolve(Overrides{})
	if r.Auth.ClientSecret != "FROM-ENV" {
		t.Fatalf("env should win, got %q", r.Auth.ClientSecret)
	}
}

func TestEnvBypassesUnavailableKeyring(t *testing.T) {
	// Sentinel on disk but no keyring here (headless server); the env override
	// must bypass the keyring entirely instead of hard-erroring.
	keyring.MockInitWithError(errors.New("no keyring"))
	t.Setenv("OPENFGA_CLIENT_SECRET", "FROM-ENV")
	c := buildResolvable(t)
	r, err := c.Resolve(Overrides{})
	if err != nil {
		t.Fatalf("env should bypass the unavailable keyring, got error: %v", err)
	}
	if r.Auth.ClientSecret != "FROM-ENV" {
		t.Fatalf("resolved secret = %q, want FROM-ENV", r.Auth.ClientSecret)
	}
}

func TestRuntimeFileSecretBypassesUnavailableKeyring(t *testing.T) {
	keyring.MockInitWithError(errors.New("no keyring"))
	c := buildResolvable(t)
	r, err := c.Resolve(Overrides{ClientSecret: "FROM-FILE"})
	if err != nil {
		t.Fatalf("runtime secret should bypass the unavailable keyring, got error: %v", err)
	}
	if r.Auth.ClientSecret != "FROM-FILE" {
		t.Fatalf("resolved secret = %q, want FROM-FILE", r.Auth.ClientSecret)
	}
}

func TestRuntimeAPITokenBypassesUnrelatedKeyringSecret(t *testing.T) {
	keyring.MockInitWithError(errors.New("no keyring"))
	c := buildResolvable(t)
	r, err := c.Resolve(Overrides{APIToken: "TOKEN"})
	if err != nil {
		t.Fatalf("API token should replace the OAuth flow without touching its keyring secret: %v", err)
	}
	if r.Auth.Method != AuthAPIToken || r.Auth.Token != "TOKEN" {
		t.Fatalf("resolved auth = %+v, want runtime API token", r.Auth)
	}
}

func TestRuntimeClientSecretCompletesEnvironmentOAuth(t *testing.T) {
	keyring.MockInitWithError(errors.New("no keyring"))
	t.Setenv("OPENFGA_CLIENT_ID", "client")
	t.Setenv("OPENFGA_TOKEN_URL", "https://issuer.example/token")
	c := New()
	r, err := c.Resolve(Overrides{ClientSecret: "FROM-FILE"})
	if err != nil {
		t.Fatalf("runtime client secret should complete environment OAuth: %v", err)
	}
	if r.Auth.Method != AuthClientCredentials || r.Auth.ClientID != "client" ||
		r.Auth.ClientSecret != "FROM-FILE" || r.Auth.TokenURL != "https://issuer.example/token" {
		t.Fatalf("resolved auth = %+v", r.Auth)
	}
}

func TestEnvBypassesMissingKeyringEntry(t *testing.T) {
	// Keyring available but the entry is missing; the env override must still
	// bypass the keyring rather than erroring on the absent entry.
	keyring.MockInit()
	t.Setenv("OPENFGA_CLIENT_SECRET", "FROM-ENV")
	c := buildResolvable(t)
	r, err := c.Resolve(Overrides{})
	if err != nil {
		t.Fatalf("env should bypass the missing keyring entry, got error: %v", err)
	}
	if r.Auth.ClientSecret != "FROM-ENV" {
		t.Fatalf("resolved secret = %q, want FROM-ENV", r.Auth.ClientSecret)
	}
}

func TestResolveMissingKeyringEntryErrors(t *testing.T) {
	keyring.MockInit() // available, but no entry set
	c := buildResolvable(t)
	if _, err := c.Resolve(Overrides{}); err == nil || !strings.Contains(err.Error(), "keyring") {
		t.Fatalf("want a keyring 'not found' error, got %v", err)
	}
}

func TestDeleteProfileSecretsAfterRemove(t *testing.T) {
	keyring.MockInit()
	c := buildResolvable(t)
	_ = scopedSecretSet(c.path, "dev", "client_secret", "x")
	// Remove refuses to delete the active profile, so switch active to another
	// profile first; the point of this test is the keyring cleanup, not that guard.
	c.Active = "other"
	c.Profiles["other"] = Profile{APIURL: "http://localhost:8080"}
	if err := c.Remove("dev"); err != nil {
		t.Fatal(err)
	}
	if got, err := scopedSecretGet(c.path, "dev", "client_secret"); err != nil || got != "x" {
		t.Fatalf("remove must not delete credentials before the config is saved: got %q, %v", got, err)
	}
	if err := c.deleteSecret("dev", "client_secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := scopedSecretGet(c.path, "dev", "client_secret"); err == nil {
		t.Fatal("post-save cleanup should delete the profile's keyring entries")
	}
}
