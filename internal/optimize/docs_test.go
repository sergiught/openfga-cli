package optimize

import (
	"os"
	"runtime/debug"
	"strings"
	"testing"
)

// TestDocsPinOpenFGAVersion keeps the OpenFGA version named in the guide and in
// lattice.go equal to the compiled github.com/openfga/openfga dependency.
func TestDocsPinOpenFGAVersion(t *testing.T) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build info")
	}
	version := ""
	for _, dep := range bi.Deps {
		if dep.Path == "github.com/openfga/openfga" {
			version = dep.Version
		}
	}
	if version == "" {
		t.Fatal("github.com/openfga/openfga not found in build info")
	}
	for _, path := range []string{
		"../../docs/site/src/content/docs/guide/model-optimizer.mdx",
		"lattice.go",
	} {
		t.Run(path, func(t *testing.T) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if want := "OpenFGA " + version; !strings.Contains(string(b), want) {
				t.Errorf("%s does not contain %q", path, want)
			}
		})
	}
}
