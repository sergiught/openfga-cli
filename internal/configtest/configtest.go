// Package configtest isolates the CLI's on-disk config so a test never reads
// or writes the developer's real one.
package configtest

import (
	"path/filepath"
	"testing"
)

// Isolate points the CLI's config at a fresh file under t.TempDir() for the
// duration of t, and returns that path.
//
// It sets OPENFGA_CONFIG rather than XDG_CONFIG_HOME because only the former is
// honored on every platform: on macOS the default config lives under
// ~/Library/Application Support and XDG_CONFIG_HOME is ignored entirely, so a
// test that relies on it silently falls through to the real config — and the
// TUI writes config on exit.
func Isolate(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("OPENFGA_CONFIG", path)
	return path
}
