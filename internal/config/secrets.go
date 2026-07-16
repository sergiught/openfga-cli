package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// keyringService namespaces this CLI's entries in the OS keyring.
const keyringService = "openfga-cli"

// keyringSentinel is the placeholder written to config.toml for a secret whose
// real value lives in the OS keyring.
const keyringSentinel = "keyring:managed"

// secretField pairs a keyring field name with the Auth field it maps to.
type secretField struct {
	name string
	ptr  *string
}

var secretFieldNames = []string{"token", "client_secret", "private_key"}

func legacySecretAccount(profile, field string) string { return profile + "." + field }

func canonicalConfigPath(configPath string) (string, error) {
	normalized, err := filepath.Abs(configPath)
	if err != nil {
		return "", err
	}
	normalized = filepath.Clean(normalized)
	if resolved, resolveErr := filepath.EvalSymlinks(normalized); resolveErr == nil {
		normalized = resolved
	} else if resolvedParent, parentErr := filepath.EvalSymlinks(filepath.Dir(normalized)); parentErr == nil {
		// A new config file does not exist yet, but its parent may itself be a
		// symlink. Canonicalize that existing portion before deriving identity.
		normalized = filepath.Join(resolvedParent, filepath.Base(normalized))
	}
	return normalizeSecretPathCase(normalized), nil
}

func secretAccount(configPath, profile, field string) (string, error) {
	normalized, err := canonicalConfigPath(configPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(normalized))
	return "v2." + hex.EncodeToString(sum[:]) + "." + profile + "." + field, nil
}

// secretsAvailable reports whether the OS keyring backend is usable. It probes
// once with a lookup: a hit or a clean "not found" means the backend answered;
// any other error (no Secret Service / dbus, locked keychain) means unusable.
func secretsAvailable() bool {
	_, err := keyring.Get(keyringService, "__probe__")
	return err == nil || errors.Is(err, keyring.ErrNotFound)
}

func scopedSecretGet(configPath, profile, field string) (string, error) {
	account, err := secretAccount(configPath, profile, field)
	if err != nil {
		return "", err
	}
	return keyring.Get(keyringService, account)
}

func scopedSecretSet(configPath, profile, field, value string) error {
	account, err := secretAccount(configPath, profile, field)
	if err != nil {
		return err
	}
	return keyring.Set(keyringService, account, value)
}

// scopedSecretDelete removes only this config file's entry. Legacy unscoped
// entries are intentionally never deleted because another config may use them.
func scopedSecretDelete(configPath, profile, field string) error {
	account, err := secretAccount(configPath, profile, field)
	if err != nil {
		return err
	}
	err = keyring.Delete(keyringService, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

func legacySecretGet(profile, field string) (string, error) {
	return keyring.Get(keyringService, legacySecretAccount(profile, field))
}
