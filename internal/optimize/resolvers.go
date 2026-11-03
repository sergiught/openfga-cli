package optimize

import (
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/openfga/pkg/typesystem"
)

// resolverProfile records, for every relation and user type, which resolver
// fast paths OpenFGA's typesystem picks. Keys are
// "<type>#<relation>|<userType>|<predicate>|<edgeID>". It reports false (and
// a nil map) when the typesystem or weighted graph cannot be built.
func resolverProfile(m *Model) (map[string]bool, bool) {
	ts, err := typesystem.New(m.Proto)
	if err != nil {
		return nil, false
	}
	weights, err := Weights(m)
	if err != nil {
		return nil, false
	}
	out := map[string]bool{}
	for _, td := range m.Proto.GetTypeDefinitions() {
		typ := td.GetType()
		for rel, meta := range td.GetMetadata().GetRelations() {
			ttus := collectTTUProtos(td.GetRelations()[rel], nil)
			// UsersetUseWeight2Resolver keys on the userset node's weights, so
			// a user type present only there (e.g. an intersection operand
			// lacking it on the object relation) must be profiled too.
			for _, ref := range meta.GetDirectlyRelatedUserTypes() {
				if ref.GetRelation() == "" {
					continue
				}
				userTypes := map[string]bool{}
				for u := range weights[typ+"#"+rel] {
					userTypes[u] = true
				}
				for u := range weights[ref.GetType()+"#"+ref.GetRelation()] {
					userTypes[u] = true
				}
				for userType := range userTypes {
					out[typ+"#"+rel+"|"+userType+"|userset-weight2|"+ref.GetType()+"#"+ref.GetRelation()] =
						ts.UsersetUseWeight2Resolver(typ, rel, userType, ref)
				}
			}
			for userType := range weights[typ+"#"+rel] {
				pre := typ + "#" + rel + "|" + userType + "|"
				out[pre+"userset-recursive|"] = ts.UsersetUseRecursiveResolver(typ, rel, userType)
				for _, ttu := range ttus {
					id := ttu.GetTupleset().GetRelation() + "→" + ttu.GetComputedUserset().GetRelation()
					out[pre+"ttu-weight2|"+id] = ts.TTUUseWeight2Resolver(typ, rel, userType, ttu)
					out[pre+"ttu-recursive|"+id] = ts.TTUUseRecursiveResolver(typ, rel, userType, ttu)
				}
			}
		}
	}
	return out, true
}

// collectTTUProtos appends every tuple-to-userset in the rewrite tree.
func collectTTUProtos(us *openfgav1.Userset, acc []*openfgav1.TupleToUserset) []*openfgav1.TupleToUserset {
	switch v := us.GetUserset().(type) {
	case *openfgav1.Userset_TupleToUserset:
		acc = append(acc, v.TupleToUserset)
	case *openfgav1.Userset_Union:
		for _, c := range v.Union.GetChild() {
			acc = collectTTUProtos(c, acc)
		}
	case *openfgav1.Userset_Intersection:
		for _, c := range v.Intersection.GetChild() {
			acc = collectTTUProtos(c, acc)
		}
	case *openfgav1.Userset_Difference:
		acc = collectTTUProtos(v.Difference.GetBase(), acc)
		acc = collectTTUProtos(v.Difference.GetSubtract(), acc)
	}
	return acc
}

// sameResolvers reports whether every profile key present in before is
// unchanged in after and after has no key before lacks.
func sameResolvers(before, after map[string]bool) bool {
	for k, v := range after {
		if bv, ok := before[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
