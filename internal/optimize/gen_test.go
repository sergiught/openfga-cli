package optimize

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// genModel returns a random, validated model. Operands are biased towards
// relations defined earlier in the same type so the rewrite rules have
// redundancy to fire on. It returns nil when the draw fails validation.
func genModel(r *rand.Rand) *openfgav1.AuthorizationModel {
	nTypes := 2 + r.IntN(3)
	nRels := make([]int, nTypes)
	for i := range nRels {
		nRels[i] = 1 + r.IntN(4)
	}
	var sb strings.Builder
	sb.WriteString("model\n  schema 1.1\n\ntype user\n")
	for ti := range nTypes {
		parent := ti
		if r.IntN(2) == 0 {
			parent = r.IntN(nTypes)
		}
		fmt.Fprintf(&sb, "\ntype t%d\n  relations\n    define parent: [t%d]\n", ti, parent)
		for ri := range nRels[ti] {
			fmt.Fprintf(&sb, "    define r%d: %s\n", ri, genDef(r, ti, ri, nTypes, nRels, parent))
		}
	}
	lm, err := modeltest.LoadModelBytes([]byte(sb.String()))
	if err != nil {
		return nil
	}
	return lm.Proto
}

func genDef(r *rand.Rand, ti, ri, nTypes int, nRels []int, parent int) string {
	operand := func(allowDirect bool) string {
		x := r.IntN(10)
		switch {
		case ri > 0 && x < 6:
			return fmt.Sprintf("r%d", r.IntN(ri))
		case x < 8 && allowDirect:
			return "[user]"
		default:
			return fmt.Sprintf("r%d from parent", r.IntN(nRels[parent]))
		}
	}
	switch k := r.IntN(10); {
	case k < 1:
		return "[user]"
	case k < 2:
		o := r.IntN(nTypes)
		return fmt.Sprintf("[user, t%d#r%d]", o, r.IntN(nRels[o]))
	case k < 3 && ri > 0:
		return fmt.Sprintf("r%d", r.IntN(ri)) // alias
	case k < 7:
		n := 2 + r.IntN(3)
		ops := make([]string, 0, n)
		direct := false
		for range n {
			if len(ops) > 0 && r.IntN(5) == 0 {
				ops = append(ops, ops[r.IntN(len(ops))]) // reuse an operand
				continue
			}
			o := operand(!direct)
			direct = direct || o == "[user]"
			ops = append(ops, o)
		}
		return strings.Join(ops, " or ")
	case k < 8:
		return operand(true) + " and " + operand(false)
	case k < 9:
		return operand(true) + " but not " + operand(false)
	default:
		return fmt.Sprintf("r%d from parent", r.IntN(nRels[parent]))
	}
}

// genTuples draws up to n tuples, each valid against the model's declared
// direct types. Objects are T:1..3 and users user:1..4.
func genTuples(r *rand.Rand, m *openfgav1.AuthorizationModel, n int) []*openfgav1.TupleKey {
	type slot struct {
		typ, rel string
		refs     []*openfgav1.RelationReference
	}
	var slots []slot
	for _, td := range m.GetTypeDefinitions() {
		for rel, meta := range td.GetMetadata().GetRelations() {
			if refs := meta.GetDirectlyRelatedUserTypes(); len(refs) > 0 {
				slots = append(slots, slot{td.GetType(), rel, refs})
			}
		}
	}
	// map iteration is random; sort for determinism
	for i := range slots {
		for j := i + 1; j < len(slots); j++ {
			if slots[j].typ+"#"+slots[j].rel < slots[i].typ+"#"+slots[i].rel {
				slots[i], slots[j] = slots[j], slots[i]
			}
		}
	}
	var out []*openfgav1.TupleKey
	seen := map[string]bool{}
	for range n {
		s := slots[r.IntN(len(slots))]
		ref := s.refs[r.IntN(len(s.refs))]
		user := fmt.Sprintf("%s:%d", ref.GetType(), 1+r.IntN(4))
		switch {
		case ref.GetType() == "user":
		case ref.GetRelation() != "":
			user = fmt.Sprintf("%s:%d#%s", ref.GetType(), 1+r.IntN(3), ref.GetRelation())
		default:
			user = fmt.Sprintf("%s:%d", ref.GetType(), 1+r.IntN(3))
		}
		tk := &openfgav1.TupleKey{Object: fmt.Sprintf("%s:%d", s.typ, 1+r.IntN(3)), Relation: s.rel, User: user}
		if tk.GetObject()+"#"+tk.GetRelation() == user {
			continue // implicit tuple, not writable
		}
		if k := tk.GetObject() + "|" + tk.GetRelation() + "|" + user; !seen[k] {
			seen[k] = true
			out = append(out, tk)
		}
	}
	return out
}

type checkKey struct{ object, relation, user string }

func allChecks(m *openfgav1.AuthorizationModel) []checkKey {
	var out []checkKey
	for _, td := range m.GetTypeDefinitions() {
		for rel := range td.GetMetadata().GetRelations() {
			for o := 1; o <= 3; o++ {
				for _, u := range []string{"user:1", "user:2", "user:3", "user:4", "user:nobody"} {
					out = append(out, checkKey{fmt.Sprintf("%s:%d", td.GetType(), o), rel, u})
				}
			}
		}
	}
	return out
}

func runChecks(t *testing.T, eng modeltest.Engine, m *openfgav1.AuthorizationModel, tuples []*openfgav1.TupleKey, checks []checkKey) map[checkKey]bool {
	t.Helper()
	ctx := t.Context()
	storeID, modelID, err := eng.Setup(ctx, m, tuples)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer func() { _ = eng.DeleteStore(ctx, storeID) }()
	sc := modeltest.Scope{StoreID: storeID, ModelID: modelID}
	res := make(map[checkKey]bool, len(checks))
	for _, c := range checks {
		ok, err := eng.Check(ctx, sc, modeltest.CheckReq{User: c.user, Relation: c.relation, Object: c.object})
		if err != nil {
			t.Fatalf("check %+v: %v", c, err)
		}
		res[c] = ok
	}
	return res
}
