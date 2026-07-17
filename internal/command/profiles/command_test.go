package profiles

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	charmlog "charm.land/log/v2"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
)

// setRunE returns the RunE of the `profiles set` subcommand, wired to a fresh
// in-memory config (a default profile is active), so validation can be tested
// without touching disk — the error paths return before SaveConfig.
func setRunE(t *testing.T) (func(*cobra.Command, []string) error, *cli.CLI) {
	t.Helper()
	c := cli.New(charmlog.New(io.Discard), config.New(), "test")
	group := New(c).Command()
	for _, sub := range group.Commands() {
		if sub.Name() == "set" {
			return sub.RunE, c
		}
	}
	t.Fatal("set subcommand not found")
	return nil, nil
}

func TestProfilesSetValidation(t *testing.T) {
	run, _ := setRunE(t)
	cmd := &cobra.Command{}

	if err := run(cmd, []string{"auth_method", "bogus"}); err == nil {
		t.Error("invalid auth_method should be rejected")
	}
	if err := run(cmd, []string{"nonsense_key", "x"}); err == nil {
		t.Error("unknown key should be rejected")
	}
	// A secret passed as a literal argument is refused (CFG-7).
	if err := run(cmd, []string{"token", "sekret"}); err == nil {
		t.Error("token as a literal argument should be refused")
	}
	if err := run(cmd, []string{"client_secret", "sekret"}); err == nil {
		t.Error("client_secret as a literal argument should be refused")
	}
	if err := run(cmd, []string{"private_key", "-----BEGIN PRIVATE KEY-----"}); err == nil {
		t.Error("private_key as a literal argument should be refused")
	}
}

// TestSetPrivateKeyStoresInKeyring drives `profiles set private_key --value-stdin`
// against an isolated on-disk config and asserts the PEM lands in the (mocked) OS
// keyring, never on disk.
func TestSetPrivateKeyStoresInKeyring(t *testing.T) {
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := cfg.Get("default")
	p.Auth = config.Auth{
		Method:   config.AuthPrivateKeyJWT,
		ClientID: "client",
		TokenURL: "https://issuer.example/token",
	}
	cfg.Set("default", p)
	c := cli.New(charmlog.New(io.Discard), cfg, "test")
	set := subCmd(t, c, "set")
	if err := set.Flags().Set("value-stdin", "true"); err != nil {
		t.Fatal(err)
	}
	const pem = "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n"
	set.SetIn(strings.NewReader(pem))
	if err := set.RunE(set, []string{"private_key"}); err != nil {
		t.Fatalf("set private_key: %v", err)
	}

	resolved, err := cfg.Resolve(config.Overrides{})
	if err != nil || resolved.Auth.PrivateKey != strings.TrimSpace(pem) {
		t.Fatalf("private_key not resolved from keyring: got %q, err %v", resolved.Auth.PrivateKey, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "BEGIN PRIVATE KEY") {
		t.Fatalf("private key must not be written to disk:\n%s", data)
	}
}

// subCmd returns a named `profiles` subcommand wired to c, so its flags can be
// set and its RunE invoked directly without touching disk.
func subCmd(t *testing.T, c *cli.CLI, name string) *cobra.Command {
	t.Helper()
	for _, sub := range New(c).Command().Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	t.Fatalf("%s subcommand not found", name)
	return nil
}

func TestProfilesAddRefusesLiteralSecrets(t *testing.T) {
	t.Run("token", func(t *testing.T) {
		add := subCmd(t, cli.New(charmlog.New(io.Discard), config.New(), "test"), "add")
		if err := add.Flags().Set("token", "sekret"); err != nil {
			t.Fatal(err)
		}
		if err := add.RunE(add, []string{"new"}); err == nil {
			t.Error("add with a literal --token should be refused")
		}
	})
	t.Run("client-secret", func(t *testing.T) {
		add := subCmd(t, cli.New(charmlog.New(io.Discard), config.New(), "test"), "add")
		if err := add.Flags().Set("client-secret", "sekret"); err != nil {
			t.Fatal(err)
		}
		if err := add.RunE(add, []string{"new"}); err == nil {
			t.Error("add with a literal --client-secret should be refused")
		}
	})
}

func TestProfilesRemoveRefusesEnvActiveProfile(t *testing.T) {
	c := cli.New(charmlog.New(io.Discard), &config.Config{
		Active: "default",
		Profiles: map[string]config.Profile{
			"default": {APIURL: config.DefaultAPIURL},
			"prod":    {APIURL: "http://prod:8080"},
		},
	}, "test")
	remove := subCmd(t, c, "remove")

	// prod is selected via the environment; removing it must be refused even
	// though it is not the file's active_profile. The guard returns before the
	// confirmation prompt, so no --force is needed.
	t.Setenv("OPENFGA_PROFILE", "prod")
	if err := remove.RunE(remove, []string{"prod"}); err == nil {
		t.Error("removing the env-selected active profile should be refused")
	}

}

func TestProfilesCurrentFailsForMissingEffectiveProfile(t *testing.T) {
	c := cli.New(charmlog.New(io.Discard), config.New(), "test")
	current := subCmd(t, c, "current")
	t.Setenv("OPENFGA_PROFILE", "missing")
	if err := current.RunE(current, nil); err == nil {
		t.Fatal("a missing effective profile must return a non-zero command error")
	}
}

func TestProfilesSetUsesEnvironmentSelectedProfile(t *testing.T) {
	for _, env := range []string{"OPENFGA_PROFILE", "FGA_PROFILE"} {
		t.Run(env, func(t *testing.T) {
			keyring.MockInit()
			cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.Set("prod", config.Profile{APIURL: "https://old.example"})
			t.Setenv("OPENFGA_PROFILE", "")
			t.Setenv("FGA_PROFILE", "")
			t.Setenv(env, "prod")
			set := subCmd(t, cli.New(charmlog.New(io.Discard), cfg, "test"), "set")
			if err := set.RunE(set, []string{"api_url", "https://new.example"}); err != nil {
				t.Fatal(err)
			}
			prod, _ := cfg.Get("prod")
			def, _ := cfg.Get("default")
			if prod.APIURL != "https://new.example" || def.APIURL != config.DefaultAPIURL {
				t.Fatalf("set targeted wrong profile: prod=%q default=%q", prod.APIURL, def.APIURL)
			}
		})
	}
}

func TestProfilesUnsetDeletesSecretAfterSave(t *testing.T) {
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := cfg.Get("default")
	p.Auth = config.Auth{Method: config.AuthAPIToken, Token: "secret"}
	cfg.Set("default", p)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	c := cli.New(charmlog.New(io.Discard), cfg, "test")
	unset := subCmd(t, c, "unset")
	if err := unset.RunE(unset, []string{"token"}); err != nil {
		t.Fatal(err)
	}
	p, _ = cfg.Get("default")
	p.Auth = config.Auth{Method: config.AuthAPIToken, Token: "keyring:managed"}
	cfg.Set("default", p)
	if _, err := cfg.Resolve(config.Overrides{}); err == nil {
		t.Fatal("unset token should delete the durable keyring entry")
	}
}

func TestProfilesSetAuthMethodCleansObsoleteSecret(t *testing.T) {
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := cfg.Get("default")
	p.Auth = config.Auth{Method: config.AuthAPIToken, Token: "secret"}
	cfg.Set("default", p)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	set := subCmd(t, cli.New(charmlog.New(io.Discard), cfg, "test"), "set")
	if err := set.RunE(set, []string{"auth_method", config.AuthNone}); err != nil {
		t.Fatal(err)
	}
	p, _ = cfg.Get("default")
	if p.Auth.Method != config.AuthNone || p.Auth.Token != "" {
		t.Fatalf("obsolete token retained after disabling auth: %+v", p.Auth)
	}

	// Reintroducing a stale sentinel must not resolve: the scoped entry was
	// deleted durably when the method changed.
	p.Auth = config.Auth{Method: config.AuthAPIToken, Token: "keyring:managed"}
	cfg.Set("default", p)
	if _, err := cfg.Resolve(config.Overrides{}); err == nil {
		t.Fatal("obsolete token remained in the keyring")
	}
}

func TestProfilesCleanupFailureNamesRetryCommand(t *testing.T) {
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := cfg.Get("default")
	p.Auth = config.Auth{Method: config.AuthAPIToken, Token: "secret"}
	cfg.Set("default", p)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	keyring.MockInitWithError(io.ErrClosedPipe)
	c := cli.New(charmlog.New(io.Discard), cfg, "test")
	unset := subCmd(t, c, "unset")
	err = unset.RunE(unset, []string{"token"})
	if err == nil || !strings.Contains(err.Error(), "ofga profiles cleanup-credentials") {
		t.Fatalf("cleanup error must name retry command, got %v", err)
	}

	keyring.MockInit()
	reloaded, err := config.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := subCmd(t, cli.New(charmlog.New(io.Discard), reloaded, "test"), "cleanup-credentials")
	if err := cleanup.RunE(cleanup, nil); err != nil {
		t.Fatalf("retry command failed: %v", err)
	}
}

func TestProfilesAddSaveFailurePreservesExistingKeyringEntry(t *testing.T) {
	keyring.MockInit()
	if err := keyring.Set("openfga-cli", "dev.token", "old-secret"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config-target")
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("new-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := cli.New(charmlog.New(io.Discard), cfg, "test")
	add := subCmd(t, c, "add")
	if err := add.Flags().Set("auth-method", config.AuthAPIToken); err != nil {
		t.Fatal(err)
	}
	if err := add.Flags().Set("token-file", tokenFile); err != nil {
		t.Fatal(err)
	}
	if err := add.RunE(add, []string{"dev"}); err == nil {
		t.Fatal("save should fail when the config target is a directory")
	}
	if got, err := keyring.Get("openfga-cli", "dev.token"); err != nil || got != "old-secret" {
		t.Fatalf("keyring entry after rollback = %q, %v; want old-secret", got, err)
	}
	if _, ok := cfg.Get("dev"); ok {
		t.Fatal("failed profile creation should be rolled back in memory")
	}
}

func TestReadSecret(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	if err := os.WriteFile(file, []byte("filetoken\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		literal   string
		file      string
		fromStdin bool
		stdin     string
		want      string
		wantErr   bool
	}{
		{name: "literal", literal: "littoken", want: "littoken"},
		{name: "file trims trailing newline", file: file, want: "filetoken"},
		{name: "stdin trims whitespace", fromStdin: true, stdin: "  stdintoken\n", want: "stdintoken"},
		{name: "empty is allowed", want: ""},
		{name: "two sources rejected", literal: "x", fromStdin: true, stdin: "y", wantErr: true},
		{name: "missing file errors", file: filepath.Join(dir, "nope"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readSecret(strings.NewReader(tt.stdin), tt.literal, tt.file, tt.fromStdin)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("readSecret() expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("readSecret() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("readSecret() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTokenStateNeverLeaksSecret(t *testing.T) {
	const secret = "supersecrettoken123"
	got := tokenState(secret)
	if strings.Contains(got, secret[:3]) || strings.Contains(got, secret[len(secret)-3:]) {
		t.Errorf("tokenState(%q) = %q leaks characters of the secret", secret, got)
	}
	if got == secret {
		t.Errorf("tokenState returned the raw secret")
	}
	if tokenState("") != "—" {
		t.Errorf("tokenState(\"\") = %q, want em dash", tokenState(""))
	}
}
