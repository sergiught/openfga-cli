package modeldiff_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/modeldiff"
	"github.com/sergiught/openfga-cli/internal/optimize"
)

const costBase = `model
  schema 1.1
type user
type folder
  relations
    define viewer: [user]
type document
  relations
    define parent: [folder]
    define viewer: [user]
`

const costWider = `model
  schema 1.1
type user
type folder
  relations
    define viewer: [user]
type document
  relations
    define parent: [folder]
    define viewer: [user] or viewer from parent
`

func TestCostIncreasedByNewTupleToUserset(t *testing.T) {
	r := modeldiff.Diff(mustModel(t, costBase), mustModel(t, costWider))
	if len(r.Cost) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(r.Cost), r.Cost)
	}
	c := r.Cost[0]
	if c.Type != "document" {
		t.Errorf("got %v, want %v", c.Type, "document")
	}
	if c.Relation != "viewer" {
		t.Errorf("got %v, want %v", c.Relation, "viewer")
	}
	if c.Direction != modeldiff.CostIncreased {
		t.Errorf("got %v, want %v", c.Direction, modeldiff.CostIncreased)
	}
	if c.New["user"] <= c.Old["user"] && c.New["user"] != -1 {
		t.Errorf("weight did not rise: %+v", c)
	}
	if !r.HasCostIncrease() {
		t.Error("HasCostIncrease = false, want true")
	}
	if r.Empty() {
		t.Error("Empty = true, want false")
	}
}

func TestCostDecreased(t *testing.T) {
	r := modeldiff.Diff(mustModel(t, costWider), mustModel(t, costBase))
	if len(r.Cost) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(r.Cost), r.Cost)
	}
	if r.Cost[0].Direction != modeldiff.CostDecreased {
		t.Errorf("got %v, want %v", r.Cost[0].Direction, modeldiff.CostDecreased)
	}
	if r.HasCostIncrease() {
		t.Error("HasCostIncrease = true, want false")
	}
}

func TestCostUnboundedIsMinusOne(t *testing.T) {
	old := `model
  schema 1.1
type user
type group
  relations
    define parent: [group]
    define member: [user]
`
	nw := `model
  schema 1.1
type user
type group
  relations
    define parent: [group]
    define member: [user] or member from parent
`
	r := modeldiff.Diff(mustModel(t, old), mustModel(t, nw))
	if len(r.Cost) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(r.Cost), r.Cost)
	}
	if got := r.Cost[0].New["user"]; got != -1 {
		t.Errorf("New[user] = %d, want -1", got)
	}
}

func TestCostOnlyOneSideRelationsSkipped(t *testing.T) {
	added := costBase + "    define editor: [user]\n"
	r := modeldiff.Diff(mustModel(t, costBase), mustModel(t, added))
	if len(r.Cost) != 0 {
		t.Errorf("want no cost entries, got %+v", r.Cost)
	}
	if len(r.Added) == 0 {
		t.Error("want an added entry")
	}
}

func TestCostSkippedWhenNoWeightedGraph(t *testing.T) {
	oldM, newM := mustModel(t, costBase), mustModel(t, costWider)
	orig := modeldiff.SetWeights(func(m *optimize.Model) (map[string]map[string]int, error) {
		if m == newM {
			return nil, fmt.Errorf("%w: boom", optimize.ErrNoWeightedGraph)
		}
		return optimize.Weights(m)
	})
	t.Cleanup(func() { modeldiff.SetWeights(orig) })
	r := modeldiff.Diff(oldM, newM)
	if !strings.HasPrefix(r.CostSkipped, "new model: ") {
		t.Errorf("got %q, want prefix %q", r.CostSkipped, "new model: ")
	}
	if strings.Contains(r.CostSkipped, "cost comparison skipped") {
		t.Errorf("got %q, want no command-level prefix", r.CostSkipped)
	}
	if !strings.Contains(r.CostSkipped, "boom") {
		t.Errorf("got %q, want it to contain %q", r.CostSkipped, "boom")
	}
	if r.Cost == nil {
		t.Error("Cost is nil, want non-nil")
	}
	if len(r.Cost) != 0 {
		t.Errorf("want no cost entries, got %+v", r.Cost)
	}
}
