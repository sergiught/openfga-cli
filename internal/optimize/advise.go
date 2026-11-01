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
func advise(m *Model) ([]Finding, error) {
	w, err := Weights(m)
	if err != nil {
		return nil, err
	}
	var caller, tuple []Finding
	for _, td := range m.Proto.GetTypeDefinitions() {
		typ := td.GetType()
		for rel, us := range td.GetRelations() {
			wts := copyWeights(w[typ+"#"+rel])
			add := func(dst *[]Finding, id string, tier Tier, reason string) {
				*dst = append(*dst, Finding{ID: id, Type: typ, Relation: rel, Reason: reason, Tier: tier, Weights: wts})
			}
			inf := hasInfinite(w[typ+"#"+rel])
			// Recursion is "inside" the operands only when the relation's own
			// definition introduces it; relations that merely inherit an
			// infinite weight are not reported.
			recursive := inf && introducesRecursion(m, typ, rel, us)
			switch {
			case us.GetIntersection() != nil && recursive:
				add(&caller, "C1", TierCaller, "Recursive relation inside an intersection: every operand is evaluated, so a deep graph is expensive; keep the recursion out of the intersection or cap depth in callers.")
			case us.GetDifference() != nil && recursive:
				add(&caller, "C1", TierCaller, "Recursive relation inside an exclusion: both sides are evaluated, so a deep graph is expensive; keep the recursion out of the exclusion or cap depth in callers.")
			}
			if us.GetDifference() != nil {
				add(&caller, "C2", TierCaller, "Exclusion present: the subtracted side is always evaluated, so a Check on this relation costs at least one extra dispatch.")
			}
			if recursive {
				add(&tuple, "T1", TierTuple, "Relation introduces recursion: each nesting level in the tuple data costs +1 datastore read and +1 dispatch per nesting level; keep nesting shallow.")
			}
			if u := us.GetUnion(); u != nil && len(u.GetChild()) >= minWideUnion {
				add(&tuple, "T2", TierTuple, fmt.Sprintf("Union with %d operands: each operand is a separate dispatch; consider consolidating roles.", len(u.GetChild())))
			}
		}
	}
	sortFindings(caller)
	sortFindings(tuple)
	return append(caller, tuple...), nil
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

// introducesRecursion reports whether the definition of typ#rel itself
// contains a self-referencing TTU or a userset direct type.
func introducesRecursion(m *Model, typ, rel string, us *openfgav1.Userset) bool {
	for _, d := range m.DirectTypes(typ, rel) {
		if d.GetRelation() != "" {
			return true
		}
	}
	found := false
	walkUserset(us, func(u *openfgav1.Userset) {
		t := u.GetTupleToUserset()
		if t == nil || t.GetComputedUserset().GetRelation() != rel {
			return
		}
		for _, d := range m.DirectTypes(typ, t.GetTupleset().GetRelation()) {
			if d.GetType() == typ {
				found = true
			}
		}
	})
	return found
}

func walkUserset(us *openfgav1.Userset, fn func(*openfgav1.Userset)) {
	fn(us)
	switch {
	case us.GetUnion() != nil:
		for _, c := range us.GetUnion().GetChild() {
			walkUserset(c, fn)
		}
	case us.GetIntersection() != nil:
		for _, c := range us.GetIntersection().GetChild() {
			walkUserset(c, fn)
		}
	case us.GetDifference() != nil:
		walkUserset(us.GetDifference().GetBase(), fn)
		walkUserset(us.GetDifference().GetSubtract(), fn)
	}
}
