package modeldiff

import "github.com/sergiught/openfga-cli/internal/optimize"

// SetWeights swaps the weights function and returns the previous one.
func SetWeights(f func(*optimize.Model) (map[string]map[string]int, error)) func(*optimize.Model) (map[string]map[string]int, error) {
	old := weights
	weights = f
	return old
}
