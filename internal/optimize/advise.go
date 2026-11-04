package optimize

import (
	"fmt"
	"sort"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/language/pkg/go/graph"
)

// minWideUnion is the operand count at which a union is reported by T2.
const minWideUnion = 4

// advise returns caller- and tuple-tier findings for m, each tier sorted by
// type, relation, then ID.
func advise(m *Model) []Finding {
	// OpenFGA cannot build the weighted graph in some models, for example an AND
	// or BUT NOT on a tuple cycle, or a tupleset type that lacks the relation.
	// Findings then carry no weights, and a relation counts as infinite when it
	// can reach a cycle in the relation dependency graph, as an inherited
	// infinite weight would on the weighted path.
	w, err := Weights(m)
	infinite := map[string]bool{}
	deps := dependencyGraph(m)
	cyclic := cyclicRelations(deps)
	if err != nil {
		for n := range deps {
			for r := range reachable(deps, []string{n}) {
				if cyclic[r] {
					infinite[n] = true
					break
				}
			}
		}
	}
	var caller, tuple []Finding
	for _, td := range m.Proto.GetTypeDefinitions() {
		typ := td.GetType()
		for rel, us := range td.GetRelations() {
			wts := copyWeights(w[typ+"#"+rel])
			add := func(dst *[]Finding, id string, tier Tier, reason string) {
				*dst = append(*dst, Finding{ID: id, Type: typ, Relation: rel, Reason: reason, Tier: tier, Weights: wts})
			}
			inf := hasInfinite(w[typ+"#"+rel]) || infinite[typ+"#"+rel]
			// T1 is reported only where the relation's own definition leads
			// back to itself; relations that merely inherit an infinite
			// weight are not reported.
			ownRecursion := inf && introducesRecursion(m, deps, typ, rel, us)
			switch {
			case us.GetIntersection() != nil && inf && hasRecursiveOperand(m, deps, cyclic, typ, rel, us.GetIntersection().GetChild()):
				add(&caller, "C1", TierCaller, "Recursive operand inside an intersection: a Check may evaluate it fully, so a deep graph is expensive; keep the recursion out of the intersection or cap depth in callers.")
			case us.GetDifference() != nil && inf && hasRecursiveOperand(m, deps, cyclic, typ, rel, []*openfgav1.Userset{us.GetDifference().GetBase(), us.GetDifference().GetSubtract()}):
				add(&caller, "C1", TierCaller, "Recursive operand inside an exclusion: a Check may evaluate it fully, so a deep graph is expensive; keep the recursion out of the exclusion or cap depth in callers.")
			}
			if us.GetDifference() != nil {
				add(&caller, "C2", TierCaller, "Exclusion present: the subtracted side is always evaluated, so a Check on this relation costs an extra datastore read, and an extra dispatch when the subtracted side is not a direct relation.")
			}
			if ownRecursion {
				add(&tuple, "T1", TierTuple, "Relation introduces recursion: each nesting level in the tuple data costs one more datastore read and one more dispatch; keep nesting shallow.")
			}
			if u := us.GetUnion(); u != nil && len(u.GetChild()) >= minWideUnion {
				add(&tuple, "T2", TierTuple, fmt.Sprintf("Union with %d operands: each computed or tuple-to-userset operand is a separate dispatch; a Check that misses evaluates all of them. Consider consolidating roles.", len(u.GetChild())))
			}
		}
	}
	sortFindings(caller)
	sortFindings(tuple)
	return append(caller, tuple...)
}

func sortFindings(fs []Finding) {
	sort.Slice(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Relation != b.Relation {
			return a.Relation < b.Relation
		}
		return a.ID < b.ID
	})
}

// copyWeights returns a fresh map with graph.Infinite replaced by -1; the
// input aliases the weighted graph's internal maps and must not be mutated.
func copyWeights(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		if v == graph.Infinite {
			v = -1
		}
		out[k] = v
	}
	return out
}

func hasInfinite(in map[string]int) bool {
	for _, v := range in {
		if v == graph.Infinite {
			return true
		}
	}
	return false
}

// introducesRecursion reports whether typ#rel's own definition references
// (computed, tuple-to-userset or userset direct type) a relation that leads
// back to typ#rel. A userset that is recursive on its own, without reaching
// typ#rel again, does not count.
func introducesRecursion(m *Model, deps map[string][]string, typ, rel string, us *openfgav1.Userset) bool {
	return reachable(deps, relationEdges(m, typ, rel, us, false))[typ+"#"+rel]
}

// hasRecursiveOperand reports whether an operand of typ#rel's intersection or
// exclusion references, directly or transitively, a relation that lies on a
// dependency cycle.
func hasRecursiveOperand(m *Model, deps map[string][]string, cyclic map[string]bool, typ, rel string, operands []*openfgav1.Userset) bool {
	for _, op := range operands {
		for e := range reachable(deps, relationEdges(m, typ, rel, op, false)) {
			if cyclic[e] {
				return true
			}
		}
	}
	return false
}
