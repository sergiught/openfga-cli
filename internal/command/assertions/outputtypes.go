package assertions

import (
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/outputschema"
)

// OutputTypes maps each assertions command's full path to what it prints under
// --json. test and write are hand-written: test builds a map, and write prints
// a different shape under --dry-run.
func OutputTypes() map[string]any {
	return map[string]any{
		"ofga assertions read":  openfga.ReadAssertionsResponse{},
		"ofga assertions test":  outputschema.Handwritten{},
		"ofga assertions write": outputschema.Handwritten{},
	}
}
