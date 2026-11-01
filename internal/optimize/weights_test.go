package optimize

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/openfga/language/pkg/go/graph"
	"github.com/openfga/language/pkg/go/transformer"
	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// LoadReferenceModel loads testdata/reference.fga.
func LoadReferenceModel(t testing.TB) *Model {
	t.Helper()
	data, err := os.ReadFile("testdata/reference.fga")
	if err != nil {
		t.Fatal(err)
	}
	// LoadModelBytes goes DSL -> JSON -> proto, which (unlike
	// transformer.TransformDSLToProto) yields a proto that DSL() can render.
	lm, err := modeltest.LoadModelBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModel(lm.Proto)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWeights_ReferenceModel(t *testing.T) {
	w, err := Weights(LoadReferenceModel(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		key  string
		want int
	}{
		{"folder#can_delete", graph.Infinite},
		{"group#member", graph.Infinite},
		{"folder#owner", 1},
	} {
		if got := w[c.key]["user"]; got != c.want {
			t.Errorf("%s user weight = %d, want %d", c.key, got, c.want)
		}
	}
}

func TestNewModel_RejectsInvalid(t *testing.T) {
	p, err := transformer.TransformDSLToProto("model\n  schema 1.1\ntype user\ntype doc\n  relations\n    define viewer: [nope]\n")
	if err != nil {
		t.Fatalf("parser rejected: %v", err)
	}
	_, err = NewModel(p)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !errors.Is(err, ErrInvalidModel) {
		t.Fatalf("error %v does not wrap ErrInvalidModel", err)
	}
	if !strings.HasPrefix(err.Error(), "invalid authorization model: ") {
		t.Fatalf("unexpected message %q", err.Error())
	}
}

func TestModel_Lookups(t *testing.T) {
	m := LoadReferenceModel(t)
	if _, ok := m.Relation("folder", "viewer"); !ok {
		t.Fatal("folder#viewer not found")
	}
	if _, ok := m.Relation("folder", "nope"); ok {
		t.Fatal("unexpected relation")
	}
	if got := len(m.DirectTypes("folder", "viewer")); got != 2 {
		t.Fatalf("direct types = %d, want 2", got)
	}
	c := m.Clone()
	c.Proto.TypeDefinitions = nil
	if len(m.Proto.TypeDefinitions) == 0 {
		t.Fatal("clone shares state")
	}
	if _, err := m.DSL(); err != nil {
		t.Fatal(err)
	}
}
