package optimize

import (
	"maps"
	"slices"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

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

func guardedList(t *testing.T, dsl string) []string {
	t.Helper()
	return slices.Sorted(maps.Keys(guardedRelations(loadInline(t, dsl))))
}

func TestGuard_ReproGuardsFolderRelations(t *testing.T) {
	got := guardedList(t, c1Model)
	want := []string{"folder#can_view", "folder#viewer"}
	if !slices.Equal(got, want) {
		t.Fatalf("guarded = %v, want %v", got, want)
	}
}

func TestGuard_AcyclicSubtractGuardsNothing(t *testing.T) {
	got := guardedList(t, `model
  schema 1.1

type user

type doc
  relations
    define blocked: [user]
    define viewer: [user]
    define can_read: viewer but not blocked
`)
	if len(got) != 0 {
		t.Fatalf("guarded = %v, want none", got)
	}
}

func TestGuard_RecursiveNotUnderSubtractNotGuarded(t *testing.T) {
	got := guardedList(t, `model
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
	if len(got) != 0 {
		t.Fatalf("guarded = %v, want none", got)
	}
}

func TestGuard_UsersetDirectTypeEdgeIntoCycle(t *testing.T) {
	got := guardedList(t, `model
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
	want := []string{"doc#reader", "folder#viewer"}
	if !slices.Equal(got, want) {
		t.Fatalf("guarded = %v, want %v", got, want)
	}
}

func TestRewrite_C1RegressionDenyStaysDeny(t *testing.T) {
	m := loadInline(t, c1Model)
	rw, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rw.Applied {
		if f.Type == "folder" && f.Relation == "viewer" {
			t.Fatalf("guarded relation rewritten: %+v", f)
		}
	}
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

func TestGuard_K1GuardsWholeRegionUnderCycle(t *testing.T) {
	g := guardedRelations(loadInline(t, k1Model))
	for _, rel := range []string{"folder#y", "folder#a", "folder#b", "folder#viewer"} {
		if !g[rel] {
			t.Errorf("%s not guarded; guarded = %v", rel, slices.Sorted(maps.Keys(g)))
		}
	}
}

func TestRewrite_K1RegressionDenyStaysDeny(t *testing.T) {
	m := loadInline(t, k1Model)
	rw, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rw.Applied {
		if f.Type == "folder" && f.Relation == "y" {
			t.Errorf("guarded relation rewritten: %+v", f)
		}
	}
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
