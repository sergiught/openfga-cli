package sandbox

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// samplesFS holds the generated workspaces plus their LICENSE/NOTICE/README.
// See samples/README.md for provenance and the refresh command.
//
//go:embed samples
var samplesFS embed.FS

const samplesDir = "samples"

var sampleNames = []string{
	"github", "gdrive", "slack", "custom-roles",
	"multitenant-rbac", "temporal-access", "expenses",
}

// SampleNames returns the sample store names in creation order.
func SampleNames() []string {
	return append([]string(nil), sampleNames...)
}

// ExtractSamples writes each embedded workspace to dir/<name> and returns
// name -> workspace root.
func ExtractSamples(dir string) (map[string]string, error) {
	roots := make(map[string]string, len(sampleNames))

	for _, name := range sampleNames {
		base := path.Join(samplesDir, name)
		dest := filepath.Join(dir, name)

		err := fs.WalkDir(samplesFS, base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			rel := "."
			if p != base {
				rel = p[len(base)+1:] // drop "base/"
			}

			if !fs.ValidPath(rel) {
				return fmt.Errorf("invalid embedded path %q", p)
			}

			target := filepath.Join(dest, filepath.FromSlash(rel))

			if d.IsDir() {
				return os.MkdirAll(target, 0o700)
			}

			data, err := samplesFS.ReadFile(p)
			if err != nil {
				return err
			}

			return os.WriteFile(target, data, 0o600)
		})
		if err != nil {
			return nil, fmt.Errorf("extract sample %q: %w", name, err)
		}

		roots[name] = dest
	}

	return roots, nil
}
