package optimize

import (
	"context"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// genModel returns a random, validated model. Operands are biased towards
// relations defined earlier in the same type so the rewrite rules have
// redundancy to fire on. It returns nil when the draw fails validation.
// Conditions and wildcards are drawn from rc, a separate stream, so the
// other draws for a seed do not depend on them.
func genModel(r, rc *rand.Rand) *openfgav1.AuthorizationModel {
	nTypes := 2 + r.IntN(3)
	nRels := make([]int, nTypes)
	for i := range nRels {
		nRels[i] = 1 + r.IntN(4)
	}
	var sb strings.Builder
	sb.WriteString("model\n  schema 1.1\n\ntype user\n")
	parentCond := false
	for ti := range nTypes {
		parent := ti
		if r.IntN(2) == 0 {
			parent = r.IntN(nTypes)
		}
		// A second parent type and a condition on the first type, each
		// with probability 1 in 4.
		refs := fmt.Sprintf("t%d", parent)
		if r.IntN(4) == 0 {
			parentCond = true
			refs += " with cx"
		}
		if r.IntN(4) == 0 {
			if other := r.IntN(nTypes); other != parent {
				refs += fmt.Sprintf(", t%d", other)
			}
		}
		fmt.Fprintf(&sb, "\ntype t%d\n  relations\n    define parent: [%s]\n", ti, refs)
		for ri := range nRels[ti] {
			fmt.Fprintf(&sb, "    define r%d: %s\n", ri, genDef(r, ti, ri, nTypes, nRels, parent))
		}
	}
	if r.IntN(4) == 0 {
		sb.WriteString(genExclusionCycleShape(r))
		if r.IntN(2) == 0 {
			sb.WriteString(genCycleAndShape(r))
		}
	}
	dsl := sb.String()
	if rc.IntN(3) == 0 {
		dsl = genConditionsAndWildcards(rc, dsl)
	}
	if parentCond && !strings.Contains(dsl, "condition cx(") {
		dsl += "\ncondition cx(x: int) {\n  x < 10\n}\n"
	}
	lm, err := modeltest.LoadModelBytes([]byte(dsl))
	if err != nil {
		return nil
	}
	return lm.Proto
}

// genExclusionCycleShape returns types where a recursive relation sits under
// `but not` and its union has a sibling intersection with an operand of
// weight >= 2. Simplifying that intersection lowers the weight beneath the
// recursive relation, which changes how OpenFGA resolves it (and whether it
// detects the cycle).
func genExclusionCycleShape(r *rand.Rand) string {
	heavy := "define b: [user, kg#member] or a"
	if r.IntN(2) == 0 {
		heavy = "define grp: [kg]\n    define b: a or member from grp"
	}
	and := "a and b"
	if r.IntN(2) == 0 {
		and = "b and a"
	}
	union := []string{"[user]", "viewer from parent", "y"}
	r.Shuffle(len(union), func(i, j int) { union[i], union[j] = union[j], union[i] })
	return "\ntype kg\n  relations\n    define member: [user]\n" +
		"\ntype k\n  relations\n    define parent: [k]\n    define a: [user]\n    " + heavy +
		"\n    define y: " + and + "\n    define viewer: " + strings.Join(union, " or ") + "\n" +
		"\ntype kd\n  relations\n    define parent: [k]\n    define can_read: [user] but not viewer from parent\n"
}

// genCycleAndShape returns a type with an AND on a tuple cycle, which stops
// OpenFGA building the model's weighted graph and so disables its
// weight-based resolvers everywhere. S2 can drop that AND. Combined with
// genExclusionCycleShape it is the shape where a rewrite in one type changed
// an exclusion's answer in another.
func genCycleAndShape(r *rand.Rand) string {
	and := "member and wide"
	if r.IntN(2) == 0 {
		and = "wide and member"
	}
	return "\ntype kt\n  relations\n    define parent: [kt]\n    define member: [user] or both from parent\n" +
		"    define wide: [user] or member\n    define both: " + and + "\n"
}

// genConditionsAndWildcards rewrites some `[user` restrictions in dsl to
// `[user, user:*`, `[user with cx, user:*` or `[user with cx`, and declares
// the cx condition when it is used. OpenFGA's weight-2 TTU fast path answers
// wrongly when a condition-false tuple and a wildcard share an object, so
// these are the models where a weight change can flip an answer.
func genConditionsAndWildcards(r *rand.Rand, dsl string) string {
	var out strings.Builder
	for {
		before, after, ok := strings.Cut(dsl, "[user")
		out.WriteString(before)
		if !ok {
			break
		}
		dsl = after
		switch r.IntN(4) {
		case 0:
			out.WriteString("[user, user:*")
		case 1:
			out.WriteString("[user with cx, user:*")
		case 2:
			out.WriteString("[user with cx")
		default:
			out.WriteString("[user")
		}
	}
	if strings.Contains(out.String(), "with cx") {
		out.WriteString("\ncondition cx(x: int) {\n  x < 10\n}\n")
	}
	return out.String()
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
		if ri >= 2 && r.IntN(2) == 0 {
			// union base under exclusion, built from earlier relations
			a := r.IntN(ri)
			b := r.IntN(ri)
			base := fmt.Sprintf("r%d or r%d", a, b)
			if r.IntN(2) == 0 {
				base += fmt.Sprintf(" or r%d", r.IntN(ri))
			}
			return "(" + base + ") but not " + operand(false)
		}
		return operand(true) + " but not " + operand(false)
	default:
		return fmt.Sprintf("r%d from parent", r.IntN(nRels[parent]))
	}
}

// genTuples draws up to n tuples, each valid against the model's declared
// direct types. Objects are T:1..3 and users user:1..4 or user:*; a
// conditioned type gets a context, drawn from rc, that makes its condition
// true or false.
func genTuples(r, rc *rand.Rand, m *openfgav1.AuthorizationModel, n int) []*openfgav1.TupleKey {
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
		case ref.GetWildcard() != nil:
			user = ref.GetType() + ":*"
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
			if c := ref.GetCondition(); c != "" {
				// x < 10 holds for half the draws. The tuple carries the
				// whole context, so checks need none.
				ctx, _ := structpb.NewStruct(map[string]any{"x": rc.IntN(20)})
				tk.Condition = &openfgav1.RelationshipCondition{Name: c, Context: ctx}
			}
			out = append(out, tk)
		}
	}
	// A condition-false tuple written before a user:* wildcard on the same
	// relation can flip OpenFGA's resolver choice, so always exercise it.
	for _, sl := range slots {
		cond, wild := "", false
		for _, ref := range sl.refs {
			if ref.GetType() != "user" {
				continue
			}
			if ref.GetWildcard() != nil {
				wild = true
			} else if ref.GetCondition() != "" && ref.GetRelation() == "" {
				cond = ref.GetCondition()
			}
		}
		if cond == "" || !wild {
			continue
		}
		obj := sl.typ + ":1"
		out = slices.DeleteFunc(out, func(tk *openfgav1.TupleKey) bool {
			return tk.GetObject() == obj && tk.GetRelation() == sl.rel && (tk.GetUser() == "user:1" || tk.GetUser() == "user:*")
		})
		ctx, _ := structpb.NewStruct(map[string]any{"x": 16})
		out = append(out,
			&openfgav1.TupleKey{Object: obj, Relation: sl.rel, User: "user:1", Condition: &openfgav1.RelationshipCondition{Name: cond, Context: ctx}},
			&openfgav1.TupleKey{Object: obj, Relation: sl.rel, User: "user:*"})
		seen[obj+"|"+sl.rel+"|user:1"] = true
		seen[obj+"|"+sl.rel+"|user:*"] = true
	}
	// Often add cyclic parent data (2-cycles and self-loops): cycles are where
	// OpenFGA's runtime cycle detection can diverge from the lattice proof.
	if r.IntN(2) == 0 {
		add := func(obj, user, cond string) {
			if k := obj + "|parent|" + user; !seen[k] {
				seen[k] = true
				tk := &openfgav1.TupleKey{Object: obj, Relation: "parent", User: user}
				if cond != "" {
					ctx, _ := structpb.NewStruct(map[string]any{"x": rc.IntN(20)})
					tk.Condition = &openfgav1.RelationshipCondition{Name: cond, Context: ctx}
				}
				out = append(out, tk)
			}
		}
		for _, td := range m.GetTypeDefinitions() {
			for _, ref := range td.GetMetadata().GetRelations()["parent"].GetDirectlyRelatedUserTypes() {
				if ref.GetType() != td.GetType() {
					continue
				}
				if r.IntN(2) == 0 {
					add(td.GetType()+":1", td.GetType()+":1", ref.GetCondition())
				} else {
					add(td.GetType()+":1", td.GetType()+":2", ref.GetCondition())
					add(td.GetType()+":2", td.GetType()+":1", ref.GetCondition())
				}
			}
		}
	}
	// Seed the genExclusionCycleShape types with a denied doc over a k cycle.
	for _, td := range m.GetTypeDefinitions() {
		if td.GetType() != "kd" {
			continue
		}
		for _, tk := range []*openfgav1.TupleKey{
			{Object: "kd:1", Relation: "parent", User: "k:1"},
			{Object: "kd:1", Relation: "can_read", User: "user:2"},
			{Object: "k:1", Relation: "parent", User: "k:2"},
			{Object: "k:2", Relation: "parent", User: "k:1"},
		} {
			if k := tk.GetObject() + "|" + tk.GetRelation() + "|" + tk.GetUser(); !seen[k] {
				seen[k] = true
				out = append(out, tk)
			}
		}
	}
	return out
}

type checkKey struct{ object, relation, user string }

// answer is a Check outcome. An engine error is a value, so an error on one
// side of a rewrite only counts as a changed answer.
type answer uint8

const (
	deny answer = iota
	allow
	checkErr
)

func (a answer) String() string {
	switch a {
	case allow:
		return "allow"
	case checkErr:
		return "error"
	default:
		return "deny"
	}
}

// maxUsersetSubjects caps the userset subjects probed per (object, relation).
const maxUsersetSubjects = 10

// allChecks probes every relation of T:1..3 with five user subjects, then with
// userset subjects: T:n#r' for every relation of the same type, then U:1#r'
// for every other type. Only the userset list is truncated.
func allChecks(m *openfgav1.AuthorizationModel) []checkKey {
	sortedRels := func(td *openfgav1.TypeDefinition) []string {
		return slices.Sorted(maps.Keys(td.GetMetadata().GetRelations()))
	}
	var out []checkKey
	for _, td := range m.GetTypeDefinitions() {
		for _, rel := range sortedRels(td) {
			for o := 1; o <= 3; o++ {
				obj := fmt.Sprintf("%s:%d", td.GetType(), o)
				for _, u := range []string{"user:1", "user:2", "user:3", "user:4", "user:nobody"} {
					out = append(out, checkKey{obj, rel, u})
				}
				var sets []string
				for _, r2 := range sortedRels(td) {
					sets = append(sets, fmt.Sprintf("%s#%s", obj, r2))
				}
				for _, other := range m.GetTypeDefinitions() {
					if other.GetType() == td.GetType() {
						continue
					}
					for _, r2 := range sortedRels(other) {
						sets = append(sets, fmt.Sprintf("%s:1#%s", other.GetType(), r2))
					}
				}
				for _, u := range sets[:min(len(sets), maxUsersetSubjects)] {
					out = append(out, checkKey{obj, rel, u})
				}
			}
		}
	}
	return out
}

func runChecks(t *testing.T, eng modeltest.Engine, m *openfgav1.AuthorizationModel, tuples []*openfgav1.TupleKey, checks []checkKey) map[checkKey]answer {
	t.Helper()
	ctx := t.Context()
	storeID, modelID, err := eng.Setup(ctx, m, tuples)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer func() { _ = eng.DeleteStore(ctx, storeID) }()
	sc := modeltest.Scope{StoreID: storeID, ModelID: modelID}
	res := make(map[checkKey]answer, len(checks))
	for _, c := range checks {
		res[c] = checkAnswer(ctx, eng, sc, c)
	}
	return res
}

// checkAnswer runs c against the store in sc.
func checkAnswer(ctx context.Context, eng modeltest.Engine, sc modeltest.Scope, c checkKey) answer {
	ok, err := eng.Check(ctx, sc, modeltest.CheckReq{User: c.user, Relation: c.relation, Object: c.object})
	switch {
	case err != nil:
		return checkErr
	case ok:
		return allow
	default:
		return deny
	}
}

func TestGenModel_SometimesMultiParentAndConditioned(t *testing.T) {
	multi, cond := false, false
	for seed := range uint64(200) {
		r := rand.New(rand.NewPCG(seed, 0))  //nolint:gosec // deterministic test generator
		rc := rand.New(rand.NewPCG(seed, 1)) //nolint:gosec // conditions and wildcards
		p := genModel(r, rc)
		if p == nil {
			continue
		}
		m, err := NewModel(p)
		if err != nil {
			continue
		}
		dsl, _ := m.DSL()
		for _, line := range strings.Split(dsl, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "define parent:") {
				continue
			}
			if strings.Contains(line, ", t") {
				multi = true
			}
			if strings.Contains(line, " with cx") {
				cond = true
			}
		}
	}
	if !multi || !cond {
		t.Errorf("over 200 seeds: two-type parent seen = %v, conditioned parent seen = %v", multi, cond)
	}
}
