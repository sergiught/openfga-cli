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
	return fixtureModel(t, "large_model.fga")
}

// stackedModel loads a model whose layout stacks organization above sheet in
// one column, with document and folder feeding both, so sheet's bundle has
// to coexist with through traffic bound for organization.
func stackedModel(t testing.TB) *openfga.AuthorizationModel {
	t.Helper()
	return fixtureModel(t, "stacked_model.fga")
}

// stressModel loads a 22-type, 900-relation model whose stacked columns wall
// off most of the canvas, the case that once made edge routing take seconds.
func stressModel(t testing.TB) *openfga.AuthorizationModel {
	t.Helper()
	return fixtureModel(t, "stress_model.fga")
}

func fixtureModel(t testing.TB, name string) *openfga.AuthorizationModel {
	t.Helper()

	dsl, err := os.ReadFile("testdata/" + name)
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
