package mapping

import (
	"errors"
	"fmt"
	"os"

	"github.com/sergiught/openfga-cli/internal/clierr"
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
		// 'ofga mappings <file>' is the interactive workspace, so the hint says a
		// person runs it; scripts and agents must pass an existing file.
		return nil, nil, clierr.WithKind(clierr.KindNotFound, clierr.WithHint(
			"pass an existing mapping file; to create one, a person runs 'ofga mappings "+shellQuote(path)+"' in a terminal (it's interactive)",
			fmt.Errorf("%s not found", path)))
	case err != nil:
		return nil, nil, err
	}
	return f, nil, nil
}
