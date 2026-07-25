package version

import (
	"runtime/debug"
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

// The embedded server version comes from the module list recorded in the
// binary; a `replace` wins over the required version, and a build without the
// server linked in reports "unknown" rather than an empty string.
func TestServerVersion(t *testing.T) {
	tests := []struct {
		name string
		deps []*debug.Module
		want string
	}{
		{
			name: "required version",
			deps: []*debug.Module{
				{Path: "github.com/openfga/api/proto", Version: "v0.0.1"},
				{Path: serverModule, Version: "v1.18.1"},
			},
			want: "v1.18.1",
		},
		{
			name: "replaced module",
			deps: []*debug.Module{
				{Path: serverModule, Version: "v1.18.1", Replace: &debug.Module{Path: serverModule, Version: "v1.19.0-rc1"}},
			},
			want: "v1.19.0-rc1",
		},
		{
			name: "server not linked in",
			deps: []*debug.Module{{Path: "github.com/spf13/cobra", Version: "v1.10.2"}},
			want: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := serverVersion(&debug.BuildInfo{Deps: tt.deps}); got != tt.want {
				t.Errorf("serverVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
