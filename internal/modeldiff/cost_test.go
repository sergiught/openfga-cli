package modeldiff_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

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
	require.Len(t, r.Cost, 1)
	c := r.Cost[0]
	require.Equal(t, "document", c.Type)
	require.Equal(t, "viewer", c.Relation)
	require.Equal(t, modeldiff.CostIncreased, c.Direction)
	require.True(t, c.New["user"] > c.Old["user"] || c.New["user"] == -1, "%+v", c)
	require.True(t, r.HasCostIncrease())
	require.False(t, r.Empty())
}

func TestCostDecreased(t *testing.T) {
	r := modeldiff.Diff(mustModel(t, costWider), mustModel(t, costBase))
	require.Len(t, r.Cost, 1)
	require.Equal(t, modeldiff.CostDecreased, r.Cost[0].Direction)
	require.False(t, r.HasCostIncrease())
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
	require.Len(t, r.Cost, 1)
	require.Equal(t, -1, r.Cost[0].New["user"])
}

func TestCostOnlyOneSideRelationsSkipped(t *testing.T) {
	added := costBase + "    define editor: [user]\n"
	r := modeldiff.Diff(mustModel(t, costBase), mustModel(t, added))
	require.Empty(t, r.Cost)
	require.NotEmpty(t, r.Added)
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
	require.Contains(t, r.CostSkipped, "new model")
	require.Contains(t, r.CostSkipped, "boom")
	require.NotNil(t, r.Cost)
	require.Empty(t, r.Cost)
}
