// Package version holds build information, injected at release time via
// -ldflags. Defaults are placeholders for `go run`/`go install` builds.
package version

import (
	"fmt"
	"runtime/debug"
)

var (
	// Version is the semantic version (e.g. v1.2.3), set by goreleaser.
	Version = "dev"
	// Commit is the short git SHA the binary was built from.
	Commit = "none"
	// Date is the build timestamp (RFC3339).
	Date = "unknown"
)

// resolveVersion returns the ldflags-set Version when present, otherwise falls
// back to the module version embedded by the Go toolchain for `go install`/`go
// run` builds (e.g. a tagged install reports "v1.2.3", a bare build "(devel)").
func resolveVersion() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return Version
}

// Resolved returns the version string, preferring the ldflags-set value and
// falling back to the module version for `go install`/`go run` builds. Use it
// wherever the version is reported (banner, `--json`) so every surface agrees.
func Resolved() string {
	return resolveVersion()
}

// String returns a one-line, human-readable build description.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", resolveVersion(), Commit, Date)
}
