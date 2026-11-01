package optimize

import (
	"sort"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
)

// Term is a userset term scoped to a type. Exactly one of Computed/TTU/Direct is set.
type Term struct {
	Type     string
	Computed string // relation name
	TTU      *struct{ Tupleset, Computed string }
	// Direct is a canonical key: "<owning relation>:" followed by the sorted
	// directly related types ("type", "type#rel", "type:*"), each suffixed
	// " with <cond>" when conditioned.
	//
	// SOUNDNESS: a direct term denotes the tuples stored under ITS OWN
	// relation, so the owning relation is part of its identity. doc#a: [user]
	// and doc#b: [user] are DIFFERENT terms.
	Direct string
}

// key is a comparable identity for t (Term itself is not comparable by value
// because of the TTU pointer).
func (t Term) key() string {
	switch {
	case t.TTU != nil:
		return "T|" + t.Type + "|" + t.TTU.Tupleset + "|" + t.TTU.Computed
	case t.Direct != "":
		return "D|" + t.Type + "|" + t.Direct
	default:
		return "C|" + t.Type + "|" + t.Computed
	}
}

// TermOf converts the rewrite us of typ#rel into a Term; md is the metadata
// of typ#rel. It returns false for union/intersection/difference (and any
// other composite rewrite).
func TermOf(typ, rel string, us *openfgav1.Userset, md *openfgav1.RelationMetadata) (Term, bool) {
	switch v := us.GetUserset().(type) {
	case *openfgav1.Userset_This:
		parts := make([]string, 0, len(md.GetDirectlyRelatedUserTypes()))
		for _, ref := range md.GetDirectlyRelatedUserTypes() {
			s := ref.GetType()
			switch r := ref.GetRelationOrWildcard().(type) {
			case *openfgav1.RelationReference_Relation:
				s += "#" + r.Relation
			case *openfgav1.RelationReference_Wildcard:
				s += ":*"
			}
			if c := ref.GetCondition(); c != "" {
				s += " with " + c
			}
			parts = append(parts, s)
		}
		sort.Strings(parts)
		return Term{Type: typ, Direct: rel + ":" + strings.Join(parts, ",")}, true
	case *openfgav1.Userset_ComputedUserset:
		return Term{Type: typ, Computed: v.ComputedUserset.GetRelation()}, true
	case *openfgav1.Userset_TupleToUserset:
		return Term{Type: typ, TTU: &struct{ Tupleset, Computed string }{
			Tupleset: v.TupleToUserset.GetTupleset().GetRelation(),
			Computed: v.TupleToUserset.GetComputedUserset().GetRelation(),
		}}, true
	}
	return Term{}, false
}

// Lattice is a set of proven (A ⊆ B) pairs. Reflexivity is implicit.
type Lattice struct {
	m     *Model
	pairs map[string]map[string]bool
}

// BuildLattice computes the least fixpoint of the derivation rules: union
// member, alias, transitivity and TTU, starting from the empty set.
func BuildLattice(m *Model) *Lattice {
	l := &Lattice{m: m, pairs: map[string]map[string]bool{}}
	terms := map[string]Term{}
	add := func(t Term) { terms[t.key()] = t }

	for _, td := range m.Proto.GetTypeDefinitions() {
		typ := td.GetType()
		for rel, us := range td.GetRelations() {
			rt := Term{Type: typ, Computed: rel}
			add(rt)
			md := td.GetMetadata().GetRelations()[rel]
			switch us.GetUserset().(type) {
			case *openfgav1.Userset_ComputedUserset: // alias
				if s, ok := TermOf(typ, rel, us, md); ok {
					add(s)
					l.add(rt, s)
					l.add(s, rt)
				}
			case *openfgav1.Userset_Union:
				for _, c := range us.GetUnion().GetChild() {
					if ct, ok := TermOf(typ, rel, c, md); ok {
						add(ct)
						l.add(ct, rt)
					}
				}
			case *openfgav1.Userset_This: // union of one operand
				if ct, ok := TermOf(typ, rel, us, md); ok {
					add(ct)
					l.add(ct, rt)
				}
			}
			// Other TTU terms inside intersections/differences are still
			// terms the TTU rule may compare.
			collectTTU(typ, us, add)
		}
	}

	list := make([]Term, 0, len(terms))
	for _, t := range terms {
		list = append(list, t)
	}
	for changed := true; changed; {
		changed = false
		// transitivity
		for a, bs := range l.pairs {
			for b := range bs {
				for c := range l.pairs[b] {
					if !l.pairs[a][c] && a != c {
						l.pairs[a][c] = true
						changed = true
					}
				}
			}
		}
		// TTU rule
		for _, a := range list {
			if a.TTU == nil {
				continue
			}
			for _, b := range list {
				if b.TTU == nil || b.Type != a.Type || b.TTU.Tupleset != a.TTU.Tupleset ||
					a.key() == b.key() || l.pairs[a.key()][b.key()] {
					continue
				}
				if l.ttuHolds(a, b) {
					l.add(a, b)
					changed = true
				}
			}
		}
	}
	return l
}

func collectTTU(typ string, us *openfgav1.Userset, add func(Term)) {
	switch v := us.GetUserset().(type) {
	case *openfgav1.Userset_TupleToUserset:
		if t, ok := TermOf(typ, "", us, nil); ok {
			add(t)
		}
	case *openfgav1.Userset_Union:
		for _, c := range v.Union.GetChild() {
			collectTTU(typ, c, add)
		}
	case *openfgav1.Userset_Intersection:
		for _, c := range v.Intersection.GetChild() {
			collectTTU(typ, c, add)
		}
	case *openfgav1.Userset_Difference:
		collectTTU(typ, v.Difference.GetBase(), add)
		collectTTU(typ, v.Difference.GetSubtract(), add)
	}
}

func (l *Lattice) add(a, b Term) {
	ak, bk := a.key(), b.key()
	if ak == bk {
		return
	}
	if l.pairs[ak] == nil {
		l.pairs[ak] = map[string]bool{}
	}
	l.pairs[ak][bk] = true
}

// ttuHolds applies the TTU rule to a and b (same type and tupleset): for
// every directly related type P of the tupleset, P#x ⊆ P#y, or P lacks x.
func (l *Lattice) ttuHolds(a, b Term) bool {
	seen := map[string]bool{}
	found := false
	for _, ref := range l.m.DirectTypes(a.Type, a.TTU.Tupleset) {
		p := ref.GetType()
		if seen[p] {
			continue
		}
		seen[p] = true
		found = true
		if _, ok := l.m.Relation(p, a.TTU.Computed); !ok {
			continue // empty term, vacuously contained
		}
		if !l.Subset(Term{Type: p, Computed: a.TTU.Computed}, Term{Type: p, Computed: b.TTU.Computed}) {
			return false
		}
	}
	return found
}

// Subset reports whether a ⊆ b is proven.
func (l *Lattice) Subset(a, b Term) bool {
	ak, bk := a.key(), b.key()
	if ak == bk || l.pairs[ak][bk] {
		return true
	}
	// A TTU pair the model never mentions is still decidable from facts
	// among computed terms.
	if a.TTU != nil && b.TTU != nil && a.Type == b.Type && a.TTU.Tupleset == b.TTU.Tupleset {
		return l.ttuHolds(a, b)
	}
	return false
}

// Equivalent reports whether a ⊆ b and b ⊆ a.
func (l *Lattice) Equivalent(a, b Term) bool { return l.Subset(a, b) && l.Subset(b, a) }
