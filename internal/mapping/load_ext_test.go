package mapping_test

import (
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

// Lives outside package mapping because auth0 imports it.
func TestEveryRecipeIsEditable(t *testing.T) {
	for _, ev := range auth0.Catalog() {
		if !ev.Recipe.Maps() {
			continue
		}
		src, err := mapping.Marshal(&mapping.Document{Rules: []mapping.Rule{ev.Recipe.Rule}})
		if err != nil {
			t.Fatalf("%s: %v", ev.Type, err)
		}
		f, err := mapping.LoadBytes(ev.Type+".yaml", src)
		if err != nil {
			t.Fatalf("%s: %v", ev.Type, err)
		}
		if !f.Editable {
			t.Errorf("%s: not editable: %s", ev.Type, f.Reason)
		}
	}
}
