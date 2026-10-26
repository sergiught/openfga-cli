package model

import (
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/outputschema"
)

// OutputTypes maps each model command's full path to what it prints under
// --json. Write is hand-written: it prints a dry-run preview or the write
// response depending on --dry-run.
func OutputTypes() map[string]any {
	return map[string]any{
		"ofga model write":  outputschema.Handwritten{},
		"ofga model list":   []openfga.AuthorizationModel{},
		"ofga model get":    openfga.AuthorizationModel{},
		"ofga model latest": openfga.AuthorizationModel{},
		"ofga model graph":  fga.Graph{},
		"ofga model test":   modeltest.Results{},
		// schema prints a JSON Schema document; init prints no JSON.
		"ofga model test schema": map[string]any{},
	}
}
