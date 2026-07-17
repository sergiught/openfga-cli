package fga

import (
	"image/color"
	"strings"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/style"
)

// weightRecursive marks a relation whose resolution can re-enter itself: its
// cost is unbounded (∞).
const weightRecursive = -1

// Cost thresholds separating the display buckets: 1 is cheap, 2..3 moderate,
// 4+ expensive.
const (
	costModerateFloor  = 2
	costExpensiveFloor = 4
)

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

type costBucket int

const (
	bucketCheap costBucket = iota
	bucketModerate
	bucketExpensive
	bucketRecursive
)

// bucket classifies a relation's resolution cost for the heatmap.
func (r Relation) bucket() costBucket {
	switch {
	case r.Recursive || r.Weight == weightRecursive:
		return bucketRecursive
	case r.Weight >= costExpensiveFloor:
		return bucketExpensive
	case r.Weight >= costModerateFloor:
		return bucketModerate
	default:
		return bucketCheap
	}
}

func bucketColor(b costBucket) color.Color {
	switch b {
	case bucketExpensive:
		return style.Red
	case bucketModerate:
		return style.Amber
	case bucketRecursive:
		return style.Magenta
	default:
		return style.Green
	}
}

func bucketGlyph(b costBucket) rune {
	if b == bucketRecursive {
		return '∞'
	}
	return '●'
}

// heatGlyph returns the colored cost marker for a relation.
func (r Relation) heatGlyph() (rune, color.Color) {
	b := r.bucket()
	return bucketGlyph(b), bucketColor(b)
}

// weightLegend labels the heat colors; shared by the tree and diagram headers so
// the two views describe the same buckets.
func weightLegend() string {
	return strings.Join([]string{
		colorRune('●', style.Green) + " " + style.Faint.Render("cheap"),
		colorRune('●', style.Amber) + " " + style.Faint.Render("moderate"),
		colorRune('●', style.Red) + " " + style.Faint.Render("costly"),
		colorRune('∞', style.Magenta) + " " + style.Faint.Render("recursive"),
	}, "  ")
}
