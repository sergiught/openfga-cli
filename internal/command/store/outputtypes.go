package store

import (
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/outputschema"
)

// OutputTypes maps each store command's full path to what it prints under
// --json. Commands whose output varies by mode (--dry-run, --use) are
// hand-written.
func OutputTypes() map[string]any {
	return map[string]any{
		"ofga stores create": outputschema.Handwritten{},
		"ofga stores list":   []openfga.Store{},
		"ofga stores get":    openfga.Store{},
		"ofga stores delete": outputschema.Handwritten{},
	}
}
