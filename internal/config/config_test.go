package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestIconsModeEnvPrecedence(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		fileVal string
		want    string
	}{
		{name: "env overrides file value", env: "ascii", fileVal: "nerd", want: "ascii"},
		{name: "falls back to file value when env unset", env: "", fileVal: "nerd", want: "nerd"},
		{name: "empty when neither set", env: "", fileVal: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENFGA_ICONS", tt.env)
			c := &Config{Icons: tt.fileVal}
			if got := c.IconsMode(); got != tt.want {
				t.Errorf("IconsMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSaveWritesSecretsWithRestrictivePerms(t *testing.T) {
	keyring.MockInit() // Save pushes the real token into the OS keyring; keep the test hermetic
	dir := filepath.Join(t.TempDir(), "cfg")
	c := &Config{
		path:     filepath.Join(dir, "config.toml"),
		Active:   "default",
		Profiles: map[string]Profile{"default": {APIURL: DefaultAPIURL, Auth: Auth{Method: AuthAPIToken, Token: "supersecret"}}},
	}
	if err := c.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	info, err := os.Stat(c.path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file perms = %o, want 600", perm)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("config dir perms = %o, want 700", perm)
	}
}

func TestResolveProfileAndOverrides(t *testing.T) {
	cfg := &Config{
		Active: "dev",
		Profiles: map[string]Profile{
			"dev":  {APIURL: "http://dev:8080", StoreID: "dev-store"},
			"prod": {APIURL: "http://prod:8080", StoreID: "prod-store"},
		},
	}

	t.Run("OPENFGA_PROFILE selects the profile", func(t *testing.T) {
		t.Setenv("OPENFGA_PROFILE", "prod")
		r, err := cfg.Resolve(Overrides{})
		if err != nil {
			t.Fatal(err)
		}
		if r.Profile != "prod" || r.APIURL != "http://prod:8080" {
			t.Errorf("got profile=%q url=%q, want prod", r.Profile, r.APIURL)
		}
	})

	t.Run("flag profile beats env profile", func(t *testing.T) {
		t.Setenv("OPENFGA_PROFILE", "prod")
		r, err := cfg.Resolve(Overrides{Profile: "dev"})
		if err != nil {
			t.Fatal(err)
		}
		if r.Profile != "dev" {
			t.Errorf("flag should win: got %q", r.Profile)
		}
	})

	t.Run("FGA_API_URL alias is honored", func(t *testing.T) {
		t.Setenv("FGA_API_URL", "http://alias:9999")
		r, err := cfg.Resolve(Overrides{})
		if err != nil {
			t.Fatal(err)
		}
		if r.APIURL != "http://alias:9999" {
			t.Errorf("FGA_API_URL not honored: got %q", r.APIURL)
		}
	})

	t.Run("--api-url flag beats env", func(t *testing.T) {
		t.Setenv("OPENFGA_API_URL", "http://env:8080")
		r, err := cfg.Resolve(Overrides{APIURL: "http://flag:8080"})
		if err != nil {
			t.Fatal(err)
		}
		if r.APIURL != "http://flag:8080" {
			t.Errorf("flag api-url should win: got %q", r.APIURL)
		}
	})
}

func TestResolveRuntimeSecretOverrides(t *testing.T) {
	cfg := &Config{
		Active: "api",
		Profiles: map[string]Profile{
			"api":   {APIURL: DefaultAPIURL, Auth: Auth{Method: AuthAPIToken, Token: "stored"}},
			"oauth": {APIURL: DefaultAPIURL, Auth: Auth{Method: AuthClientCredentials, ClientID: "id", ClientSecret: "stored", TokenURL: "https://issuer/token"}},
			"jwt":   {APIURL: DefaultAPIURL, Auth: Auth{Method: AuthPrivateKeyJWT, ClientID: "id", TokenURL: "https://issuer/token", KeyFile: "/stored.pem"}},
		},
	}

	r, err := cfg.Resolve(Overrides{APIToken: "runtime"})
	if err != nil || r.Auth.Token != "runtime" {
		t.Fatalf("runtime token: auth=%+v err=%v", r.Auth, err)
	}
	r, err = cfg.Resolve(Overrides{Profile: "oauth", ClientSecret: "runtime"})
	if err != nil || r.Auth.ClientSecret != "runtime" {
		t.Fatalf("runtime client secret: auth=%+v err=%v", r.Auth, err)
	}
	r, err = cfg.Resolve(Overrides{Profile: "jwt", PrivateKey: "PEM"})
	if err != nil || r.Auth.PrivateKey != "PEM" || r.Auth.KeyFile != "" {
		t.Fatalf("runtime private key: auth=%+v err=%v", r.Auth, err)
	}
	if _, err := cfg.Resolve(Overrides{ClientSecret: "wrong-flow"}); err == nil {
		t.Fatal("client secret override on a non-client_credentials profile should fail")
	}
}

func TestSaveRejectsConcurrentConfigChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Save(); err != nil {
		t.Fatal(err)
	}
	first, _ := LoadFrom(path)
	second, _ := LoadFrom(path)
	first.Theme = "nord"
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}
	second.Theme = "dracula"
	if err := second.Save(); err == nil || !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("stale save should fail clearly, got %v", err)
	}
}

func TestConcurrentInitialSaveAllowsOnlyOneCreator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	first, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, cfg := range []*Config{first, second} {
		go func(c *Config) {
			<-start
			results <- c.Save()
		}(cfg)
	}
	close(start)
	var succeeded, failed int
	for range 2 {
		if err := <-results; err == nil {
			succeeded++
		} else {
			failed++
		}
	}
	if succeeded != 1 || failed != 1 {
		t.Fatalf("concurrent initial saves: %d succeeded, %d failed; want one each", succeeded, failed)
	}
}

func TestLoadFromSymlinkSavesCanonicalConfig(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.toml")
	cfg, err := LoadFrom(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "config-link.toml")
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	throughAlias, err := LoadFrom(alias)
	if err != nil {
		t.Fatal(err)
	}
	throughAlias.Theme = "nord"
	if err := throughAlias.Save(); err != nil {
		t.Fatal(err)
	}
	if throughAlias.Path() != target {
		t.Fatalf("config path = %q, want canonical target %q", throughAlias.Path(), target)
	}
	if info, err := os.Lstat(alias); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("save replaced config symlink: info=%v err=%v", info, err)
	}
	reloaded, err := LoadFrom(target)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Theme != "nord" {
		t.Fatalf("canonical target was not updated: theme=%q", reloaded.Theme)
	}
}

func TestAuthValidateRejectsIncompleteMethods(t *testing.T) {
	tests := []struct {
		name string
		auth Auth
		want string
	}{
		{"api token", Auth{Method: AuthAPIToken}, "requires a token"},
		{"client id", Auth{Method: AuthClientCredentials}, "requires client_id"},
		{"client secret", Auth{Method: AuthClientCredentials, ClientID: "id"}, "requires client_secret"},
		{"token URL", Auth{Method: AuthClientCredentials, ClientID: "id", ClientSecret: "secret"}, "requires token_url"},
		{"private key", Auth{Method: AuthPrivateKeyJWT, ClientID: "id", TokenURL: "https://issuer/token"}, "requires private_key or key_file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.auth.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestResolveIgnoresSecretsFromInactiveAuthMethod(t *testing.T) {
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	cfg := New()
	p, _ := cfg.Get("default")
	p.Auth = Auth{Method: AuthNone, Token: keyringSentinel}
	cfg.Set("default", p)

	resolved, err := cfg.Resolve(Overrides{})
	if err != nil {
		t.Fatalf("inactive token must not require the keyring: %v", err)
	}
	if resolved.Auth.Method != AuthNone {
		t.Fatalf("resolved auth method = %q", resolved.Auth.Method)
	}
}

func TestSaveRefusesToOverwriteUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// A deliberately broken TOML file the user does not want clobbered.
	const original = "this is = not [valid toml"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if cfg.LoadErr() == nil {
		t.Fatal("expected a load error for a broken config file")
	}

	// Mutating theme/profiles and saving must NOT touch the file on disk.
	cfg.Theme = "nord"
	if err := cfg.Save(); err == nil {
		t.Fatal("Save() should refuse to overwrite an unparseable file")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("broken config was overwritten: got %q, want %q", got, original)
	}
}

func TestSaveRefusesUnsupportedSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// A valid file written by a newer ofga (schema version above what we support).
	original := fmt.Sprintf("version = %d\nactive_profile = \"default\"\n\n[profiles.default]\n  api_url = %q\n",
		SchemaVersion+1, DefaultAPIURL)
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if cfg.LoadErr() == nil {
		t.Fatal("expected a load error for an unsupported schema version")
	}

	cfg.Theme = "nord"
	if err := cfg.Save(); err == nil {
		t.Fatal("Save() should refuse to overwrite a newer-schema file")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("newer-schema config was mutated: got %q, want %q", got, original)
	}
}

func TestResolveEnvOnlyClientCredentials(t *testing.T) {
	t.Setenv("OPENFGA_CLIENT_ID", "cid")
	t.Setenv("OPENFGA_CLIENT_SECRET", "csecret")
	t.Setenv("OPENFGA_TOKEN_URL", "https://issuer/oauth/token")
	t.Setenv("OPENFGA_API_AUDIENCE", "https://api.example.com")
	t.Setenv("OPENFGA_SCOPES", "read write")

	// A profile with no configured auth: the env alone must produce a usable
	// client_credentials grant.
	c := &Config{
		Active:   "p",
		Profiles: map[string]Profile{"p": {APIURL: DefaultAPIURL}},
	}
	r, err := c.Resolve(Overrides{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if r.Auth.Method != AuthClientCredentials {
		t.Fatalf("Auth.Method = %q, want %q", r.Auth.Method, AuthClientCredentials)
	}
	if r.Auth.ClientID != "cid" || r.Auth.ClientSecret != "csecret" ||
		r.Auth.TokenURL != "https://issuer/oauth/token" || r.Auth.Audience != "https://api.example.com" {
		t.Errorf("unexpected resolved auth: %+v", r.Auth)
	}
	if len(r.Auth.Scopes) != 2 || r.Auth.Scopes[0] != "read" || r.Auth.Scopes[1] != "write" {
		t.Errorf("scopes = %v, want [read write]", r.Auth.Scopes)
	}
}

func TestResolveEnvOnlyClientCredentialsIncompleteIsIgnored(t *testing.T) {
	// Missing token URL: the partial env must not fabricate a broken grant.
	t.Setenv("OPENFGA_CLIENT_ID", "cid")
	t.Setenv("OPENFGA_CLIENT_SECRET", "csecret")

	c := &Config{
		Active:   "p",
		Profiles: map[string]Profile{"p": {APIURL: DefaultAPIURL}},
	}
	r, err := c.Resolve(Overrides{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if r.Auth.Method == AuthClientCredentials {
		t.Errorf("incomplete env should not yield a client_credentials grant: %+v", r.Auth)
	}
}

func TestResolveEnvTokenRespectsAuthMethod(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		wantMethod string
		wantToken  string
	}{
		{name: "overrides api_token profile", method: AuthAPIToken, wantMethod: AuthAPIToken, wantToken: "envtoken"},
		{name: "overrides none profile", method: AuthNone, wantMethod: AuthAPIToken, wantToken: "envtoken"},
		{name: "leaves client_credentials intact", method: AuthClientCredentials, wantMethod: AuthClientCredentials, wantToken: ""},
		{name: "leaves private_key_jwt intact", method: AuthPrivateKeyJWT, wantMethod: AuthPrivateKeyJWT, wantToken: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENFGA_API_TOKEN", "envtoken")
			c := &Config{
				Active:   "p",
				Profiles: map[string]Profile{"p": {APIURL: DefaultAPIURL, Auth: Auth{Method: tt.method}}},
			}
			r, err := c.Resolve(Overrides{})
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if r.Auth.Method != tt.wantMethod {
				t.Errorf("Auth.Method = %q, want %q", r.Auth.Method, tt.wantMethod)
			}
			if r.Auth.Token != tt.wantToken {
				t.Errorf("Auth.Token = %q, want %q", r.Auth.Token, tt.wantToken)
			}
		})
	}
}
