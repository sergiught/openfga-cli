// Package config loads and persists ofga configuration: a set of named
// connection profiles (contexts) plus the name of the active one. Values are
// stored as TOML in the platform config directory and can be overridden by
// OPENFGA_* environment variables and command-line flags.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	gap "github.com/muesli/go-app-paths"
)

const (
	appName        = "ofga"
	configFileName = "config.toml"

	// DefaultAPIURL is the address of a stock local OpenFGA server.
	DefaultAPIURL = "http://localhost:8080"
)

// ErrNoProfile is returned when a requested profile does not exist.
var ErrNoProfile = errors.New("profile not found")

// Auth method names for Auth.Method.
const (
	AuthNone              = "none"
	AuthAPIToken          = "api_token"
	AuthClientCredentials = "client_credentials"
	AuthPrivateKeyJWT     = "private_key_jwt"
)

// Auth holds a profile's authentication configuration. Which fields apply
// depends on Method; unused ones stay empty.
type Auth struct {
	Method string `toml:"method,omitempty" json:"method,omitempty"` // none | api_token | client_credentials | private_key_jwt

	Token string `toml:"token,omitempty" json:"-"` // secret; never serialized to JSON output

	// client_credentials and private_key_jwt share the OAuth2 grant shape.
	ClientID     string   `toml:"client_id,omitempty" json:"client_id,omitempty"`
	ClientSecret string   `toml:"client_secret,omitempty" json:"-"` // secret; never serialized to JSON output
	TokenURL     string   `toml:"token_url,omitempty" json:"token_url,omitempty"`
	Audience     string   `toml:"audience,omitempty" json:"audience,omitempty"`
	Scopes       []string `toml:"scopes,omitempty" json:"scopes,omitempty"`

	// private_key_jwt.
	APIAudience   string `toml:"api_audience,omitempty" json:"api_audience,omitempty"` // audience requested in the grant
	KeyFile       string `toml:"key_file,omitempty" json:"key_file,omitempty"`         // path to the PEM signing key
	SigningMethod string `toml:"signing_method,omitempty" json:"signing_method,omitempty"`
	KeyID         string `toml:"key_id,omitempty" json:"key_id,omitempty"`

	// private_key holds the PEM signing key contents for private_key_jwt when
	// stored in the OS keyring (config carries the sentinel). key_file remains
	// the on-disk-path alternative.
	PrivateKey string `toml:"private_key,omitempty" json:"-"` // secret; never serialized to JSON output
}

// Profile is a single named connection context.
type Profile struct {
	APIURL  string `toml:"api_url" json:"api_url"`
	StoreID string `toml:"store_id,omitempty" json:"store_id,omitempty"`
	ModelID string `toml:"model_id,omitempty" json:"model_id,omitempty"`
	Auth    Auth   `toml:"auth,omitempty" json:"auth"`
}

// ResolvedAuth returns the profile's effective auth.
func (p Profile) ResolvedAuth() Auth { return p.Auth }

// secretFields returns the keyring-managed secret fields of a, so Save and
// Resolve can treat them uniformly.
func (a *Auth) secretFields() []secretField {
	return []secretField{
		{"token", &a.Token},
		{"client_secret", &a.ClientSecret},
		{"private_key", &a.PrivateKey},
	}
}

// Config is the on-disk configuration document.
// SchemaVersion is the current on-disk config format version, written on Save
// and available to gate future migrations.
const SchemaVersion = 1

type Config struct {
	Version  int                `toml:"version"`
	Active   string             `toml:"active_profile"`
	Theme    string             `toml:"theme,omitempty"`
	Icons    string             `toml:"icons,omitempty"`
	Profiles map[string]Profile `toml:"profiles"`

	path    string // resolved file path; not serialized
	existed bool   // whether the config was read from an existing file
	loadErr error  // a deferred parse/version error; nil when the file loaded cleanly
}

// Resolved is the fully merged, ready-to-use connection configuration after
// applying profile values, environment variables and flag overrides.
type Resolved struct {
	Profile string
	APIURL  string
	StoreID string
	ModelID string
	Auth    Auth
}

// APIToken returns the pre-shared token when the resolved auth uses one, for
// callers that only care about the legacy token (e.g. masked display).
func (r Resolved) APIToken() string {
	if r.Auth.Method == AuthAPIToken {
		return r.Auth.Token
	}
	return ""
}

// New returns an empty Config seeded with a sensible default profile.
func New() *Config {
	return &Config{
		Active: "default",
		Profiles: map[string]Profile{
			"default": {APIURL: DefaultAPIURL},
		},
	}
}

// Path returns the resolved configuration file path.
func (c *Config) Path() string { return c.path }

// DefaultPath returns the resolved config file path, or "" if it can't be
// determined. Useful before a Config is loaded (e.g. to point at a broken file).
func DefaultPath() string {
	p, err := resolvePath()
	if err != nil {
		return ""
	}
	return p
}

// Existed reports whether the config was read from an existing file on disk,
// as opposed to a freshly-minted in-memory default. Callers use this to write
// out a starter config on first run.
func (c *Config) Existed() bool { return c.existed }

// LoadErr returns the deferred parse/version error recorded at load time, or
// nil when the file loaded cleanly. Read-only inspection commands surface it as
// a warning while still operating on the in-memory defaults.
func (c *Config) LoadErr() error { return c.loadErr }

// ClearLoadErr discards a recorded load error so a subsequent Save is allowed.
// Only the intentional recovery path (`ofga init`) uses this: it deliberately
// replaces an unparseable or unsupported file, whereas ordinary mutations keep
// the Save guard that refuses to clobber a file that failed to load.
func (c *Config) ClearLoadErr() { c.loadErr = nil }

// IconsMode returns the configured glyph capability rung, giving precedence
// to the OPENFGA_ICONS environment variable over the on-disk value.
func (c *Config) IconsMode() string {
	if v := os.Getenv("OPENFGA_ICONS"); v != "" {
		return v
	}
	return c.Icons
}

// resolvePath computes the config file path: an explicit OPENFGA_CONFIG wins,
// otherwise the platform-appropriate default is used.
func resolvePath() (string, error) {
	if v := os.Getenv("OPENFGA_CONFIG"); v != "" {
		return v, nil
	}
	scope := gap.NewScope(gap.User, appName)
	p, err := scope.ConfigPath(configFileName)
	if err != nil {
		return "", err
	}
	return p, nil
}

// Load reads the config from its default location (honoring OPENFGA_CONFIG).
func Load() (*Config, error) { return LoadFrom("") }

// LoadFrom reads the config from path, or the resolved default when path is
// empty. If the file does not exist, a fresh default config is returned (not yet
// written to disk). A parse failure or an unsupported schema version is not
// returned as an error but recorded on the Config (see Resolve), so read-only
// inspection like `ofga config path` still works against a broken file.
func LoadFrom(path string) (*Config, error) {
	if path == "" {
		var err error
		path, err = resolvePath()
		if err != nil {
			return nil, err
		}
	}

	cfg := New()
	cfg.path = path

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Reset profiles so the file is authoritative, then decode.
	cfg.Profiles = map[string]Profile{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		broken := New()
		broken.path = path
		broken.existed = true
		broken.loadErr = fmt.Errorf("parse config %s: %w", path, err)
		return broken, nil
	}
	cfg.existed = true
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	if len(cfg.Profiles) == 0 {
		cfg.Profiles["default"] = Profile{APIURL: DefaultAPIURL}
	}
	if cfg.Active == "" {
		cfg.Active = "default"
	}
	if cfg.Version > SchemaVersion {
		cfg.loadErr = fmt.Errorf("config %s is schema version %d, but this ofga supports up to %d — please upgrade ofga",
			path, cfg.Version, SchemaVersion)
	}
	return cfg, nil
}

// Save writes the config to disk, creating parent directories as needed. The
// file holds API tokens and client secrets, so it is written with 0600
// permissions (dir 0700) via a temp file + atomic rename, ensuring the secrets
// are never world-readable even briefly and never left truncated on error.
func (c *Config) Save() error {
	// Refuse to overwrite a file we could not parse (or that a newer ofga
	// wrote): replacing it with the in-memory defaults would destroy the user's
	// real config. Callers surface this instead of silently clobbering.
	if c.loadErr != nil {
		return c.loadErr
	}
	if c.path == "" {
		p, err := resolvePath()
		if err != nil {
			return err
		}
		c.path = p
	}
	if c.Version == 0 {
		c.Version = SchemaVersion
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(c.path), configFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure config file: %w", err)
	}
	enc := toml.NewEncoder(tmp)
	enc.Indent = "  "
	if err := enc.Encode(c); err != nil {
		tmp.Close()
		return fmt.Errorf("encode config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

// ProfileNames returns the profile names sorted alphabetically.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Get returns a profile by name.
func (c *Config) Get(name string) (Profile, bool) {
	p, ok := c.Profiles[name]
	return p, ok
}

// Set creates or replaces a profile.
func (c *Config) Set(name string, p Profile) {
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	c.Profiles[name] = p
}

// Remove deletes a profile. The active profile cannot be removed.
func (c *Config) Remove(name string) error {
	if _, ok := c.Profiles[name]; !ok {
		return c.noProfileErr(name)
	}
	if name == c.Active {
		return fmt.Errorf("cannot remove the active profile %q; switch first", name)
	}
	delete(c.Profiles, name)
	return nil
}

// Use sets the active profile.
func (c *Config) Use(name string) error {
	if _, ok := c.Profiles[name]; !ok {
		return c.noProfileErr(name)
	}
	c.Active = name
	return nil
}

// noProfileErr builds an ErrNoProfile error that lists the available profiles,
// so a typo'd or missing profile points the user at the real names.
func (c *Config) noProfileErr(name string) error {
	if names := c.ProfileNames(); len(names) > 0 {
		return fmt.Errorf("%w: %q (available: %s)", ErrNoProfile, name, strings.Join(names, ", "))
	}
	return fmt.Errorf("%w: %q (no profiles configured; run `ofga init`)", ErrNoProfile, name)
}

// Overrides carries flag-supplied values that take precedence over everything.
// An empty string means "not set".
type Overrides struct {
	Profile string
	APIURL  string
	StoreID string
	ModelID string
}

// firstEnv returns the first non-empty value among the named environment
// variables. The OPENFGA_* names are canonical; the FGA_* names are accepted as
// aliases for compatibility with the official CLI.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// splitScopes parses an OPENFGA_SCOPES value, which may be space- or
// comma-separated.
func splitScopes(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t' || r == '\n'
	})
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// Resolve merges, in increasing order of precedence: profile values, OPENFGA_*
// (or FGA_*) environment variables, then flag overrides.
func (c *Config) Resolve(o Overrides) (Resolved, error) {
	// A file that failed to parse (or was written by a newer ofga) surfaces here,
	// so commands needing a profile fail clearly while `config path` still works.
	if c.loadErr != nil {
		return Resolved{}, c.loadErr
	}
	name := c.Active
	if v := firstEnv("OPENFGA_PROFILE", "FGA_PROFILE"); v != "" {
		name = v
	}
	if o.Profile != "" {
		name = o.Profile
	}
	p, ok := c.Profiles[name]
	if !ok {
		return Resolved{}, c.noProfileErr(name)
	}

	r := Resolved{
		Profile: name,
		APIURL:  p.APIURL,
		StoreID: p.StoreID,
		ModelID: p.ModelID,
		Auth:    p.ResolvedAuth(),
	}

	// Environment overrides.
	if v := firstEnv("OPENFGA_API_URL", "FGA_API_URL"); v != "" {
		r.APIURL = v
	}
	if v := firstEnv("OPENFGA_STORE_ID", "FGA_STORE_ID"); v != "" {
		r.StoreID = v
	}
	if v := firstEnv("OPENFGA_MODEL_ID", "OPENFGA_AUTHORIZATION_MODEL_ID",
		"FGA_MODEL_ID", "FGA_AUTHORIZATION_MODEL_ID"); v != "" {
		r.ModelID = v
	}
	// A token from the environment overrides the profile's token, but only when
	// the profile isn't using an OAuth flow (client_credentials/private_key_jwt).
	// Silently swapping a configured OAuth flow for a bare token would disable
	// the profile's real auth without any signal; switch profiles for that.
	if v := firstEnv("OPENFGA_API_TOKEN", "FGA_API_TOKEN"); v != "" {
		if r.Auth.Method == "" || r.Auth.Method == AuthNone || r.Auth.Method == AuthAPIToken {
			r.Auth = Auth{Method: AuthAPIToken, Token: v}
		}
	}
	// OAuth secrets from the environment, so CI need not persist them in the
	// config file. Each only applies to its own flow.
	if v := firstEnv("OPENFGA_CLIENT_SECRET", "FGA_CLIENT_SECRET"); v != "" && r.Auth.Method == AuthClientCredentials {
		r.Auth.ClientSecret = v
	}
	if v := firstEnv("OPENFGA_KEY_FILE", "FGA_KEY_FILE"); v != "" && r.Auth.Method == AuthPrivateKeyJWT {
		r.Auth.KeyFile = v
	}
	// Env-only client_credentials, so CI can run an OAuth flow without a stored
	// profile. Only when the profile isn't already using a different flow, and
	// only when the full grant (id + secret + token URL) is present in the
	// environment; individual fields fall back to any stored profile values.
	if pm := p.ResolvedAuth().Method; pm == "" || pm == AuthClientCredentials {
		clientID := firstEnv("OPENFGA_CLIENT_ID", "FGA_CLIENT_ID")
		clientSecret := firstEnv("OPENFGA_CLIENT_SECRET", "FGA_CLIENT_SECRET")
		tokenURL := firstEnv("OPENFGA_TOKEN_URL", "FGA_TOKEN_URL")
		if clientID == "" {
			clientID = r.Auth.ClientID
		}
		if clientSecret == "" {
			clientSecret = r.Auth.ClientSecret
		}
		if tokenURL == "" {
			tokenURL = r.Auth.TokenURL
		}
		if clientID != "" && clientSecret != "" && tokenURL != "" {
			audience := firstEnv("OPENFGA_API_AUDIENCE", "FGA_API_AUDIENCE")
			if audience == "" {
				audience = r.Auth.Audience
			}
			scopes := r.Auth.Scopes
			if s := firstEnv("OPENFGA_SCOPES", "FGA_SCOPES"); s != "" {
				scopes = splitScopes(s)
			}
			r.Auth = Auth{
				Method:       AuthClientCredentials,
				ClientID:     clientID,
				ClientSecret: clientSecret,
				TokenURL:     tokenURL,
				Audience:     audience,
				Scopes:       scopes,
			}
		}
	}

	// Flag overrides (highest precedence).
	if o.APIURL != "" {
		r.APIURL = o.APIURL
	}
	if o.StoreID != "" {
		r.StoreID = o.StoreID
	}
	if o.ModelID != "" {
		r.ModelID = o.ModelID
	}

	if r.APIURL == "" {
		r.APIURL = DefaultAPIURL
	}
	return r, nil
}
