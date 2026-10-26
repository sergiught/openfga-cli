package base

import (
	"maps"

	"github.com/sergiught/openfga-cli/internal/command/model"
	"github.com/sergiught/openfga-cli/internal/command/store"
	"github.com/sergiught/openfga-cli/internal/command/tuple"
)

// OutputTypes merges every command package's OutputTypes plus the root
// command's own, keyed by full command path. It feeds tools/schemagen.
func OutputTypes() map[string]any {
	all := map[string]any{}
	for _, m := range []map[string]any{
		store.OutputTypes(),
		tuple.OutputTypes(),
		model.OutputTypes(),
	} {
		maps.Copy(all, m)
	}
	return all
}
