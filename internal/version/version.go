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

// serverModule is the OpenFGA server module the CLI links in and runs
// in-process for `ofga model test`.
const serverModule = "github.com/openfga/openfga"

// Server returns the version of the embedded OpenFGA server, read from the
// module list the Go toolchain records in the binary. Builds without that
// information report "unknown".
func Server() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return serverVersion(info)
}

// serverVersion picks the embedded server's module version out of build info,
// honouring a `replace` directive so a locally patched server reports the
// version actually built.
func serverVersion(info *debug.BuildInfo) string {
	for _, dep := range info.Deps {
		if dep == nil || dep.Path != serverModule {
			continue
		}
		if dep.Replace != nil && dep.Replace.Version != "" {
			return dep.Replace.Version
		}
		if dep.Version != "" {
			return dep.Version
		}
	}
	return "unknown"
}

// String returns a one-line, human-readable build description. The server
// version is qualified as "embedded" because it describes the OpenFGA library
// linked into this binary for `ofga model test`, not the server the active
// profile points at.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s, embedded openfga server %s)", resolveVersion(), Commit, Date, Server())
}
