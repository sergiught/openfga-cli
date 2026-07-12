package config

import (
	"os"
	"path/filepath"
	"testing"
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
	dir := filepath.Join(t.TempDir(), "cfg")
	c := &Config{
		path:     filepath.Join(dir, "config.toml"),
		Active:   "default",
		Profiles: map[string]Profile{"default": {APIURL: DefaultAPIURL, APIToken: "supersecret"}},
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
