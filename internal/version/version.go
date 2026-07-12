// Package version holds build information, injected at release time via
// -ldflags. Defaults are placeholders for `go run`/`go install` builds.
package version

import "fmt"

var (
	// Version is the semantic version (e.g. v1.2.3), set by goreleaser.
	Version = "dev"
	// Commit is the short git SHA the binary was built from.
	Commit = "none"
	// Date is the build timestamp (RFC3339).
	Date = "unknown"
)

// String returns a one-line, human-readable build description.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
