package optimize

import (
	"maps"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

const c1Model = `model
  schema 1.1

type user

type folder
  relations
    define parent: [folder]
    define viewer: [user] or viewer from parent or can_view from parent
    define can_view: viewer

type doc
  relations
    define parent: [folder]
    define can_read: [user] but not viewer from parent
`

func exclusionSite(t *testing.T, dsl string) string {
	t.Helper()
	site, ok := recursiveExclusion(loadInline(t, dsl))
	if ok != (site != "") {
		t.Fatalf("recursiveExclusion = (%q, %v)", site, ok)
	}
	return site
}

func TestGuard_C1ExclusionIsRecursive(t *testing.T) {
	if got := exclusionSite(t, c1Model); got != "doc#can_read" {
		t.Fatalf("site = %q, want doc#can_read", got)
	}
}

func TestGuard_AcyclicSubtractDoesNotBlock(t *testing.T) {
	got := exclusionSite(t, `model
  schema 1.1

type user

type doc
  relations
    define blocked: [user]
    define viewer: [user]
    define can_read: viewer but not blocked
`)
	if got != "" {
		t.Fatalf("site = %q, want none", got)
	}
}

func TestGuard_RecursionOutsideSubtractDoesNotBlock(t *testing.T) {
	got := exclusionSite(t, `model
  schema 1.1

type user

type folder
  relations
    define parent: [folder]
    define viewer: [user] or viewer from parent

type doc
  relations
    define blocked: [user]
    define can_read: [user] but not blocked
`)
	if got != "" {
		t.Fatalf("site = %q, want none", got)
	}
}

func TestGuard_UsersetDirectTypeEdgeIntoCycle(t *testing.T) {
	got := exclusionSite(t, `model
  schema 1.1

type user

type folder
  relations
    define parent: [folder]
    define viewer: [user] or viewer from parent

type doc
  relations
    define reader: [user, folder#viewer]
    define can_read: [user] but not reader
`)
	if got != "doc#can_read" {
		t.Fatalf("site = %q, want doc#can_read", got)
	}
}

// TestGuard_PicksFirstSortedSite has two recursive exclusions; the reported
// one must not depend on map iteration order.
func TestGuard_PicksFirstSortedSite(t *testing.T) {
	for range 10 {
		got := exclusionSite(t, `model
  schema 1.1

type user

type folder
  relations
    define parent: [folder]
    define viewer: [user] or viewer from parent
    define z: [user] but not viewer
    define b: [user] but not viewer
`)
		if got != "folder#b" {
			t.Fatalf("site = %q, want folder#b", got)
		}
	}
}

func TestRewrite_C1RegressionDenyStaysDeny(t *testing.T) {
	m := loadInline(t, c1Model)
	rw, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	assertBlocked(t, rw)
	tuples := []*openfgav1.TupleKey{
		{Object: "doc:1", Relation: "parent", User: "folder:1"},
		{Object: "doc:1", Relation: "can_read", User: "user:2"},
		{Object: "folder:1", Relation: "parent", User: "folder:2"},
		{Object: "folder:2", Relation: "parent", User: "folder:1"},
	}
	checks := []checkKey{{"doc:1", "can_read", "user:2"}}
	eng, err := modeltest.NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	before := runChecks(t, eng, rw.Before.Proto, tuples, checks)
	after := runChecks(t, eng, rw.After.Proto, tuples, checks)
	if !maps.Equal(before, after) {
		t.Fatalf("answers differ: before=%v after=%v", before, after)
	}
}

// cNewModel has a recursive exclusion in doc and an AND on a tuple cycle in
// the unrelated team type. That AND stops OpenFGA from building its weighted
// graph, which disables weight-based resolvers model-wide; simplifying it
// (both: member and wide -> member) re-enables them and changes how the
// exclusion's cycle is resolved.
const cNewModel = `model
  schema 1.1

type user

type folder
  relations
    define parent: [folder]
    define viewer: [user] or viewer from parent

type doc
  relations
    define parent: [folder]
    define can_read: [user] but not viewer from parent

type team
  relations
    define parent: [team]
    define member: [user] or both from parent
    define wide: [user] or member
    define both: member and wide
`

func TestRewrite_CNewRegressionDenyStaysDeny(t *testing.T) {
	m := loadInline(t, cNewModel)
	rw, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	tuples := []*openfgav1.TupleKey{
		{Object: "doc:1", Relation: "can_read", User: "user:2"},
		{Object: "doc:1", Relation: "parent", User: "folder:1"},
		{Object: "folder:1", Relation: "parent", User: "folder:1"},
	}
	checks := []checkKey{{"doc:1", "can_read", "user:2"}}
	eng, err := modeltest.NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	for i := range 3 {
		before := runChecks(t, eng, rw.Before.Proto, tuples, checks)
		after := runChecks(t, eng, rw.After.Proto, tuples, checks)
		if !maps.Equal(before, after) {
			t.Fatalf("run %d: answers differ: before=%v after=%v applied=%+v", i, before, after, rw.Applied)
		}
	}
	assertBlocked(t, rw)
}

// assertBlocked fails unless the model-wide gate stopped every rewrite.
func assertBlocked(t *testing.T, rw *RewriteResult) {
	t.Helper()
	if len(rw.Applied) != 0 {
		t.Errorf("applied = %+v, want none", rw.Applied)
	}
	if !strings.HasPrefix(rw.Blocked, "doc#can_read subtracts a recursive relation") {
		t.Errorf("Blocked = %q, want it to name doc#can_read", rw.Blocked)
	}
}

// k1Model has a non-cyclic folder#y beneath the recursive folder#viewer on the
// subtracted side. Rewriting y (a and b -> a) lowers its weight, which switches
// OpenFGA to its recursive TTU resolver for viewer and drops cycle detection.
const k1Model = `model
  schema 1.1

type user

type group
  relations
    define member: [user]

type folder
  relations
    define parent: [folder]
    define a: [user]
    define b: [user, group#member] or a
    define y: a and b
    define viewer: [user] or viewer from parent or y

type doc
  relations
    define parent: [folder]
    define can_read: [user] but not viewer from parent
`

func TestRewrite_K1RegressionDenyStaysDeny(t *testing.T) {
	m := loadInline(t, k1Model)
	rw, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	assertBlocked(t, rw)
	tuples := []*openfgav1.TupleKey{
		{Object: "doc:1", Relation: "parent", User: "folder:1"},
		{Object: "doc:1", Relation: "can_read", User: "user:2"},
		{Object: "folder:1", Relation: "parent", User: "folder:2"},
		{Object: "folder:2", Relation: "parent", User: "folder:1"},
	}
	checks := []checkKey{{"doc:1", "can_read", "user:2"}}
	eng, err := modeltest.NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	for i := range 3 {
		before := runChecks(t, eng, rw.Before.Proto, tuples, checks)
		after := runChecks(t, eng, rw.After.Proto, tuples, checks)
		if !maps.Equal(before, after) {
			t.Fatalf("run %d: answers differ: before=%v after=%v", i, before, after)
		}
	}
}

// TestRewrite_KeepsWeightedGraphBuildability has no exclusion, so the gate is
// open, but S2 on team#both would remove the AND on team's tuple cycle and let
// OpenFGA build a weighted graph it cannot build today. That rewrite must be
// discarded.
func TestRewrite_KeepsWeightedGraphBuildability(t *testing.T) {
	m := loadInline(t, `model
  schema 1.1

type user

type team
  relations
    define parent: [team]
    define member: [user] or both from parent
    define wide: [user] or member
    define both: member and wide
`)
	if weightedGraphBuilds(m) {
		t.Fatal("weighted graph builds; the test needs a model where it does not")
	}
	cand := m.Clone()
	f := findingFor(t, m, "S2", "team", "both")
	f.apply(cand)
	if !weightedGraphBuilds(cand) {
		t.Fatal("S2 on team#both does not flip buildability; the test is vacuous")
	}
	rw, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	if rw.Blocked != "" {
		t.Fatalf("Blocked = %q, want the gate open", rw.Blocked)
	}
	for _, f := range rw.Applied {
		if f.Type == "team" && f.Relation == "both" {
			t.Fatalf("buildability-changing rewrite applied: %+v", f)
		}
	}
	if weightedGraphBuilds(rw.After) != weightedGraphBuilds(m) {
		t.Fatal("rewritten model's buildability differs from the original")
	}
}

// findingFor returns the safe finding with id on typ#rel in m's first pass.
func findingFor(t *testing.T, m *Model, id, typ, rel string) Finding {
	t.Helper()
	l := BuildLattice(m)
	for _, r := range safeRules {
		for _, f := range r(m, l) {
			if f.ID == id && f.Type == typ && f.Relation == rel {
				return f
			}
		}
	}
	t.Fatalf("no %s finding on %s#%s", id, typ, rel)
	return Finding{}
}

// cWildcardModel has a conditioned user and a user:* wildcard on folder#r0.
// S2 can simplify y (r0 and heavy -> r0), which lowers y's weight and makes
// OpenFGA take its weight-2 TTU fast path for doc#viewer. That path answers
// flakily wrong when a condition-false tuple and a wildcard share an object.
const cWildcardModel = `model
  schema 1.1

type user

type group
  relations
    define member: [user]

type folder
  relations
    define r0: [user with cx, user:*]
    define heavy: [user, group#member] or r0
    define y: r0 and heavy

type doc
  relations
    define parent: [folder]
    define viewer: y from parent

condition cx(x: int) {
  x < 10
}
`

func TestRewrite_CWildcardConditionKeepsWeights(t *testing.T) {
	m := loadInline(t, cWildcardModel)
	findingFor(t, m, "S2", "folder", "y") // the rewrite the guard must refuse
	rw, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rw.Applied {
		if f.Type == "folder" && f.Relation == "y" {
			t.Fatalf("weight-changing rewrite applied: %+v", f)
		}
	}
	ctx16, err := structpb.NewStruct(map[string]any{"x": 16})
	if err != nil {
		t.Fatal(err)
	}
	tuples := []*openfgav1.TupleKey{
		{Object: "doc:1", Relation: "parent", User: "folder:1"},
		{Object: "folder:1", Relation: "r0", User: "user:1", Condition: &openfgav1.RelationshipCondition{Name: "cx", Context: ctx16}},
		{Object: "folder:1", Relation: "r0", User: "user:*"},
	}
	eng, err := modeltest.NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	ctx := t.Context()
	storeID, modelID, err := eng.Setup(ctx, rw.After.Proto, tuples)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.DeleteStore(ctx, storeID) }()
	sc := modeltest.Scope{StoreID: storeID, ModelID: modelID}
	for i := range 20 {
		ok, err := eng.Check(ctx, sc, modeltest.CheckReq{User: "user:1", Relation: "viewer", Object: "doc:1"})
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("check %d: doc:1#viewer@user:1 = false, want true", i)
		}
	}
}
