package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// buildResolvable returns a Config with active profile "dev" whose client_secret
// is keyring-managed (sentinel on the stored profile).
func buildResolvable(t *testing.T) *Config {
	return &Config{
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
	_ = secretSet("dev", "client_secret", "FROM-KEYRING")
	c := buildResolvable(t)
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
	_ = secretSet("dev", "client_secret", "FROM-KEYRING")
	t.Setenv("OPENFGA_CLIENT_SECRET", "FROM-ENV")
	c := buildResolvable(t)
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

func TestRemoveDeletesKeyringEntries(t *testing.T) {
	keyring.MockInit()
	_ = secretSet("dev", "client_secret", "x")
	c := buildResolvable(t)
	// Remove refuses to delete the active profile, so switch active to another
	// profile first; the point of this test is the keyring cleanup, not that guard.
	c.Active = "other"
	c.Profiles["other"] = Profile{APIURL: "http://localhost:8080"}
	if err := c.Remove("dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := secretGet("dev", "client_secret"); err == nil {
		t.Fatal("remove should delete the profile's keyring entries")
	}
}
