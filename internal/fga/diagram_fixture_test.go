package fga

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/openfga/language/pkg/go/transformer"
	"github.com/sergiught/go-openfga/openfga"
)

// largeModel loads the synthetic 22-type model used by the diagram
// performance and wrapping tests.
func largeModel(t testing.TB) *openfga.AuthorizationModel {
	t.Helper()

	dsl, err := os.ReadFile("testdata/large_model.fga")
	if err != nil {
		t.Fatal(err)
	}

	raw, err := transformer.TransformDSLToJSON(string(dsl))
	if err != nil {
		t.Fatal(err)
	}

	var model openfga.AuthorizationModel
	if err := json.Unmarshal([]byte(raw), &model); err != nil {
		t.Fatal(err)
	}

	return &model
}

func TestLargeModelFixture(t *testing.T) {
	if got := len(ParseModel(largeModel(t)).Types); got != 22 {
		t.Fatalf("types = %d, want 22", got)
	}
}
