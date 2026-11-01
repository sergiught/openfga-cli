package optimize

import (
	"fmt"

	"github.com/openfga/language/pkg/go/graph"
)

// Weights returns, per "type#relation", the upstream weight of each user
// type. graph.Infinite is preserved.
func Weights(m *Model) (map[string]map[string]int, error) {
	wg, err := graph.NewWeightedAuthorizationModelGraphBuilder().Build(m.Proto)
	if err != nil {
		return nil, fmt.Errorf("build weighted graph: %w", err)
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
