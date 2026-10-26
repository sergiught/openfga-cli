package tuple

import (
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/outputschema"
)

// OutputTypes maps each tuple command's full path to what it prints under
// --json. Write and delete are hand-written: their output is a dry-run
// preview, a single-tuple count or a bulk result depending on the flags.
func OutputTypes() map[string]any {
	return map[string]any{
		"ofga tuples write":   outputschema.Handwritten{},
		"ofga tuples delete":  outputschema.Handwritten{},
		"ofga tuples read":    []openfga.Tuple{},
		"ofga tuples changes": []openfga.TupleChange{},
	}
}
