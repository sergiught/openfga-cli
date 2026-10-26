package mapping

import (
	"errors"
	"fmt"
	"os"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// targetPath resolves the optional file argument.
func targetPath(args []string) string {
	if len(args) == 1 && args[0] != "" {
		return args[0]
	}
	return mapping.DefaultFile
}

// loadMappingFile loads a mapping file. A file with structural problems returns
// them as findings; a missing file returns an error naming the next step.
func loadMappingFile(path string) (*mapping.File, []mapping.Finding, error) {
	f, err := mapping.Load(path)
	var le *mapping.LoadError
	switch {
	case errors.As(err, &le):
		return nil, mapping.FindingsFrom(le.Diagnostics), nil
	case errors.Is(err, os.ErrNotExist):
		return nil, nil, fmt.Errorf("%s not found: pass a mapping file, or create one with 'ofga mappings %s'", path, shellQuote(path))
	case err != nil:
		return nil, nil, err
	}
	return f, nil, nil
}
