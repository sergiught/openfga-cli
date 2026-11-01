package optimize

import (
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
)

// guardedRelations returns the relations ("type#rel") that no safe rule may
// rewrite: those reachable from the subtracted side of an exclusion that can
// also reach a cycle. OpenFGA's runtime cycle detection under an exclusion is
// evaluation-shape dependent, so the least-fixpoint proof does not carry over.
func guardedRelations(m *Model) map[string]bool {
	graph, roots := dependencyGraph(m)
	reach := func(from []string) map[string]bool { return reachable(graph, from) }

	// A node is on a cycle when it reaches itself through at least one edge.
	cyclic := map[string]bool{}
	for n, succ := range graph {
		if reach(succ)[n] {
			cyclic[n] = true
		}
	}

	guarded := map[string]bool{}
	for n := range reach(roots) {
		for r := range reach([]string{n}) {
			if cyclic[r] {
				guarded[n] = true
				break
			}
		}
	}
	return guarded
}

// dependencyGraph returns the relation dependency graph and the subtract-side
// roots described on guardedRelations.
func dependencyGraph(m *Model) (graph map[string][]string, roots []string) {
	graph = map[string][]string{}
	for _, td := range m.Proto.GetTypeDefinitions() {
		for rel, us := range td.GetRelations() {
			node := td.GetType() + "#" + rel
			graph[node] = relationEdges(m, td.GetType(), rel, us, false, nil)
			roots = relationEdges(m, td.GetType(), rel, us, true, roots)
		}
	}
	return graph, roots
}

// reachable returns every node reachable from the given nodes, themselves
// included.
func reachable(graph map[string][]string, from []string) map[string]bool {
	seen := map[string]bool{}
	queue := append([]string{}, from...)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if seen[n] {
			continue
		}
		seen[n] = true
		queue = append(queue, graph[n]...)
	}
	return seen
}

// relationEdges lists the dependency edges of typ#rel's userset. With
// subtractOnly it returns only edges found inside a difference's subtract
// side, appended to acc.
func relationEdges(m *Model, typ, rel string, us *openfgav1.Userset, subtractOnly bool, acc []string) []string {
	var out []string
	var walk func(u *openfgav1.Userset, inSub bool)
	walk = func(u *openfgav1.Userset, inSub bool) {
		emit := func(n string) {
			if !subtractOnly || inSub {
				out = append(out, n)
			}
		}
		switch v := u.GetUserset().(type) {
		case *openfgav1.Userset_This:
			for _, ref := range m.DirectTypes(typ, rel) {
				if r := ref.GetRelation(); r != "" {
					emit(ref.GetType() + "#" + r)
				}
			}
		case *openfgav1.Userset_ComputedUserset:
			emit(typ + "#" + v.ComputedUserset.GetRelation())
		case *openfgav1.Userset_TupleToUserset:
			ts := v.TupleToUserset.GetTupleset().GetRelation()
			x := v.TupleToUserset.GetComputedUserset().GetRelation()
			for _, ref := range m.DirectTypes(typ, ts) {
				if _, ok := m.Relation(ref.GetType(), x); ok {
					emit(ref.GetType() + "#" + x)
				}
			}
		case *openfgav1.Userset_Union:
			for _, c := range v.Union.GetChild() {
				walk(c, inSub)
			}
		case *openfgav1.Userset_Intersection:
			for _, c := range v.Intersection.GetChild() {
				walk(c, inSub)
			}
		case *openfgav1.Userset_Difference:
			walk(v.Difference.GetBase(), inSub)
			walk(v.Difference.GetSubtract(), true)
		}
	}
	walk(us, false)
	return append(acc, out...)
}
