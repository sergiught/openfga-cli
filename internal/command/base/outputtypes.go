package base

import (
	"maps"

	"github.com/sergiught/openfga-cli/internal/command/api"
	"github.com/sergiught/openfga-cli/internal/command/assertions"
	"github.com/sergiught/openfga-cli/internal/command/configcmd"
	"github.com/sergiught/openfga-cli/internal/command/docs"
	"github.com/sergiught/openfga-cli/internal/command/mapping"
	"github.com/sergiught/openfga-cli/internal/command/model"
	"github.com/sergiught/openfga-cli/internal/command/profiles"
	"github.com/sergiught/openfga-cli/internal/command/query"
	"github.com/sergiught/openfga-cli/internal/command/store"
	"github.com/sergiught/openfga-cli/internal/command/tuple"
	"github.com/sergiught/openfga-cli/internal/outputschema"
)

// OutputTypes merges every command package's OutputTypes plus the root
// command's own, keyed by full command path. It feeds tools/schemagen.
func OutputTypes() map[string]any {
	// commands is reflected from the catalog type; version is hand-written
	// because it prints a map.
	all := map[string]any{
		"ofga commands": catalog{},
		"ofga version":  outputschema.Handwritten{},
	}
	for _, m := range []map[string]any{
		store.OutputTypes(),
		tuple.OutputTypes(),
		model.OutputTypes(),
		query.OutputTypes(),
		assertions.OutputTypes(),
		profiles.OutputTypes(),
		configcmd.OutputTypes(),
		mapping.OutputTypes(),
		api.OutputTypes(),
		docs.OutputTypes(),
	} {
		maps.Copy(all, m)
	}
	return all
}
