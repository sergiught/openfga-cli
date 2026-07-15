package config

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// keyringService namespaces this CLI's entries in the OS keyring.
const keyringService = "openfga-cli"

// keyringSentinel is the placeholder written to config.toml for a secret whose
// real value lives in the OS keyring.
//
//nolint:unused // consumed by the Save/Resolve tasks that follow this one
const keyringSentinel = "keyring:managed"

// secretField pairs a keyring field name with the Auth field it maps to.
type secretField struct {
	name string
	ptr  *string
}

func secretAccount(profile, field string) string { return profile + "." + field }

// secretsAvailable reports whether the OS keyring backend is usable. It probes
// once with a lookup: a hit or a clean "not found" means the backend answered;
// any other error (no Secret Service / dbus, locked keychain) means unusable.
func secretsAvailable() bool {
	_, err := keyring.Get(keyringService, "__probe__")
	return err == nil || errors.Is(err, keyring.ErrNotFound)
}

func secretGet(profile, field string) (string, error) {
	return keyring.Get(keyringService, secretAccount(profile, field))
}

func secretSet(profile, field, value string) error {
	return keyring.Set(keyringService, secretAccount(profile, field), value)
}

// secretDelete removes an entry, treating "not found" as success.
func secretDelete(profile, field string) error {
	err := keyring.Delete(keyringService, secretAccount(profile, field))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
