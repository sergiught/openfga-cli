package optimize

import (
	"maps"
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
// the model's weighted graph is discarded. In a model with a conditioned or
// wildcard direct type, a rewrite that changes any relation's weight is
// discarded too: OpenFGA's weight-2 TTU fast path answers wrongly there.
//
// RewriteSubset applies the same loop under the same gates but only for the
// findings the caller accepts.
func Rewrite(m *Model) (*RewriteResult, error) {
	return rewrite(m, nil)
}

// RewriteSubset is Rewrite restricted to the findings named in accept. A nil
// or empty map accepts nothing. Every gate of Rewrite still applies, so an
// accepted finding is skipped if it would be unsafe in this combination.
func RewriteSubset(m *Model, accept map[FindingKey]bool) (*RewriteResult, error) {
	if accept == nil {
		accept = map[FindingKey]bool{}
	}
	return rewrite(m, accept)
}

// rewrite is the shared loop; a nil accept map accepts every finding.
func rewrite(m *Model, accept map[FindingKey]bool) (*RewriteResult, error) {
	cur := m.Clone()
	rw := &RewriteResult{Before: m}
	if site, ok := recursiveExclusion(m); ok {
		rw.Blocked = site + " subtracts a recursive relation; OpenFGA's cycle handling there depends on evaluation strategy, so no rewrite can be proven safe"
		rw.After = cur
		return rw, nil
	}
	buildable := weightedGraphBuilds(m)
	keepWeights := hasConditionOrWildcard(m)
	var weights map[string]map[string]int
	if keepWeights {
		// Every accepted rewrite keeps these weights, so comparing against
		// the input is comparing against the current model.
		weights, _ = Weights(m)
	}
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
			// A rejected finding must not take the relation's slot for this pass.
			if accept != nil {
				n := 0
				for _, a := range rw.Applied {
					if a.Type == f.Type && a.Relation == f.Relation && a.ID == f.ID {
						n++
					}
				}
				if !accept[FindingKey{Type: f.Type, Relation: f.Relation, ID: f.ID, N: n}] {
					continue
				}
			}
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
			// Without a weighted graph (equal failure, given the check
			// above) there are no weights to change.
			if keepWeights && buildable && !sameWeights(weights, validated) {
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

// hasConditionOrWildcard reports whether any relation of m has a conditioned
// or wildcard directly related type.
func hasConditionOrWildcard(m *Model) bool {
	for _, td := range m.Proto.GetTypeDefinitions() {
		for _, meta := range td.GetMetadata().GetRelations() {
			for _, ref := range meta.GetDirectlyRelatedUserTypes() {
				if ref.GetCondition() != "" || ref.GetWildcard() != nil {
					return true
				}
			}
		}
	}
	return false
}

// sameWeights reports whether m's weighted graph gives every relation the
// same weights as want.
func sameWeights(want map[string]map[string]int, m *Model) bool {
	got, err := Weights(m)
	if err != nil {
		return false
	}
	return maps.EqualFunc(want, got, maps.Equal)
}
