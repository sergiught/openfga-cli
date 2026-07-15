package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func newSavable(t *testing.T) *Config {
	t.Helper()
	// package-internal test: set the unexported path directly (as existing
	// config_test.go does).
	return &Config{path: filepath.Join(t.TempDir(), "config.toml")}
}

func TestSaveStoresSecretInKeyringNotDisk(t *testing.T) {
	keyring.MockInit()
	c := newSavable(t)
	c.Profiles = map[string]Profile{
		"dev": {APIURL: "http://localhost:8080", Auth: Auth{Method: AuthClientCredentials, ClientID: "id", ClientSecret: "TOP-SECRET", TokenURL: "http://idp/token"}},
	}
	c.Active = "dev"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(c.path)
	if strings.Contains(string(data), "TOP-SECRET") {
		t.Fatalf("secret must not be written to disk:\n%s", data)
	}
	if !strings.Contains(string(data), keyringSentinel) {
		t.Fatalf("disk should carry the sentinel:\n%s", data)
	}
	if got, _ := secretGet("dev", "client_secret"); got != "TOP-SECRET" {
		t.Fatalf("keyring value = %q", got)
	}
}

func TestSaveHardErrorsWhenKeyringUnavailable(t *testing.T) {
	keyring.MockInitWithError(errors.New("no keyring"))
	c := newSavable(t)
	c.Profiles = map[string]Profile{"dev": {Auth: Auth{Method: AuthClientCredentials, ClientSecret: "x"}}}
	c.Active = "dev"
	if err := c.Save(); err == nil {
		t.Fatal("save with a real secret and no keyring must hard-error")
	}
	if _, statErr := os.Stat(c.path); statErr == nil {
		t.Fatal("nothing should be written on the error path")
	}
}

func TestSaveLeavesSentinelUntouched(t *testing.T) {
	// The keyring is unavailable here on purpose: an all-sentinel profile must
	// still Save without error because the loop skips sentinel fields before
	// ever probing the keyring. If the skip were broken, secretsAvailable()
	// would be false and Save would hard-error, so this proves the skip works.
	keyring.MockInitWithError(errors.New("unavailable"))
	c := newSavable(t)
	c.Profiles = map[string]Profile{"dev": {APIURL: "http://x", Auth: Auth{Method: AuthClientCredentials, ClientSecret: keyringSentinel}}}
	c.Active = "dev"
	if err := c.Save(); err != nil {
		t.Fatal(err) // a loaded (sentinel) secret is not a real value, so no keyring write and no error
	}
}
