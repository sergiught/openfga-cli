package modeldiff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sergiught/openfga-cli/internal/optimize"
)

// Direction values are a stable part of the JSON contract.
const (
	CostIncreased = "increased"
	CostDecreased = "decreased"
)

// CostEntry is one relation whose per-user-type weights differ between models.
type CostEntry struct {
	Type      string         `json:"type"`
	Relation  string         `json:"relation"`
	Direction string         `json:"direction" jsonschema:"enum=increased,enum=decreased"`
	Old       map[string]int `json:"old" jsonschema_description:"Weight per user type; -1 means unbounded (infinite) weight."`
	New       map[string]int `json:"new" jsonschema_description:"Weight per user type; -1 means unbounded (infinite) weight."`
}

// HasCostIncrease reports whether any relation got more expensive.
func (r Result) HasCostIncrease() bool {
	for _, c := range r.Cost {
		if c.Direction == CostIncreased {
			return true
		}
	}
	return false
}

// weights is a variable so tests can inject a failure.
var weights = optimize.Weights

func diffCost(r *Result, oldM, newM *optimize.Model) {
	ow, err := weights(oldM)
	if err != nil {
		r.CostSkipped = skipReason("old model", err)
		return
	}
	nw, err := weights(newM)
	if err != nil {
		r.CostSkipped = skipReason("new model", err)
		return
	}
	for key, o := range ow {
		n, ok := nw[key]
		if !ok {
			continue
		}
		dir := direction(o, n)
		if dir == "" {
			continue
		}
		typ, rel, _ := strings.Cut(key, "#")
		r.Cost = append(r.Cost, CostEntry{
			Type: typ, Relation: rel, Direction: dir,
			Old: optimize.JSONWeights(o), New: optimize.JSONWeights(n),
		})
	}
	sort.Slice(r.Cost, func(i, j int) bool {
		if r.Cost[i].Type != r.Cost[j].Type {
			return r.Cost[i].Type < r.Cost[j].Type
		}
		return r.Cost[i].Relation < r.Cost[j].Relation
	})
}

func skipReason(side string, err error) string {
	if reason, ok := optimize.NoWeightedGraphReason(err); ok {
		return fmt.Sprintf("%s: %s", side, reason)
	}
	return fmt.Sprintf("%s: %v", side, err)
}

// direction returns "" when the maps are equal, CostIncreased when any weight
// rose or a user type was added, else CostDecreased.
func direction(o, n map[string]int) string {
	up, down := false, false
	for u, nv := range n {
		ov, ok := o[u]
		switch {
		case !ok || nv > ov:
			up = true
		case nv < ov:
			down = true
		}
	}
	for u := range o {
		if _, ok := n[u]; !ok {
			down = true
		}
	}
	switch {
	case up:
		return CostIncreased
	case down:
		return CostDecreased
	}
	return ""
}
