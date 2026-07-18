// Package config loads and persists ofga configuration: a set of named
// connection profiles (contexts) plus the name of the active one. Values are
// stored as TOML in the platform config directory and can be overridden by
// OPENFGA_* environment variables and command-line flags.
package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	gap "github.com/muesli/go-app-paths"
	"github.com/zalando/go-keyring"

	"github.com/sergiught/openfga-cli/internal/readlimit"
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

// Validate reports incomplete authentication settings before the SDK turns
// them into a remote token-exchange failure or silently sends no credential.
func (a Auth) Validate() error {
	switch a.Method {
	case "", AuthNone:
		return nil
	case AuthAPIToken:
		if a.Token == "" {
			return errors.New("api_token authentication requires a token")
		}
	case AuthClientCredentials:
		switch {
		case a.ClientID == "":
			return errors.New("client_credentials authentication requires client_id")
		case a.ClientSecret == "":
			return errors.New("client_credentials authentication requires client_secret")
		case a.TokenURL == "":
			return errors.New("client_credentials authentication requires token_url")
		}
	case AuthPrivateKeyJWT:
		switch {
		case a.ClientID == "":
			return errors.New("private_key_jwt authentication requires client_id")
		case a.TokenURL == "":
			return errors.New("private_key_jwt authentication requires token_url")
		case a.PrivateKey == "" && a.KeyFile == "":
			return errors.New("private_key_jwt authentication requires private_key or key_file")
		}
	default:
		return fmt.Errorf("unknown auth method %q (use none, api_token, client_credentials or private_key_jwt)", a.Method)
	}
	return nil
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

// activeSecretFields returns only the keyring fields used by the selected auth
// method. Stale fields from an older method must never make an otherwise valid
// profile depend on an unavailable keyring.
func (a *Auth) activeSecretFields() []secretField {
	switch a.Method {
	case AuthAPIToken:
		return []secretField{{"token", &a.Token}}
	case AuthClientCredentials:
		return []secretField{{"client_secret", &a.ClientSecret}}
	case AuthPrivateKeyJWT:
		return []secretField{{"private_key", &a.PrivateKey}}
	default:
		return nil
	}
}

// ConfiguredSecretFields lists keyring-managed fields present on the profile.
func (a Auth) ConfiguredSecretFields() []string {
	var fields []string
	for _, sf := range a.secretFields() {
		if *sf.ptr != "" {
			fields = append(fields, sf.name)
		}
	}
	return fields
}

// Config is the on-disk configuration document.
// SchemaVersion is the current on-disk config format version, written on Save
// and available to gate future migrations.
const SchemaVersion = 1

type Config struct {
	Version                   int                 `toml:"version"`
	Active                    string              `toml:"active_profile"`
	Theme                     string              `toml:"theme,omitempty"`
	Icons                     string              `toml:"icons,omitempty"`
	PendingCredentialCleanups []CredentialCleanup `toml:"pending_credential_cleanup,omitempty" json:"-"`
	Profiles                  map[string]Profile  `toml:"profiles"`

	path    string // resolved file path; not serialized
	existed bool   // whether the config was read from an existing file
	loadErr error  // a deferred parse/version error; nil when the file loaded cleanly

	fingerprint    [sha256.Size]byte
	fingerprintSet bool
}

// CredentialCleanup is a durable, config-scoped request to remove obsolete
// keyring fields. It makes a failed post-save cleanup explicitly retryable.
type CredentialCleanup struct {
	Profile string   `toml:"profile"`
	Fields  []string `toml:"fields"`
}

// Resolved is the fully merged, ready-to-use connection configuration after
// applying profile values, environment variables and flag overrides.
type Resolved struct {
	Profile string
	APIURL  string
	StoreID string
	ModelID string
	Auth    Auth

	// Notices holds non-fatal advisories gathered during resolution (e.g. an
	// environment token ignored under an OAuth profile, or an incomplete env
	// grant). The caller surfaces them on stderr; they never change the auth.
	Notices []string
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
	var err error
	path, err = canonicalConfigPath(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}

	cfg := New()
	cfg.path = path

	data, err := readlimit.File(path, readlimit.Config, "config file")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg.fingerprint = sha256.Sum256(data)
	cfg.fingerprintSet = true

	// Reset profiles so the file is authoritative, then decode.
	cfg.Profiles = map[string]Profile{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		broken := New()
		broken.path = path
		broken.existed = true
		broken.fingerprint = cfg.fingerprint
		broken.fingerprintSet = true
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
	unlock, err := c.lockForSave()
	if err != nil {
		return err
	}
	defer unlock()
	return c.saveLocked()
}

// SaveWithSecretCleanup saves the config and removes the named profile's
// credentials before releasing the same cross-process lock. saved reports
// whether the config was durably replaced when cleanup itself fails.
func (c *Config) SaveWithSecretCleanup(profile string, all bool, expected ...string) (saved bool, err error) {
	unlock, err := c.lockForSave()
	if err != nil {
		return false, err
	}
	defer unlock()
	previousPending := append([]CredentialCleanup(nil), c.PendingCredentialCleanups...)
	fields, err := cleanupFields(all, expected)
	if err != nil {
		return false, err
	}
	c.queueCredentialCleanup(profile, fields)
	if err := c.saveLocked(); err != nil {
		if saveWasCommitted(err) {
			return true, err
		}
		c.PendingCredentialCleanups = previousPending
		return false, err
	}
	if len(fields) == 0 {
		return true, nil
	}
	if _, err := c.retryCredentialCleanupLocked(); err != nil {
		return true, err
	}
	return true, nil
}

// RetryCredentialCleanup retries all durable cleanup requests while holding
// the same cross-process lock used for config writes.
func (c *Config) RetryCredentialCleanup() (int, error) {
	unlock, err := c.lockForSave()
	if err != nil {
		return len(c.PendingCredentialCleanups), err
	}
	defer unlock()
	if len(c.PendingCredentialCleanups) == 0 {
		return 0, nil
	}
	// Verify the file fingerprint under the lock before deleting anything.
	// Another process may have reintroduced this profile/field since load.
	if err := c.saveLocked(); err != nil {
		return len(c.PendingCredentialCleanups), err
	}
	return c.retryCredentialCleanupLocked()
}

func cleanupFields(all bool, expected []string) ([]string, error) {
	if all {
		expected = secretFieldNames
	}
	seen := make(map[string]bool, len(expected))
	fields := make([]string, 0, len(expected))
	for _, field := range expected {
		if !isSecretField(field) {
			return nil, fmt.Errorf("unknown secret field %q", field)
		}
		if !seen[field] {
			seen[field] = true
			fields = append(fields, field)
		}
	}
	return fields, nil
}

func isSecretField(field string) bool {
	for _, candidate := range secretFieldNames {
		if field == candidate {
			return true
		}
	}
	return false
}

func (c *Config) queueCredentialCleanup(profile string, fields []string) {
	if len(fields) == 0 {
		return
	}
	for i := range c.PendingCredentialCleanups {
		pending := &c.PendingCredentialCleanups[i]
		if pending.Profile != profile {
			continue
		}
		merged, _ := cleanupFields(false, append(pending.Fields, fields...))
		pending.Fields = merged
		return
	}
	c.PendingCredentialCleanups = append(c.PendingCredentialCleanups, CredentialCleanup{
		Profile: profile,
		Fields:  append([]string(nil), fields...),
	})
}

func (c *Config) retryCredentialCleanupLocked() (int, error) {
	original := c.PendingCredentialCleanups
	var (
		remaining []CredentialCleanup
		errs      []error
	)
	for _, pending := range c.PendingCredentialCleanups {
		fields, err := cleanupFields(false, pending.Fields)
		if err != nil {
			errs = append(errs, fmt.Errorf("cleanup profile %q: %w", pending.Profile, err))
			remaining = append(remaining, pending)
			continue
		}
		var retryFields []string
		for _, field := range fields {
			if c.profileUsesSecret(pending.Profile, field) {
				continue
			}
			if err := c.deleteSecret(pending.Profile, field); err != nil {
				errs = append(errs, fmt.Errorf("delete %s for profile %q: %w", field, pending.Profile, err))
				retryFields = append(retryFields, field)
			}
		}
		if len(retryFields) > 0 {
			remaining = append(remaining, CredentialCleanup{Profile: pending.Profile, Fields: retryFields})
		}
	}

	changed := len(remaining) != len(c.PendingCredentialCleanups)
	if !changed {
		for i := range remaining {
			if remaining[i].Profile != c.PendingCredentialCleanups[i].Profile ||
				!equalStrings(remaining[i].Fields, c.PendingCredentialCleanups[i].Fields) {
				changed = true
				break
			}
		}
	}
	c.PendingCredentialCleanups = remaining
	if changed {
		if err := c.saveLocked(); err != nil {
			errs = append(errs, fmt.Errorf("record credential cleanup progress: %w", err))
			if !saveWasCommitted(err) {
				c.PendingCredentialCleanups = original
				return len(original), errors.Join(errs...)
			}
			return len(remaining), errors.Join(errs...)
		}
	}
	return len(remaining), errors.Join(errs...)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (c *Config) profileUsesSecret(profile, field string) bool {
	p, ok := c.Profiles[profile]
	if !ok {
		return false
	}
	for _, sf := range p.Auth.secretFields() {
		if sf.name == field {
			return *sf.ptr != ""
		}
	}
	return false
}

func (c *Config) lockForSave() (func(), error) {
	// Refuse to overwrite a file we could not parse (or that a newer ofga
	// wrote): replacing it with the in-memory defaults would destroy the user's
	// real config. Callers surface this instead of silently clobbering.
	if c.loadErr != nil {
		return nil, c.loadErr
	}
	if c.path == "" {
		p, err := resolvePath()
		if err != nil {
			return nil, err
		}
		c.path, err = canonicalConfigPath(p)
		if err != nil {
			return nil, fmt.Errorf("resolve config path: %w", err)
		}
	}
	if c.Version == 0 {
		c.Version = SchemaVersion
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	unlock, err := lockConfig(c.path)
	if err != nil {
		return nil, err
	}
	return unlock, nil
}

func (c *Config) saveLocked() error {
	if c.existed && c.fingerprintSet {
		current, err := readlimit.File(c.path, readlimit.Config, "config file")
		if err != nil {
			return fmt.Errorf("check config before save: %w", err)
		}
		if sha256.Sum256(current) != c.fingerprint {
			return fmt.Errorf("config %s changed on disk since it was loaded; reload it and retry", c.path)
		}
	} else if info, err := os.Stat(c.path); err == nil && info.Mode().IsRegular() {
		return fmt.Errorf("config %s was created by another process since this command started; reload it and retry", c.path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check config before save: %w", err)
	}

	// Move any real secret values into the OS keyring, leaving only the
	// sentinel in the file. A value already equal to the sentinel came from
	// disk and is left alone.
	var staged []stagedSecret
	fail := func(err error) error {
		return c.rollbackStagedSecrets(staged, err)
	}
	for name, p := range c.Profiles {
		auth := p.Auth
		for _, sf := range auth.secretFields() {
			if *sf.ptr == "" || *sf.ptr == keyringSentinel {
				continue
			}
			if !secretsAvailable() {
				return fail(fmt.Errorf("cannot store %s for profile %q securely: the OS keyring is unavailable. "+
					"Use the matching process-scoped --auth-*-file flag instead, "+
					"or use key_file for private_key_jwt", sf.name, name))
			}
			old, getErr := scopedSecretGet(c.path, name, sf.name)
			hadOld := getErr == nil
			if getErr != nil && !errors.Is(getErr, keyring.ErrNotFound) {
				return fail(fmt.Errorf("read existing %s for profile %q from keyring: %w", sf.name, name, getErr))
			}
			original := *sf.ptr
			if err := scopedSecretSet(c.path, name, sf.name, *sf.ptr); err != nil {
				return fail(secretStoreError(sf.name, name, err))
			}
			staged = append(staged, stagedSecret{
				profile: name, field: sf.name, original: original, old: old, hadOld: hadOld,
			})
			*sf.ptr = keyringSentinel
		}
		p.Auth = auth
		c.Profiles[name] = p
	}

	tmp, err := os.CreateTemp(filepath.Dir(c.path), configFileName+".*.tmp")
	if err != nil {
		return fail(fmt.Errorf("create config file: %w", err))
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fail(fmt.Errorf("secure config file: %w", err))
	}
	digest := sha256.New()
	enc := toml.NewEncoder(io.MultiWriter(tmp, digest))
	enc.Indent = "  "
	if err := enc.Encode(c); err != nil {
		tmp.Close()
		return fail(fmt.Errorf("encode config: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fail(fmt.Errorf("sync config file: %w", err))
	}
	if err := tmp.Close(); err != nil {
		return fail(fmt.Errorf("write config file: %w", err))
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		return fail(fmt.Errorf("write config file: %w", err))
	}
	copy(c.fingerprint[:], digest.Sum(nil))
	c.fingerprintSet = true
	c.existed = true
	if err := syncConfigDir(filepath.Dir(c.path)); err != nil {
		// The atomic replacement happened, so rolling keyring entries back
		// would make the new sentinel-bearing file reference old credentials.
		// Mark this distinctly so cleanup callers never delete credentials
		// when the directory entry's crash durability is uncertain.
		return &committedSaveError{err: fmt.Errorf("sync config directory: %w", err)}
	}
	return nil
}

// secretStoreError explains a failed OS-keyring write. A locked keyring gets
// specific, recoverable guidance because its raw driver message ("Cannot create
// an item in a locked collection") is opaque and secretsAvailable cannot flag it
// in advance; any other failure keeps the plain wrap (and its cause).
func secretStoreError(field, profile string, err error) error {
	if keyringLocked(err) {
		return fmt.Errorf("cannot store %s for profile %q: the OS login keyring is locked. "+
			"Unlock it (e.g. via your desktop keyring or Seahorse) and retry, or avoid "+
			"persisting it with the process-scoped --auth-%s-file flag or the %s env var",
			field, profile, authFileFlagName(field), authEnvVar(field))
	}
	return fmt.Errorf("store %s for profile %q in keyring: %w", field, profile, err)
}

// authFileFlagName maps a keyring field to its --auth-*-file flag stem so the
// guidance points at the exact flag (token → --auth-token-file, etc.).
func authFileFlagName(field string) string {
	switch field {
	case "client_secret":
		return "client-secret"
	case "private_key":
		return "private-key"
	default:
		return "token"
	}
}

// authEnvVar maps a keyring field to the environment variable that supplies it
// without touching the keyring (mirrors secretEnvOverride).
func authEnvVar(field string) string {
	switch field {
	case "client_secret":
		return "OPENFGA_CLIENT_SECRET"
	case "private_key":
		return "OPENFGA_KEY_FILE"
	default:
		return "OPENFGA_API_TOKEN"
	}
}

type committedSaveError struct{ err error }

func (e *committedSaveError) Error() string { return e.err.Error() }
func (e *committedSaveError) Unwrap() error { return e.err }

func saveWasCommitted(err error) bool {
	var committed *committedSaveError
	return errors.As(err, &committed)
}

// SaveWasCommitted reports whether Save replaced the config before a
// post-rename durability sync failed. Callers must retain their in-memory
// changes in this case rather than rolling back over the replaced file.
func SaveWasCommitted(err error) bool { return saveWasCommitted(err) }

type stagedSecret struct {
	profile  string
	field    string
	original string
	old      string
	hadOld   bool
}

func (c *Config) rollbackStagedSecrets(staged []stagedSecret, cause error) error {
	var rollbackErrs []error
	for i := len(staged) - 1; i >= 0; i-- {
		s := staged[i]
		var err error
		if s.hadOld {
			err = scopedSecretSet(c.path, s.profile, s.field, s.old)
		} else {
			err = scopedSecretDelete(c.path, s.profile, s.field)
		}
		if err != nil {
			rollbackErrs = append(rollbackErrs,
				fmt.Errorf("restore %s for profile %q in keyring: %w", s.field, s.profile, err))
		}
		p, ok := c.Profiles[s.profile]
		if ok {
			setSecretField(&p.Auth, s.field, s.original)
			c.Profiles[s.profile] = p
		}
	}
	if len(rollbackErrs) == 0 {
		return cause
	}
	return errors.Join(append([]error{cause}, rollbackErrs...)...)
}

func setSecretField(a *Auth, field, value string) {
	switch field {
	case "token":
		a.Token = value
	case "client_secret":
		a.ClientSecret = value
	case "private_key":
		a.PrivateKey = value
	}
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

func (c *Config) deleteSecret(profile, field string) error {
	if !isSecretField(field) {
		return fmt.Errorf("unknown secret field %q", field)
	}
	path, err := c.secretPath()
	if err != nil {
		return err
	}
	return scopedSecretDelete(path, profile, field)
}

func (c *Config) secretPath() (string, error) {
	if c.path != "" {
		return c.path, nil
	}
	return resolvePath()
}

func (c *Config) readSecret(profile, field string) (string, error) {
	path, err := c.secretPath()
	if err != nil {
		return "", err
	}
	value, err := scopedSecretGet(path, profile, field)
	if err == nil || !errors.Is(err, keyring.ErrNotFound) {
		return value, err
	}

	// Old releases used profile.field for every config. Copy a legacy value
	// into this config's namespace on first read, but never delete the shared
	// entry: another --config file may still depend on it.
	value, err = legacySecretGet(profile, field)
	if err != nil {
		return "", err
	}
	_ = scopedSecretSet(path, profile, field, value)
	return value, nil
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
	Profile      string
	APIURL       string
	StoreID      string
	ModelID      string
	APIToken     string
	ClientSecret string
	PrivateKey   string
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

// secretEnvOverride returns the environment value that would override a
// keyring-managed secret field, or "" if none is set. It mirrors the env
// blocks in Resolve so that, for a sentinel field, an env override bypasses
// the keyring entirely. private_key's non-keyring path is key_file, so it
// maps to OPENFGA_KEY_FILE.
func secretEnvOverride(field string) string {
	switch field {
	case "token":
		return firstEnv("OPENFGA_API_TOKEN", "FGA_API_TOKEN")
	case "client_secret":
		return firstEnv("OPENFGA_CLIENT_SECRET", "FGA_CLIENT_SECRET")
	case "private_key":
		return firstEnv("OPENFGA_KEY_FILE", "FGA_KEY_FILE")
	default:
		return ""
	}
}

func secretRuntimeOverride(o Overrides, field string) string {
	switch field {
	case "token":
		return o.APIToken
	case "client_secret":
		return o.ClientSecret
	case "private_key":
		return o.PrivateKey
	default:
		return ""
	}
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

// ActiveName returns the profile name selected by the standard precedence:
// the --profile override, else OPENFGA_PROFILE/FGA_PROFILE, else the active
// profile on disk. It does not check that the profile exists. Callers that need
// the label of the connection Resolve() actually made must use this so the two
// never drift.
func (c *Config) ActiveName(o Overrides) string {
	name := c.Active
	if v := firstEnv("OPENFGA_PROFILE", "FGA_PROFILE"); v != "" {
		name = v
	}
	if o.Profile != "" {
		name = o.Profile
	}
	return name
}

// Resolve merges, in increasing order of precedence: profile values, OPENFGA_*
// (or FGA_*) environment variables, then flag overrides.
func (c *Config) Resolve(o Overrides) (Resolved, error) {
	// A file that failed to parse (or was written by a newer ofga) surfaces here,
	// so commands needing a profile fail clearly while `config path` still works.
	if c.loadErr != nil {
		return Resolved{}, c.loadErr
	}
	name := c.ActiveName(o)
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

	// Replace keyring sentinels with the real values before env overrides
	// (which still win). name is the resolved profile name.
	secretFields := r.Auth.activeSecretFields()
	if o.APIToken != "" {
		// A process-scoped bearer token replaces the entire auth flow, so none
		// of the profile's now-irrelevant keyring entries should be required.
		r.Auth = Auth{}
		secretFields = nil
	}
	for _, sf := range secretFields {
		if *sf.ptr != keyringSentinel {
			continue
		}
		// An env override for this secret bypasses the keyring entirely (the
		// headless/CI escape hatch). Clear the sentinel so the env block below
		// fills it; clearing to "" — not leaving the sentinel — matters for
		// private_key, whose non-empty value would otherwise be parsed as
		// literal PEM by client.go's signingKeyPEM.
		if secretRuntimeOverride(o, sf.name) != "" || secretEnvOverride(sf.name) != "" {
			*sf.ptr = ""
			continue
		}
		if !secretsAvailable() {
			return Resolved{}, fmt.Errorf("%s for profile %q is stored in the OS keyring, which is unavailable here; "+
				"use the matching --auth-*-file flag or compatibility environment variable instead", sf.name, name)
		}
		v, err := c.readSecret(name, sf.name)
		if err != nil {
			return Resolved{}, fmt.Errorf("%s for profile %q is not in this machine's keyring; re-set it with "+
				"`ofga profiles set %s` or use the matching --auth-*-file flag", sf.name, name, sf.name)
		}
		*sf.ptr = v
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
	envToken := firstEnv("OPENFGA_API_TOKEN", "FGA_API_TOKEN")
	if envToken != "" {
		if r.Auth.Method == "" || r.Auth.Method == AuthNone || r.Auth.Method == AuthAPIToken {
			r.Auth = Auth{Method: AuthAPIToken, Token: envToken}
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
	var partialGrantMissing []string
	if pm := p.ResolvedAuth().Method; pm == "" || pm == AuthClientCredentials {
		envClientID := firstEnv("OPENFGA_CLIENT_ID", "FGA_CLIENT_ID")
		envClientSecret := firstEnv("OPENFGA_CLIENT_SECRET", "FGA_CLIENT_SECRET")
		envTokenURL := firstEnv("OPENFGA_TOKEN_URL", "FGA_TOKEN_URL")
		clientID := envClientID
		clientSecret := envClientSecret
		if clientSecret == "" {
			clientSecret = o.ClientSecret
		}
		tokenURL := envTokenURL
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
		} else if envClientID != "" || envClientSecret != "" || envTokenURL != "" {
			// At least one env grant field is set but the trio is incomplete, so
			// the grant is dropped. Record which fields are missing so the caller
			// can warn instead of failing later with a bare 401.
			if clientID == "" {
				partialGrantMissing = append(partialGrantMissing, "OPENFGA_CLIENT_ID")
			}
			if clientSecret == "" {
				partialGrantMissing = append(partialGrantMissing, "OPENFGA_CLIENT_SECRET")
			}
			if tokenURL == "" {
				partialGrantMissing = append(partialGrantMissing, "OPENFGA_TOKEN_URL")
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
	if o.APIToken != "" {
		r.Auth = Auth{Method: AuthAPIToken, Token: o.APIToken}
	}
	if o.ClientSecret != "" {
		if r.Auth.Method != AuthClientCredentials {
			return Resolved{}, fmt.Errorf("--auth-client-secret-file requires a client_credentials profile or environment configuration")
		}
		r.Auth.ClientSecret = o.ClientSecret
	}
	if o.PrivateKey != "" {
		if r.Auth.Method != AuthPrivateKeyJWT {
			return Resolved{}, fmt.Errorf("--auth-private-key-file requires a private_key_jwt profile")
		}
		r.Auth.PrivateKey = o.PrivateKey
		r.Auth.KeyFile = ""
	}

	if r.APIURL == "" {
		r.APIURL = DefaultAPIURL
	}

	// Non-fatal advisories, surfaced by the caller on stderr. An env token under
	// an OAuth profile was ignored above; an incomplete env grant left auth off.
	if envToken != "" && (r.Auth.Method == AuthClientCredentials || r.Auth.Method == AuthPrivateKeyJWT) {
		r.Notices = append(r.Notices, fmt.Sprintf(
			"note: OPENFGA_API_TOKEN is set but profile %q uses %s auth; the token was ignored", r.Profile, r.Auth.Method))
	}
	if len(partialGrantMissing) > 0 && (r.Auth.Method == "" || r.Auth.Method == AuthNone) {
		r.Notices = append(r.Notices, fmt.Sprintf(
			"note: partial OAuth env grant — %s; falling back to no authentication", missingVarsPhrase(partialGrantMissing)))
	}
	return r, nil
}

// missingVarsPhrase renders one or more environment variable names as a short
// clause, e.g. "OPENFGA_CLIENT_SECRET is not set" or "OPENFGA_CLIENT_ID and
// OPENFGA_TOKEN_URL are not set".
func missingVarsPhrase(names []string) string {
	if len(names) == 1 {
		return names[0] + " is not set"
	}
	return strings.Join(names, " and ") + " are not set"
}
