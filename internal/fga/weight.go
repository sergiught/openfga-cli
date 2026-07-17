package fga

import (
	"strings"

	"github.com/sergiught/go-openfga/openfga"
)

// weightRecursive marks a relation whose resolution can re-enter itself: its
// cost is unbounded (∞).
const weightRecursive = -1

// computeWeights assigns each "type#relation" a worst-case resolution cost by
// walking the rewrite rules: 1 for a direct edge to a terminal type, +1 per
// computed / tuple-to-userset hop, the max across union/intersection/difference
// branches, and weightRecursive (∞) when a relation can reach itself.
func computeWeights(m *openfga.AuthorizationModel) map[string]int {
	rules := map[string]map[string]openfga.Userset{}
	directs := map[string]map[string][]string{}
	for _, td := range m.TypeDefinitions {
		rules[td.Type] = td.Relations
		directs[td.Type] = directTypesByRelation(td.Metadata)
	}

	const (
		unvisited = iota
		inProgress
		done
	)
	state := map[string]int{}
	memo := map[string]int{}

	maxCost := func(a, b int) int {
		if a == weightRecursive || b == weightRecursive {
			return weightRecursive
		}
		if a > b {
			return a
		}
		return b
	}
	inc := func(w int) int {
		if w == weightRecursive {
			return weightRecursive
		}
		return w + 1
	}

	var costOf func(typ, rel string) int
	var costRule func(typ, rel string, rule openfga.Userset) int

	costOf = func(typ, rel string) int {
		key := typ + "#" + rel
		switch state[key] {
		case done:
			return memo[key]
		case inProgress:
			return weightRecursive // back-edge: this path re-enters an in-flight node
		}
		rule, ok := rules[typ][rel]
		if !ok {
			return 1 // unknown relation: treat as a single lookup
		}
		state[key] = inProgress
		w := costRule(typ, rel, rule)
		if w == 0 {
			w = 1 // every relation costs at least one lookup
		}
		state[key] = done
		memo[key] = w
		return w
	}

	costRule = func(typ, rel string, rule openfga.Userset) int {
		best := 0
		// Direct assignment: "this" resolves against the relation's directly
		// related user types (from metadata), not any sub-rule.
		if rule.This != nil {
			for _, label := range directs[typ][rel] {
				if i := strings.Index(label, "#"); i >= 0 {
					best = maxCost(best, inc(costOf(label[:i], label[i+1:]))) // userset e.g. group#member
				} else {
					best = maxCost(best, 1) // terminal type or wildcard (user, user:*)
				}
			}
		}
		if cu := rule.ComputedUserset; cu != nil && cu.Relation != "" {
			best = maxCost(best, inc(costOf(typ, cu.Relation)))
		}
		if ttu := rule.TupleToUserset; ttu != nil && ttu.ComputedUserset.Relation != "" {
			via, target := ttu.Tupleset.Relation, ttu.ComputedUserset.Relation
			branch := 0
			for _, parent := range directs[typ][via] {
				pt := typePart(parent)
				if _, ok := rules[pt][target]; ok {
					branch = maxCost(branch, costOf(pt, target))
				}
			}
			best = maxCost(best, inc(branch))
		}
		for _, set := range []*openfga.Usersets{rule.Union, rule.Intersection} {
			if set == nil {
				continue
			}
			for _, child := range set.Child {
				best = maxCost(best, costRule(typ, rel, child))
			}
		}
		if diff := rule.Difference; diff != nil {
			best = maxCost(best, costRule(typ, rel, diff.Base))
			best = maxCost(best, costRule(typ, rel, diff.Subtract))
		}
		return best
	}

	out := map[string]int{}
	for typ, rels := range rules {
		for rel := range rels {
			out[typ+"#"+rel] = costOf(typ, rel)
		}
	}
	return out
}
