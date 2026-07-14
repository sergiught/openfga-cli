package version

import (
	"strings"
	"testing"
)

// DOC-4: an ldflags-set Version is authoritative; the build-info fallback only
// applies to the "dev" placeholder.
func TestResolveVersion(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	Version = "v1.2.3"
	if got := resolveVersion(); got != "v1.2.3" {
		t.Errorf("resolveVersion() = %q, want the ldflags value v1.2.3", got)
	}
	if s := String(); !strings.Contains(s, "v1.2.3") {
		t.Errorf("String() = %q, want it to include v1.2.3", s)
	}

	// With the default placeholder, the resolver never returns "" — it falls
	// back to build info when available, else to the placeholder itself.
	Version = "dev"
	if got := resolveVersion(); got == "" {
		t.Error("resolveVersion() returned empty for the dev placeholder")
	}
}
