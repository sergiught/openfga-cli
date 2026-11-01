package optimize

import (
	"fmt"
	"sort"
)

// maxPasses caps the rewrite fixpoint loop.
const maxPasses = 10

// RewriteResult is the outcome of applying the safe rules to a model.
type RewriteResult struct {
	Applied []Finding
	Before  *Model // original, untouched
	After   *Model // rewritten and validated
}

// Rewrite applies the safe rules to fixpoint (at most 10 passes). At most one
// finding is applied per relation per pass, and the lattice is rebuilt from
// the current model each pass, so dependent drops are re-derived rather than
// assumed.
func Rewrite(m *Model) (*RewriteResult, error) {
	cur := m.Clone()
	rw := &RewriteResult{Before: m}
	guarded := guardedRelations(m)
	for range maxPasses {
		l := BuildLattice(cur)
		typeIdx := map[string]int{}
		for i, td := range cur.Proto.GetTypeDefinitions() {
			typeIdx[td.GetType()] = i
		}
		var all []Finding
		for _, r := range safeRules {
			for _, f := range r(cur, l) {
				if !guarded[f.Type+"#"+f.Relation] {
					all = append(all, f)
				}
			}
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
		next := cur.Clone()
		seen := map[string]bool{}
		for _, f := range all {
			key := f.Type + "#" + f.Relation
			if seen[key] {
				continue
			}
			seen[key] = true
			f.apply(next)
			rw.Applied = append(rw.Applied, f)
		}
		validated, err := NewModel(next.Proto)
		if err != nil {
			return nil, fmt.Errorf("rewrite produced an invalid model: %w", err)
		}
		cur = validated
	}
	rw.After = cur
	return rw, nil
}
