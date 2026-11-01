package optimize

import (
	"sort"

	"github.com/openfga/language/pkg/go/graph"
)

// maxPasses caps the rewrite fixpoint loop.
const maxPasses = 10

// RewriteResult is the outcome of applying the safe rules to a model.
type RewriteResult struct {
	Applied []Finding
	Before  *Model // original, untouched
	After   *Model // rewritten and validated
	// Blocked, when set, says why no safe rewrite was applied at all.
	Blocked string
}

// Rewrite applies the safe rules to fixpoint (at most 10 passes). At most one
// finding is applied per relation per pass, and the lattice is rebuilt from
// the current model each pass, so dependent drops are re-derived rather than
// assumed. A model with a recursive exclusion gets no rewrite at all (see
// recursiveExclusion), and a rewrite that changes whether OpenFGA can build
// the model's weighted graph is discarded.
func Rewrite(m *Model) (*RewriteResult, error) {
	cur := m.Clone()
	rw := &RewriteResult{Before: m}
	if site, ok := recursiveExclusion(m); ok {
		rw.Blocked = site + " subtracts a recursive relation; OpenFGA's cycle handling there depends on evaluation strategy, so no rewrite can be proven safe"
		rw.After = cur
		return rw, nil
	}
	buildable := weightedGraphBuilds(m)
	for range maxPasses {
		applied := len(rw.Applied)
		l := BuildLattice(cur)
		typeIdx := map[string]int{}
		for i, td := range cur.Proto.GetTypeDefinitions() {
			typeIdx[td.GetType()] = i
		}
		var all []Finding
		for _, r := range safeRules {
			all = append(all, r(cur, l)...)
		}
		sort.SliceStable(all, func(i, j int) bool {
			a, b := all[i], all[j]
			if typeIdx[a.Type] != typeIdx[b.Type] {
				return typeIdx[a.Type] < typeIdx[b.Type]
			}
			if ruleOrder(a.ID) != ruleOrder(b.ID) {
				return ruleOrder(a.ID) < ruleOrder(b.ID)
			}
			return a.Relation < b.Relation
		})
		if len(all) == 0 {
			break
		}
		next := cur
		seen := map[string]bool{}
		for _, f := range all {
			key := f.Type + "#" + f.Relation
			if seen[key] {
				continue
			}
			seen[key] = true
			cand := next.Clone()
			f.apply(cand)
			// An edit that is sound in the lattice can still leave the model
			// invalid (e.g. no entrypoints); discard it rather than fail.
			validated, err := NewModel(cand.Proto)
			if err != nil {
				continue
			}
			// The weighted graph decides which resolvers OpenFGA uses for
			// the whole model; keep that choice unchanged.
			if weightedGraphBuilds(validated) != buildable {
				continue
			}
			next = validated
			rw.Applied = append(rw.Applied, f)
		}
		if len(rw.Applied) == applied {
			break
		}
		cur = next
	}
	rw.After = cur
	return rw, nil
}

// weightedGraphBuilds reports whether OpenFGA can build m's weighted graph.
func weightedGraphBuilds(m *Model) bool {
	_, err := graph.NewWeightedAuthorizationModelGraphBuilder().Build(m.Proto)
	return err == nil
}
