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
}

// Profile is a single named connection context.
type Profile struct {
	APIURL   string `toml:"api_url" json:"api_url"`
	StoreID  string `toml:"store_id,omitempty" json:"store_id,omitempty"`
	ModelID  string `toml:"model_id,omitempty" json:"model_id,omitempty"`
	APIToken string `toml:"api_token,omitempty" json:"-"` // legacy secret; never serialized to JSON output
	Auth     Auth   `toml:"auth,omitempty" json:"auth"`
}

// ResolvedAuth returns the profile's effective auth, folding the legacy
// top-level api_token into an api_token method when no explicit method is set.
func (p Profile) ResolvedAuth() Auth {
	if p.Auth.Method == "" && p.APIToken != "" {
		return Auth{Method: AuthAPIToken, Token: p.APIToken}
	}
	return p.Auth
}

// Config is the on-disk configuration document.
type Config struct {
	Active   string             `toml:"active_profile"`
	Theme    string             `toml:"theme,omitempty"`
	Icons    string             `toml:"icons,omitempty"`
	Profiles map[string]Profile `toml:"profiles"`

	path    string // resolved file path; not serialized
	existed bool   // whether the config was read from an existing file
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

// Existed reports whether the config was read from an existing file on disk,
// as opposed to a freshly-minted in-memory default. Callers use this to write
// out a starter config on first run.
func (c *Config) Existed() bool { return c.existed }

// IconsMode returns the configured glyph capability rung, giving precedence
// to the OPENFGA_ICONS environment variable over the on-disk value.
func (c *Config) IconsMode() string {
	if v := os.Getenv("OPENFGA_ICONS"); v != "" {
		return v
	}
	return c.Icons
}

// resolvePath computes the platform-appropriate config file path.
func resolvePath() (string, error) {
	scope := gap.NewScope(gap.User, appName)
	p, err := scope.ConfigPath(configFileName)
	if err != nil {
		return "", err
	}
	return p, nil
}

// Load reads the config from disk. If the file does not exist, a fresh default
// config is returned (not yet written to disk).
func Load() (*Config, error) {
	path, err := resolvePath()
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("parse config %s: %w", path, err)
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
	return cfg, nil
}

// Save writes the config to disk, creating parent directories as needed. The
// file holds API tokens and client secrets, so it is written with 0600
// permissions (dir 0700) via a temp file + atomic rename, ensuring the secrets
// are never world-readable even briefly and never left truncated on error.
func (c *Config) Save() error {
	if c.path == "" {
		p, err := resolvePath()
		if err != nil {
			return err
		}
		c.path = p
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
		return fmt.Errorf("%w: %q", ErrNoProfile, name)
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
		return fmt.Errorf("%w: %q", ErrNoProfile, name)
	}
	c.Active = name
	return nil
}

// Overrides carries flag-supplied values that take precedence over everything.
// An empty string means "not set".
type Overrides struct {
	Profile string
	StoreID string
	ModelID string
}

// Resolve merges, in increasing order of precedence: profile values, OPENFGA_*
// environment variables, then flag overrides.
func (c *Config) Resolve(o Overrides) (Resolved, error) {
	name := c.Active
	if o.Profile != "" {
		name = o.Profile
	}
	p, ok := c.Profiles[name]
	if !ok {
		return Resolved{}, fmt.Errorf("%w: %q", ErrNoProfile, name)
	}

	r := Resolved{
		Profile: name,
		APIURL:  p.APIURL,
		StoreID: p.StoreID,
		ModelID: p.ModelID,
		Auth:    p.ResolvedAuth(),
	}

	// Environment overrides.
	if v := os.Getenv("OPENFGA_API_URL"); v != "" {
		r.APIURL = v
	}
	if v := os.Getenv("OPENFGA_STORE_ID"); v != "" {
		r.StoreID = v
	}
	if v := os.Getenv("OPENFGA_MODEL_ID"); v != "" {
		r.ModelID = v
	}
	if v := os.Getenv("OPENFGA_AUTHORIZATION_MODEL_ID"); v != "" {
		r.ModelID = v
	}
	// A token from the environment overrides the profile's token, but only when
	// the profile isn't using an OAuth flow (client_credentials/private_key_jwt).
	// Silently swapping a configured OAuth flow for a bare token would disable
	// the profile's real auth without any signal; switch profiles for that.
	if v := os.Getenv("OPENFGA_API_TOKEN"); v != "" {
		if r.Auth.Method == "" || r.Auth.Method == AuthNone || r.Auth.Method == AuthAPIToken {
			r.Auth = Auth{Method: AuthAPIToken, Token: v}
		}
	}

	// Flag overrides (highest precedence). API URL and auth come from the
	// active profile (and OPENFGA_* env vars); switch profiles to change them.
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
