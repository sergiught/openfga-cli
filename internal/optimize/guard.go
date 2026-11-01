package optimize

import (
	"slices"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
)

// recursiveExclusion returns an exclusion ("type#rel", the first in sorted
// order) whose subtracted side can reach a cycle in the relation dependency
// graph. When OpenFGA detects a cycle under an exclusion, the exclusion
// denies, and whether it detects one depends on the resolver it picks. That
// choice is model-wide: an AND or BUT NOT on a tuple cycle anywhere stops
// OpenFGA building its weighted graph and disables the weight-based
// resolvers everywhere. So a rewrite in any type can change such an answer,
// and the least-fixpoint proof does not carry over to any of them.
func recursiveExclusion(m *Model) (site string, ok bool) {
	graph := dependencyGraph(m)
	cyclic := cyclicRelations(graph)
	var sites []string
	for _, td := range m.Proto.GetTypeDefinitions() {
		for rel, us := range td.GetRelations() {
			for n := range reachable(graph, relationEdges(m, td.GetType(), rel, us, true)) {
				if cyclic[n] {
					sites = append(sites, td.GetType()+"#"+rel)
					break
				}
			}
		}
	}
	if len(sites) == 0 {
		return "", false
	}
	return slices.Min(sites), true
}

// Dependents returns every relation ("type#rel") of m that can reach one of
// targets in the relation dependency graph, targets included.
func Dependents(m *Model, targets []string) map[string]bool {
	graph := dependencyGraph(m)
	out := map[string]bool{}
	for n := range graph {
		reach := reachable(graph, []string{n})
		for _, t := range targets {
			if reach[t] {
				out[n] = true
				break
			}
		}
	}
	return out
}

// cyclicRelations returns the nodes of graph that lie on a cycle, i.e. reach
// themselves through at least one edge.
func cyclicRelations(graph map[string][]string) map[string]bool {
	cyclic := map[string]bool{}
	for n, succ := range graph {
		if reachable(graph, succ)[n] {
			cyclic[n] = true
		}
	}
	return cyclic
}

// dependencyGraph returns the relation dependency graph: an edge from each
// "type#rel" to every relation its definition can evaluate.
func dependencyGraph(m *Model) map[string][]string {
	graph := map[string][]string{}
	for _, td := range m.Proto.GetTypeDefinitions() {
		for rel, us := range td.GetRelations() {
			graph[td.GetType()+"#"+rel] = relationEdges(m, td.GetType(), rel, us, false)
		}
	}
	return graph
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
// side.
func relationEdges(m *Model, typ, rel string, us *openfgav1.Userset, subtractOnly bool) []string {
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
			emit(typ + "#" + ts)
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
	return out
}
