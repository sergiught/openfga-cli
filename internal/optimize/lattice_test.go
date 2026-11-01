package optimize

import (
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func loadInline(t *testing.T, dsl string) *Model {
	t.Helper()
	lm, err := modeltest.LoadModelBytes([]byte(dsl))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModel(lm.Proto)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func comp(typ, rel string) Term { return Term{Type: typ, Computed: rel} }

func ttu(typ, ts, c string) Term {
	return Term{Type: typ, TTU: &struct{ Tupleset, Computed string }{ts, c}}
}

func wantSubset(t *testing.T, l *Lattice, a, b Term, want bool) {
	t.Helper()
	if got := l.Subset(a, b); got != want {
		t.Fatalf("Subset(%+v, %+v) = %v, want %v", a, b, got, want)
	}
}

func TestLattice_ReferenceModel(t *testing.T) {
	l := BuildLattice(LoadReferenceModel(t))
	wantSubset(t, l, comp("organization", "admin"), comp("organization", "member"), true)
	wantSubset(t, l, comp("folder", "owner"), comp("folder", "editor"), true)
	wantSubset(t, l, comp("folder", "owner"), comp("folder", "viewer"), true) // transitive
	wantSubset(t, l, comp("folder", "viewer"), comp("folder", "owner"), false)
	// can_view is an alias of viewer.
	if !l.Equivalent(comp("folder", "can_view"), comp("folder", "viewer")) {
		t.Fatal("folder can_view should be equivalent to viewer")
	}
	// Nothing is derived about intersection operands.
	wantSubset(t, l, comp("folder", "owner"), comp("folder", "can_delete"), false)
	wantSubset(t, l, comp("document", "editor"), comp("document", "can_edit"), false)
	// viewer from parent on document: parent is [folder]; folder editor ⊆ viewer
	// but the TTU pair (editor from parent) ⊆ (viewer from parent) holds.
	wantSubset(t, l, ttu("document", "parent", "editor"), ttu("document", "parent", "viewer"), true)
	wantSubset(t, l, ttu("document", "parent", "viewer"), ttu("document", "parent", "editor"), false)
}

// operands returns the terms of the union operands of typ#rel (or of the
// whole definition when it is not a union).
func operands(t *testing.T, m *Model, typ, rel string) []Term {
	t.Helper()
	us, ok := m.Relation(typ, rel)
	if !ok {
		t.Fatalf("no relation %s#%s", typ, rel)
	}
	md := m.Proto.GetTypeDefinitions()
	var meta *openfgav1.RelationMetadata
	for _, td := range md {
		if td.GetType() == typ {
			meta = td.GetMetadata().GetRelations()[rel]
		}
	}
	children := []*openfgav1.Userset{us}
	if u := us.GetUnion(); u != nil {
		children = u.GetChild()
	}
	var out []Term
	for _, c := range children {
		term, ok := TermOf(typ, rel, c, meta)
		if !ok {
			t.Fatalf("operand of %s#%s is not a term", typ, rel)
		}
		out = append(out, term)
	}
	return out
}

func TestLattice_ConditionedDirectIsUnique(t *testing.T) {
	m := loadInline(t, `model
  schema 1.1
type user
type doc
  relations
    define a: [user]
    define b: [user with c]
    define w: [user, user:*]
    define v: [user] or b or w
condition c(x: int) {
  x > 1
}
`)
	l := BuildLattice(m)
	a := operands(t, m, "doc", "a")[0]
	b := operands(t, m, "doc", "b")[0]
	w := operands(t, m, "doc", "w")[0]
	if a == b || a == w || b == w {
		t.Fatal("direct terms must differ by condition and wildcard")
	}
	wantSubset(t, l, a, b, false)
	wantSubset(t, l, b, a, false)
	wantSubset(t, l, a, w, false)
	wantSubset(t, l, w, a, false)
}

func TestLattice_TTUMissingRelationIsVacuous(t *testing.T) {
	// org lacks x/y entirely: the term is empty there, so it holds vacuously.
	m := loadInline(t, `model
  schema 1.1
type user
type org
type folder
  relations
    define x: [user]
    define y: [user]
type doc
  relations
    define parent: [org]
    define a: [user]
`)
	l := BuildLattice(m)
	wantSubset(t, l, ttu("doc", "parent", "x"), ttu("doc", "parent", "y"), true)

	// parent: [folder, org]; folder has x and y with x not subset of y.
	m = loadInline(t, `model
  schema 1.1
type user
type org
type folder
  relations
    define x: [user]
    define y: [user]
type doc
  relations
    define parent: [folder, org]
`)
	l = BuildLattice(m)
	wantSubset(t, l, ttu("doc", "parent", "x"), ttu("doc", "parent", "y"), false)

	// Same, but folder x is a union member of y: holds.
	m = loadInline(t, `model
  schema 1.1
type user
type org
type folder
  relations
    define x: [user]
    define y: [user] or x
type doc
  relations
    define parent: [folder, org]
`)
	l = BuildLattice(m)
	wantSubset(t, l, ttu("doc", "parent", "x"), ttu("doc", "parent", "y"), true)
	wantSubset(t, l, ttu("doc", "parent", "y"), ttu("doc", "parent", "x"), false)
}

func TestLattice_DirectTermsScopedToRelation(t *testing.T) {
	m := loadInline(t, `model
  schema 1.1
type user
type doc
  relations
    define a: [user] or x
    define x: [user]
    define v: [user] or a
`)
	l := BuildLattice(m)
	aDirect := operands(t, m, "doc", "a")[0]
	vDirect := operands(t, m, "doc", "v")[0]
	if aDirect == vDirect {
		t.Fatal("direct terms of different relations must differ")
	}
	// doc:1#v@user:1 does not grant a.
	wantSubset(t, l, vDirect, comp("doc", "a"), false)
	wantSubset(t, l, vDirect, aDirect, false)
	wantSubset(t, l, vDirect, comp("doc", "v"), true)
	wantSubset(t, l, comp("doc", "a"), comp("doc", "v"), true)
}

func TestLattice_RecursiveTTUIsLeastFixpoint(t *testing.T) {
	m := loadInline(t, `model
  schema 1.1
type user
type doc
  relations
    define parent: [doc]
    define viewer: [user] or viewer from parent
`)
	l := BuildLattice(m)
	rec := ttu("doc", "parent", "viewer")
	wantSubset(t, l, rec, comp("doc", "viewer"), true)
	wantSubset(t, l, comp("doc", "viewer"), rec, false)
}

func TestLattice_NoFactsFromIntersectionOrExclusion(t *testing.T) {
	m := loadInline(t, `model
  schema 1.1
type user
type doc
  relations
    define a: [user]
    define b: [user]
    define i: a and b
    define e: a but not b
`)
	l := BuildLattice(m)
	wantSubset(t, l, comp("doc", "a"), comp("doc", "i"), false)
	wantSubset(t, l, comp("doc", "i"), comp("doc", "a"), false)
	wantSubset(t, l, comp("doc", "a"), comp("doc", "e"), false)
	wantSubset(t, l, comp("doc", "b"), comp("doc", "e"), false)
}
