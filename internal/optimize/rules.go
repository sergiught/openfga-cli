package optimize

import (
	"fmt"
	"sort"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/proto"
)

// rule inspects a model and its lattice and returns findings, each carrying
// the exact edit to make.
type rule func(m *Model, l *Lattice) []Finding

var safeRules = []rule{ruleS1, ruleS2, ruleS3, ruleS4}

// ruleOrder gives the position of a finding ID within a pass.
func ruleOrder(id string) int {
	for i, want := range []string{"S1", "S2", "S3", "S4"} {
		if id == want {
			return i
		}
	}
	return len(safeRules)
}

// unionSite is a union eligible for operand removal: the relation's whole
// definition, or the base side of a top-level `but not`. Unions on the
// subtracted side and inside intersections are never eligible.
func unionSite(us *openfgav1.Userset) *openfgav1.Userset_Union {
	if u := us.GetUnion(); u != nil {
		return us.GetUserset().(*openfgav1.Userset_Union)
	}
	if d := us.GetDifference(); d != nil && d.GetBase().GetUnion() != nil {
		return d.GetBase().GetUserset().(*openfgav1.Userset_Union)
	}
	return nil
}

// dropOperand removes child idx of the eligible union of typ#rel, collapsing
// a one-operand union to its remaining operand.
func dropOperand(m *Model, typ, rel string, idx int) {
	for _, td := range m.Proto.GetTypeDefinitions() {
		if td.GetType() != typ {
			continue
		}
		us := td.GetRelations()[rel]
		collapse := func(u *openfgav1.Usersets) *openfgav1.Userset {
			kids := append([]*openfgav1.Userset{}, u.GetChild()[:idx]...)
			kids = append(kids, u.GetChild()[idx+1:]...)
			if len(kids) == 1 {
				return kids[0]
			}
			return &openfgav1.Userset{Userset: &openfgav1.Userset_Union{Union: &openfgav1.Usersets{Child: kids}}}
		}
		if u := us.GetUnion(); u != nil {
			td.GetRelations()[rel] = collapse(u)
		} else if d := us.GetDifference(); d != nil {
			d.Base = collapse(d.GetBase().GetUnion())
		}
		return
	}
}

// fragment returns the right-hand side of `define rel:` for typ#rel.
func fragment(m *Model, typ, rel string) (string, error) {
	dsl, err := m.DSL()
	if err != nil {
		return "", err
	}
	in := false
	for _, line := range strings.Split(dsl, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "type ") {
			in = strings.TrimSpace(strings.TrimPrefix(line, "type ")) == typ
			continue
		}
		if in && strings.HasPrefix(trimmed, "define "+rel+":") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "define "+rel+":")), nil
		}
	}
	return "", fmt.Errorf("relation %s#%s not found in rendered model", typ, rel)
}

// newFinding builds a safe finding whose Before/After come from rendering
// the relation before and after edit runs on a clone.
func newFinding(m *Model, id, typ, rel, reason, proof string, edit func(*Model)) (Finding, bool) {
	before, err := fragment(m, typ, rel)
	if err != nil {
		return Finding{}, false
	}
	c := m.Clone()
	edit(c)
	after, err := fragment(c, typ, rel)
	if err != nil {
		return Finding{}, false
	}
	return Finding{
		ID: id, Type: typ, Relation: rel,
		Before: before, After: after,
		Reason: reason, Proof: proof, Tier: TierSafe,
		apply: edit,
	}, true
}

func sortedRelations(td *openfgav1.TypeDefinition) []string {
	names := make([]string, 0, len(td.GetRelations()))
	for r := range td.GetRelations() {
		names = append(names, r)
	}
	sort.Strings(names)
	return names
}

func termString(t Term) string {
	switch {
	case t.TTU != nil:
		return t.TTU.Computed + " from " + t.TTU.Tupleset
	case t.Direct != "":
		return "direct assignments"
	}
	return t.Computed
}

// unionRule finds union operands (selected by droppable) contained in another
// operand of the same union. The equivalent-operands branch (keep the earlier
// one) is defensive: aliases are one-way and OpenFGA rejects computed cycles,
// so Equivalent holds only for identical keys, already excluded as syntactic
// duplicates, and degenerate TTU pairs.
func unionRule(m *Model, l *Lattice, id, reason string, droppable func(Term) bool) []Finding {
	var out []Finding
	for _, td := range m.Proto.GetTypeDefinitions() {
		typ := td.GetType()
		for _, rel := range sortedRelations(td) {
			site := unionSite(td.GetRelations()[rel])
			if site == nil {
				continue
			}
			md := td.GetMetadata().GetRelations()[rel]
			kids := site.Union.GetChild()
			terms := make([]Term, len(kids))
			ok := make([]bool, len(kids))
			for i, c := range kids {
				terms[i], ok[i] = TermOf(typ, rel, c, md)
			}
			found := false
			for i := 0; i < len(kids) && !found; i++ {
				if !ok[i] || !droppable(terms[i]) {
					continue
				}
				for j := range kids {
					if j == i || !ok[j] || terms[i].key() == terms[j].key() || !l.Subset(terms[i], terms[j]) {
						continue
					}
					if l.Subset(terms[j], terms[i]) && j > i {
						continue // equivalent: keep the earlier operand
					}
					idx := i
					proof := fmt.Sprintf("%s#%s ⊆ %s#%s", typ, termString(terms[i]), typ, termString(terms[j]))
					f, good := newFinding(m, id, typ, rel, reason, proof, func(mm *Model) { dropOperand(mm, typ, rel, idx) })
					if good && l.admit != nil && !l.admit(f) {
						continue // rejected: try the next candidate
					}
					if good {
						out = append(out, f)
						found = true
					}
					break
				}
			}
		}
	}
	return out
}

// ruleS1 drops a computed-userset union operand contained in another operand.
func ruleS1(m *Model, l *Lattice) []Finding {
	return unionRule(m, l, "S1", "operand is already included by another operand of the union",
		func(t Term) bool { return t.TTU == nil && t.Direct == "" })
}

// ruleS4 drops a tuple-to-userset union operand contained in another operand.
func ruleS4(m *Model, l *Lattice) []Finding {
	return unionRule(m, l, "S4", "tuple-to-userset operand is already included by another operand of the union",
		func(t Term) bool { return t.TTU != nil })
}

// ruleS2 rewrites `A and B` to the contained operand when A ⊆ B (or B ⊆ A).
// Only a two-operand intersection that is the relation's whole definition.
func ruleS2(m *Model, l *Lattice) []Finding {
	var out []Finding
	for _, td := range m.Proto.GetTypeDefinitions() {
		typ := td.GetType()
		for _, rel := range sortedRelations(td) {
			in := td.GetRelations()[rel].GetIntersection()
			if in == nil || len(in.GetChild()) != 2 {
				continue
			}
			md := td.GetMetadata().GetRelations()[rel]
			a, okA := TermOf(typ, rel, in.GetChild()[0], md)
			b, okB := TermOf(typ, rel, in.GetChild()[1], md)
			if !okA || !okB || a.Computed == "" || b.Computed == "" || a.key() == b.key() {
				continue
			}
			keep := -1
			switch {
			case l.Subset(a, b):
				keep = 0
			case l.Subset(b, a):
				keep = 1
			}
			if keep < 0 {
				continue
			}
			kept, other := a, b
			if keep == 1 {
				kept, other = b, a
			}
			proof := fmt.Sprintf("%s#%s ⊆ %s#%s", typ, kept.Computed, typ, other.Computed)
			f, good := newFinding(m, "S2", typ, rel, "intersection operand is already implied by the other", proof, func(mm *Model) {
				for _, d := range mm.Proto.GetTypeDefinitions() {
					if d.GetType() == typ {
						d.GetRelations()[rel] = d.GetRelations()[rel].GetIntersection().GetChild()[keep]
					}
				}
			})
			if good {
				out = append(out, f)
			}
		}
	}
	return out
}

// duplicateName names a duplicated union operand so each duplicate group has
// its own proof text, and so its own finding key.
func duplicateName(u *openfgav1.Userset) string {
	if t := u.GetTupleToUserset(); t != nil {
		return t.GetComputedUserset().GetRelation() + " from " + t.GetTupleset().GetRelation()
	}
	return u.GetComputedUserset().GetRelation()
}

// ruleS3 drops a syntactically duplicated union operand.
func ruleS3(m *Model, l *Lattice) []Finding {
	var out []Finding
	for _, td := range m.Proto.GetTypeDefinitions() {
		typ := td.GetType()
		for _, rel := range sortedRelations(td) {
			site := unionSite(td.GetRelations()[rel])
			if site == nil {
				continue
			}
			kids := site.Union.GetChild()
		scan:
			for j := 1; j < len(kids); j++ {
				if kids[j].GetThis() != nil {
					continue
				}
				for i := 0; i < j; i++ {
					if !proto.Equal(kids[i], kids[j]) {
						continue
					}
					idx := j
					f, good := newFinding(m, "S3", typ, rel, "duplicate union operand", "operands are syntactically identical: "+duplicateName(kids[j]),
						func(mm *Model) { dropOperand(mm, typ, rel, idx) })
					if good && l.admit != nil && !l.admit(f) {
						break // rejected: look for a duplicate of another operand
					}
					if good {
						out = append(out, f)
					}
					break scan
				}
			}
		}
	}
	return out
}
