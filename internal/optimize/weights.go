package optimize

import (
	"errors"
	"fmt"

	"github.com/openfga/language/pkg/go/graph"
)

// ErrNoWeightedGraph is wrapped by Weights when OpenFGA's weighted graph
// cannot be built for the model (an AND or BUT NOT on a tuple cycle).
var ErrNoWeightedGraph = errors.New("weighted graph cannot be built")

// Weights returns, per "type#relation", the upstream weight of each user
// type. graph.Infinite is preserved.
func Weights(m *Model) (map[string]map[string]int, error) {
	wg, err := graph.NewWeightedAuthorizationModelGraphBuilder().Build(m.Proto)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoWeightedGraph, err)
	}
	out := map[string]map[string]int{}
	for _, n := range wg.GetNodes() {
		if n.GetNodeType() != graph.SpecificTypeAndRelation {
			continue
		}
		out[n.GetUniqueLabel()] = n.GetWeights()
	}
	return out, nil
}
