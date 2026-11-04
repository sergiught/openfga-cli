package optimize

import (
	"os"
	"slices"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
)

func TestRewrite_ReferenceModelAppliesExactlyFour(t *testing.T) {
	rw, err := Rewrite(LoadReferenceModel(t))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rw.Applied))
	for _, f := range rw.Applied {
		got = append(got, f.ID+" "+f.Type+"#"+f.Relation+": "+f.Before+" -> "+f.After)
	}
	want := []string{
		"S1 organization#can_view: member or admin -> member",
		"S1 folder#viewer: [user, group#member] or editor or owner or viewer from parent -> [user, group#member] or editor or viewer from parent",
		"S2 folder#can_delete: owner and editor -> owner",
		"S1 document#viewer: [user] or editor or owner or viewer from parent or member from org -> [user] or editor or viewer from parent or member from org",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("applied findings:\n got %q\nwant %q", got, want)
	}
	again, err := Rewrite(rw.After)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Applied) != 0 {
		t.Fatalf("not idempotent: %+v", again.Applied)
	}
	dsl, err := rw.After.DSL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dsl, "define can_delete: owner\n") {
		t.Fatalf("missing rewritten can_delete in:\n%s", dsl)
	}
	// Before is untouched.
	orig, _ := rw.Before.DSL()
	if !strings.Contains(orig, "define can_delete: owner and editor") {
		t.Fatalf("Before was mutated:\n%s", orig)
	}
}

func TestRewrite_FixpointCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define r0: [user]\n")
	for i := 1; i <= 12; i++ {
		b.WriteString("    define r" + itoa(i) + ": r" + itoa(i-1) + "\n")
	}
	// Wide union over a chain of aliases, each contained in the next: one drop per pass.
	b.WriteString("    define v: r0")
	for i := 1; i <= 12; i++ {
		b.WriteString(" or r" + itoa(i))
	}
	b.WriteString("\n")
	rw, err := Rewrite(loadInline(t, b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(rw.Applied) != 10 {
		t.Fatalf("applied %d findings, want exactly 10 (cap)", len(rw.Applied))
	}
	if _, err := rw.After.DSL(); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}

const seed11Model = `model
  schema 1.1
type user
type t1
  relations
    define parent: [t1]
    define r0: [user] or r0 from parent or r3 from parent or r3 from parent
    define r1: r2 from parent or r0 or r2 from parent or r2 from parent
    define r2: (r1 or r1) but not r3 from parent
    define r3: (r0 or r1) but not r1 from parent
`

func TestRewrite_Seed11ProducesValidModel(t *testing.T) {
	rw, err := Rewrite(loadInline(t, seed11Model))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewModel(rw.After.Proto); err != nil {
		t.Fatalf("after does not validate: %v", err)
	}
}

func TestRewrite_SkipsFindingThatInvalidatesModel(t *testing.T) {
	m := loadInline(t, "model\n  schema 1.1\ntype user\ntype doc\n  relations\n    define a: [user]\n    define b: a\n    define c: a or b\n")
	good := ruleS1(m, BuildLattice(m))
	if len(good) != 1 {
		t.Fatalf("expected one S1 finding, got %d", len(good))
	}
	bad := Finding{ID: "S1", Type: "doc", Relation: "a", Tier: TierSafe, apply: func(m *Model) {
		for _, td := range m.Proto.GetTypeDefinitions() {
			if td.GetType() != "doc" {
				continue
			}
			td.GetRelations()["a"] = &openfgav1.Userset{Userset: &openfgav1.Userset_ComputedUserset{
				ComputedUserset: &openfgav1.ObjectRelation{Relation: "a"},
			}}
		}
	}}
	orig := safeRules
	defer func() { safeRules = orig }()
	calls := 0
	safeRules = []rule{func(*Model, *Lattice) []Finding {
		if calls++; calls > 1 {
			return nil
		}
		return append([]Finding{bad}, good...)
	}}
	rw, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(rw.Applied) != 1 || rw.Applied[0].Relation != "c" {
		t.Fatalf("applied = %+v, want only the valid finding on c", rw.Applied)
	}
}

func keySet(keys []FindingKey) map[FindingKey]bool {
	s := map[FindingKey]bool{}
	for _, k := range keys {
		s[k] = true
	}
	return s
}

func TestRewriteSubset_AllEqualsRewrite(t *testing.T) {
	m := LoadReferenceModel(t)
	full, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := RewriteSubset(m, keySet(Keys(full.Applied)))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(Keys(sub.Applied), Keys(full.Applied)) {
		t.Fatalf("keys differ: got %v want %v", Keys(sub.Applied), Keys(full.Applied))
	}
	got, _ := sub.After.DSL()
	want, _ := full.After.DSL()
	if got != want {
		t.Fatalf("DSL differs:\n%s\nwant:\n%s", got, want)
	}
}

func TestRewriteSubset_NoneIsIdentity(t *testing.T) {
	m := LoadReferenceModel(t)
	for _, accept := range []map[FindingKey]bool{{}, nil} {
		sub, err := RewriteSubset(m, accept)
		if err != nil {
			t.Fatal(err)
		}
		if len(sub.Applied) != 0 {
			t.Fatalf("applied = %+v, want none", sub.Applied)
		}
		got, _ := sub.After.DSL()
		want, _ := m.DSL()
		if got != want {
			t.Fatalf("After differs from input:\n%s", got)
		}
	}
}

func TestRewriteSubset_OnlyAccepted(t *testing.T) {
	m := LoadReferenceModel(t)
	full, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	all := Keys(full.Applied)
	if len(all) != 4 {
		t.Fatalf("want 4 keys, got %d", len(all))
	}
	for _, k := range all {
		accept := keySet(all)
		delete(accept, k)
		sub, err := RewriteSubset(m, accept)
		if err != nil {
			t.Fatal(err)
		}
		for _, got := range Keys(sub.Applied) {
			if !accept[got] {
				t.Errorf("rejecting %v: applied unaccepted %v", k, got)
			}
		}
		if slices.Contains(Keys(sub.Applied), k) {
			t.Errorf("rejected %v was applied", k)
		}
		if _, err := NewModel(sub.After.Proto); err != nil {
			t.Errorf("rejecting %v: invalid After: %v", k, err)
		}
	}
}

func TestRewriteSubset_BlockedModel(t *testing.T) {
	m := loadInline(t, c1Model)
	full, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, accept := range []map[FindingKey]bool{nil, {}, keySet(Keys(full.Applied)), {{Type: "doc", Relation: "can_read", ID: "S1"}: true}} {
		sub, err := RewriteSubset(m, accept)
		if err != nil {
			t.Fatal(err)
		}
		assertBlocked(t, sub)
	}
}

func TestKeys_Occurrence(t *testing.T) {
	got := Keys([]Finding{
		{ID: "S1", Type: "doc", Relation: "viewer"},
		{ID: "S2", Type: "doc", Relation: "editor"},
		{ID: "S1", Type: "doc", Relation: "viewer"},
	})
	want := []FindingKey{
		{Type: "doc", Relation: "viewer", ID: "S1", N: 0},
		{Type: "doc", Relation: "editor", ID: "S2", N: 0},
		{Type: "doc", Relation: "viewer", ID: "S1", N: 1},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRewrite_ResolverGateKeepsDuplicateTTUDrop(t *testing.T) {
	m := loadInline(t, "model\n  schema 1.1\ntype user\ntype folder\n  relations\n    define parent: [folder]\n    define x: [user]\n    define v: x from parent or x from parent\n")
	rw, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(rw.Applied) != 1 || rw.Applied[0].ID != "S3" || rw.Applied[0].After != "x from parent" {
		t.Errorf("applied = %+v, want one S3 with After %q", rw.Applied, "x from parent")
	}
}

func TestRewriteSubset_RejectedDoesNotShiftOrdinal(t *testing.T) {
	m := loadInline(t, "model\n  schema 1.1\ntype user\ntype doc\n  relations\n    define owner: [user]\n    define editor: [user] or owner\n    define admin: [user] or editor\n    define viewer: [user] or owner or editor or admin\n")
	rw, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	keys := Keys(rw.Applied)
	if len(keys) < 2 || keys[0].Type != keys[1].Type || keys[0].Relation != keys[1].Relation || keys[0].ID != keys[1].ID || keys[0].Proof == keys[1].Proof {
		t.Fatalf("need two findings sharing (Type, Relation, ID) with different Proof, got %+v", keys)
	}
	// The second finding only exists once the first is applied, so accepting
	// it alone applies nothing; accepting the first applies exactly the first.
	sub, err := RewriteSubset(m, map[FindingKey]bool{keys[1]: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Applied) != 0 {
		t.Fatalf("applied = %+v, want none", sub.Applied)
	}
	sub, err = RewriteSubset(m, map[FindingKey]bool{keys[0]: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Applied) != 1 || sub.Applied[0].Proof != rw.Applied[0].Proof {
		t.Fatalf("applied = %+v, want only the finding with proof %q", sub.Applied, rw.Applied[0].Proof)
	}
	// A key with the right ordinal but another finding's proof must not match.
	stale := FindingKey{Type: keys[0].Type, Relation: keys[0].Relation, ID: keys[0].ID, Proof: "other"}
	if sub, err = RewriteSubset(m, map[FindingKey]bool{stale: true}); err != nil || len(sub.Applied) != 0 {
		t.Fatalf("stale-proof key applied %+v (err %v)", sub.Applied, err)
	}
	rep, err := Analyze(m.Proto)
	if err != nil {
		t.Fatal(err)
	}
	for i, k := range keys {
		if rep.Findings[i].Key != k.String() {
			t.Errorf("Findings[%d].Key = %q, want %q", i, rep.Findings[i].Key, k.String())
		}
		if rep.Rewrite != nil && i < len(rep.Rewrite.Applied) && rep.Rewrite.Applied[i].Key != rep.Findings[i].Key {
			t.Errorf("Rewrite.Applied[%d].Key = %q, want %q", i, rep.Rewrite.Applied[i].Key, rep.Findings[i].Key)
		}
	}
}

func TestRewrite_GatedFindingIsReported(t *testing.T) {
	data, err := os.ReadFile("testdata/ttu_multi_parent_vacuous.fga")
	if err != nil {
		t.Fatal(err)
	}
	rw, err := Rewrite(loadInline(t, string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(rw.Applied) != 0 {
		t.Fatalf("applied = %+v, want none", rw.Applied)
	}
	if len(rw.Gated) != 1 {
		t.Fatalf("gated = %+v, want one", rw.Gated)
	}
	g := rw.Gated[0]
	if g.ID != "S4" || g.Type != "doc" || g.Relation != "viewer" {
		t.Errorf("gated = %s %s#%s, want S4 doc#viewer", g.ID, g.Type, g.Relation)
	}
	if want := "changes whether OpenFGA can build the weighted graph"; g.Gate != want {
		t.Errorf("gate = %q, want %q", g.Gate, want)
	}
	rep, err := Analyze(loadInline(t, string(data)).Proto)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rewrite != nil || len(rep.Gated) != 1 || rep.Gated[0].Key == "" {
		t.Errorf("report: rewrite=%v gated=%+v, want nil rewrite and one keyed gated finding", rep.Rewrite, rep.Gated)
	}
}
