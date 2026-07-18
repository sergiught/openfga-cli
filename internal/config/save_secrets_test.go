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
	if got, _ := scopedSecretGet(c.path, "dev", "client_secret"); got != "TOP-SECRET" {
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

func TestSaveFailureRestoresPreviousKeyringSecret(t *testing.T) {
	keyring.MockInit()
	targetDir := filepath.Join(t.TempDir(), "config-target")
	if err := os.Mkdir(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := scopedSecretSet(targetDir, "dev", "token", "OLD"); err != nil {
		t.Fatal(err)
	}
	c := &Config{
		path:   targetDir,
		Active: "dev",
		Profiles: map[string]Profile{
			"dev": {APIURL: DefaultAPIURL, Auth: Auth{Method: AuthAPIToken, Token: "NEW"}},
		},
	}
	if err := c.Save(); err == nil {
		t.Fatal("renaming a config file over a directory should fail")
	}
	if got, err := scopedSecretGet(c.path, "dev", "token"); err != nil || got != "OLD" {
		t.Fatalf("keyring rollback = %q, %v; want OLD", got, err)
	}
	if got := c.Profiles["dev"].Auth.Token; got != "NEW" {
		t.Fatalf("in-memory secret = %q, want NEW after rollback", got)
	}
}

func TestSaveCleanupDeletesOnlyRequestedSecret(t *testing.T) {
	keyring.MockInit()
	c := newSavable(t)
	if err := scopedSecretSet(c.path, "dev", "token", "token"); err != nil {
		t.Fatal(err)
	}
	if err := scopedSecretSet(c.path, "dev", "client_secret", "client"); err != nil {
		t.Fatal(err)
	}
	c.Active = "dev"
	c.Profiles = map[string]Profile{
		"dev": {
			APIURL: DefaultAPIURL,
			Auth: Auth{
				Method:       AuthClientCredentials,
				ClientID:     "id",
				ClientSecret: keyringSentinel,
				TokenURL:     "https://issuer/token",
			},
		},
	}
	saved, err := c.SaveWithSecretCleanup("dev", false, "token")
	if err != nil || !saved {
		t.Fatalf("save with cleanup: saved=%t err=%v", saved, err)
	}
	if _, err := scopedSecretGet(c.path, "dev", "token"); err == nil {
		t.Fatal("requested token was not deleted")
	}
	if got, err := scopedSecretGet(c.path, "dev", "client_secret"); err != nil || got != "client" {
		t.Fatalf("unrelated client secret = %q, %v; want preserved", got, err)
	}
}

func TestSecretsAreNamespacedByConfigPath(t *testing.T) {
	keyring.MockInit()
	path1 := filepath.Join(t.TempDir(), "config.toml")
	path2 := filepath.Join(t.TempDir(), "config.toml")
	newConfig := func(path, token string) *Config {
		return &Config{
			path: path, Active: "same",
			Profiles: map[string]Profile{
				"same": {APIURL: DefaultAPIURL, Auth: Auth{Method: AuthAPIToken, Token: token}},
			},
		}
	}
	c1 := newConfig(path1, "one")
	c2 := newConfig(path2, "two")
	if err := c1.Save(); err != nil {
		t.Fatal(err)
	}
	if err := c2.Save(); err != nil {
		t.Fatal(err)
	}
	r1, err := c1.Resolve(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := c2.Resolve(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Auth.Token != "one" || r2.Auth.Token != "two" {
		t.Fatalf("tokens crossed config namespaces: first=%q second=%q", r1.Auth.Token, r2.Auth.Token)
	}

	delete(c1.Profiles, "same")
	c1.Active = ""
	if saved, err := c1.SaveWithSecretCleanup("same", true); err != nil || !saved {
		t.Fatalf("remove first profile: saved=%t err=%v", saved, err)
	}
	r2, err = c2.Resolve(Overrides{})
	if err != nil || r2.Auth.Token != "two" {
		t.Fatalf("cleaning first config affected second: token=%q err=%v", r2.Auth.Token, err)
	}
}

func TestLegacySecretReadMigratesWithoutDeletingSharedEntry(t *testing.T) {
	keyring.MockInit()
	if err := keyring.Set(keyringService, legacySecretAccount("same", "token"), "legacy"); err != nil {
		t.Fatal(err)
	}
	c := newSavable(t)
	c.Active = "same"
	c.Profiles = map[string]Profile{
		"same": {APIURL: DefaultAPIURL, Auth: Auth{Method: AuthAPIToken, Token: keyringSentinel}},
	}
	r, err := c.Resolve(Overrides{})
	if err != nil || r.Auth.Token != "legacy" {
		t.Fatalf("legacy resolve = %q, %v", r.Auth.Token, err)
	}
	if got, err := scopedSecretGet(c.path, "same", "token"); err != nil || got != "legacy" {
		t.Fatalf("migrated secret = %q, %v", got, err)
	}
	if err := c.deleteSecret("same", "token"); err != nil {
		t.Fatal(err)
	}
	if got, err := legacySecretGet("same", "token"); err != nil || got != "legacy" {
		t.Fatalf("cleanup deleted shared legacy secret: %q, %v", got, err)
	}
}

func TestCleanupDoesNotDeleteNewSecretOfSameField(t *testing.T) {
	keyring.MockInit()
	c := newSavable(t)
	c.Active = "dev"
	c.Profiles = map[string]Profile{
		"dev": {APIURL: DefaultAPIURL, Auth: Auth{Method: AuthAPIToken, Token: "new"}},
	}
	saved, err := c.SaveWithSecretCleanup("dev", false, "token")
	if err != nil || !saved {
		t.Fatalf("save with same-field cleanup: saved=%t err=%v", saved, err)
	}
	if got, err := scopedSecretGet(c.path, "dev", "token"); err != nil || got != "new" {
		t.Fatalf("new token was deleted: %q, %v", got, err)
	}
	if len(c.PendingCredentialCleanups) != 0 {
		t.Fatalf("unsafe cleanup should be discarded, pending=%+v", c.PendingCredentialCleanups)
	}
}

func TestFailedCleanupIsPersistedAndRetryable(t *testing.T) {
	keyring.MockInitWithError(errors.New("locked keyring"))
	c := newSavable(t)
	c.Active = "dev"
	c.Profiles = map[string]Profile{"dev": {APIURL: DefaultAPIURL}}
	saved, err := c.SaveWithSecretCleanup("removed", false, "client_secret")
	if !saved || err == nil {
		t.Fatalf("cleanup failure = saved %t, err %v", saved, err)
	}
	if len(c.PendingCredentialCleanups) != 1 {
		t.Fatalf("pending cleanup not retained: %+v", c.PendingCredentialCleanups)
	}

	keyring.MockInit()
	if err := scopedSecretSet(c.path, "removed", "client_secret", "stale"); err != nil {
		t.Fatal(err)
	}
	if err := scopedSecretSet(c.path, "removed", "token", "unrelated"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadFrom(c.path)
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := reloaded.RetryCredentialCleanup()
	if err != nil || remaining != 0 {
		t.Fatalf("retry cleanup: remaining=%d err=%v", remaining, err)
	}
	if _, err := scopedSecretGet(c.path, "removed", "client_secret"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("retry did not delete exact stale field: %v", err)
	}
	if got, err := scopedSecretGet(c.path, "removed", "token"); err != nil || got != "unrelated" {
		t.Fatalf("retry deleted unrelated field: %q, %v", got, err)
	}
}

// A locked OS keyring (GNOME Keyring's "Cannot create an item in a locked
// collection") still answers reads, so secretsAvailable() can't detect it up
// front; the write is where it surfaces. keyringLocked recognizes it so the CLI
// can explain how to recover instead of leaking the raw D-Bus string.
func TestKeyringLocked(t *testing.T) {
	locked := []string{
		"Cannot create an item in a locked collection",
		"The collection is Locked",
		"org.freedesktop.Secret.Error: collection is locked",
	}
	for _, s := range locked {
		if !keyringLocked(errors.New(s)) {
			t.Errorf("keyringLocked(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"no keyring", "secret not found", "dbus: connection refused"} {
		if keyringLocked(errors.New(s)) {
			t.Errorf("keyringLocked(%q) = true, want false", s)
		}
	}
	if keyringLocked(nil) {
		t.Error("keyringLocked(nil) = true, want false")
	}
	if keyringLocked(keyring.ErrNotFound) {
		t.Error("keyringLocked(ErrNotFound) = true, want false")
	}
}

func TestSecretStoreError(t *testing.T) {
	locked := secretStoreError("client_secret", "local",
		errors.New("Cannot create an item in a locked collection")).Error()
	// The guidance names both escape hatches: the flag and the env var.
	for _, want := range []string{"locked", "client_secret", "local", "--auth-client-secret-file", "OPENFGA_CLIENT_SECRET"} {
		if !strings.Contains(locked, want) {
			t.Errorf("locked-keyring message missing %q: %s", want, locked)
		}
	}

	cause := errors.New("some other keyring failure")
	generic := secretStoreError("token", "prod", cause)
	if !errors.Is(generic, cause) {
		t.Error("generic store error should wrap the underlying cause")
	}
	if strings.Contains(generic.Error(), "locked") {
		t.Errorf("generic store error should not mention locked: %s", generic.Error())
	}
}
