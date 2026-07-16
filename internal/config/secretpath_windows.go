package config

import "strings"

// Windows config paths are case-insensitive by default. Normalize spelling so
// aliases such as C:\Users and c:\users resolve to one keyring namespace.
func normalizeSecretPathCase(path string) string { return strings.ToLower(path) }
