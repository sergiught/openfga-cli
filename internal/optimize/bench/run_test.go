package bench

import (
	"context"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
)

func find(t *testing.T, cs []CheckResult, name string) CheckResult {
	t.Helper()
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not found", name)
	return CheckResult{}
}

func TestRun_ReferenceModelDenyReadsHalve(t *testing.T) {
	m, err := optimize.NewModel(loadRef(t))
	if err != nil {
		t.Fatal(err)
	}
	rw, err := optimize.Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	src, err := Synthesize(rw.Before.Proto, SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), rw.Before.Proto, rw.After.Proto, src, Options{Iterations: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Mismatches) != 0 {
		t.Fatalf("mismatches: %+v", res.Mismatches)
	}
	if res.Totals.DenyReadsAfter >= res.Totals.DenyReadsBefore {
		t.Errorf("deny reads did not drop: %d -> %d", res.Totals.DenyReadsBefore, res.Totals.DenyReadsAfter)
	}
	deny := find(t, res.Checks, "deny document#can_view")
	t.Logf("deny document#can_view reads: %d -> %d", deny.Before.Reads, deny.After.Reads)
	if deny.After.Reads >= deny.Before.Reads*6/10 {
		t.Errorf("deny reads %d -> %d, want after < 60%% of before", deny.Before.Reads, deny.After.Reads)
	}
	if deny.Before.P50Ms < 0 || deny.Before.P95Ms < deny.Before.P50Ms {
		t.Errorf("bad latency %+v", deny.Before)
	}
	if res.TupleCount == 0 || len(res.Tuples) != res.TupleCount {
		t.Errorf("tuples not recorded: count %d, listed %d", res.TupleCount, len(res.Tuples))
	}
}

func TestRun_DenyCountsDeterministic(t *testing.T) {
	ref := loadRef(t)
	src, err := Synthesize(ref, SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	run := func() *Result {
		r, err := Run(context.Background(), ref, nil, src, Options{Iterations: 1})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a, b := run(), run()
	unstable := shortCircuitRelations(ref)
	checked := 0
	for i, c := range a.Checks {
		if a.Checks[i].Allowed || unstable[c.Relation] {
			continue
		}
		checked++
		if ra, rb := c.Before.Reads, b.Checks[i].Before.Reads; ra != rb {
			t.Errorf("%s: reads %d vs %d", c.Name, ra, rb)
		}
	}
	if checked == 0 {
		t.Fatal("no deny checks compared")
	}
}

// shortCircuitRelations returns relation names whose definition reaches an
// intersection or exclusion, directly or through a computed userset or TTU
// (matched by name across types, conservatively). Their concurrent branches
// short-circuit, so their read counts may legitimately vary.
func shortCircuitRelations(m *openfgav1.AuthorizationModel) map[string]bool {
	deps := map[string]map[string]bool{}
	bad := map[string]bool{}
	var walk func(rel string, u *openfgav1.Userset)
	walk = func(rel string, u *openfgav1.Userset) {
		switch v := u.GetUserset().(type) {
		case *openfgav1.Userset_Intersection:
			bad[rel] = true
			for _, c := range v.Intersection.GetChild() {
				walk(rel, c)
			}
		case *openfgav1.Userset_Difference:
			bad[rel] = true
			walk(rel, v.Difference.GetBase())
			walk(rel, v.Difference.GetSubtract())
		case *openfgav1.Userset_Union:
			for _, c := range v.Union.GetChild() {
				walk(rel, c)
			}
		case *openfgav1.Userset_ComputedUserset:
			deps[rel][v.ComputedUserset.GetRelation()] = true
		case *openfgav1.Userset_TupleToUserset:
			deps[rel][v.TupleToUserset.GetComputedUserset().GetRelation()] = true
			deps[rel][v.TupleToUserset.GetTupleset().GetRelation()] = true
		}
	}
	for _, td := range m.GetTypeDefinitions() {
		for rel, u := range td.GetRelations() {
			if deps[rel] == nil {
				deps[rel] = map[string]bool{}
			}
			walk(rel, u)
		}
	}
	for changed := true; changed; {
		changed = false
		for rel, ds := range deps {
			for d := range ds {
				if bad[d] && !bad[rel] {
					bad[rel], changed = true, true
				}
			}
		}
	}
	return bad
}

func TestRun_ReportsMismatch(t *testing.T) {
	load := func(dsl string) *modeltest.LoadedModel {
		lm, err := modeltest.LoadModelBytes([]byte(dsl))
		if err != nil {
			t.Fatal(err)
		}
		return lm
	}
	before := load(`model
  schema 1.1
type user
type document
  relations
    define blocked: [user]
    define viewer: [user]
`)
	after := load(`model
  schema 1.1
type user
type document
  relations
    define blocked: [user]
    define viewer: [user] but not blocked
`)
	src := &Source{
		Kind:   "workspace",
		Tuples: []*openfgav1.TupleKey{tk("user:a", "viewer", "document:1"), tk("user:a", "blocked", "document:1")},
		Checks: []Check{{Name: "x", User: "user:a", Relation: "viewer", Object: "document:1"}},
	}
	res, err := Run(context.Background(), before.Proto, after.Proto, src, Options{Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Mismatches) != 1 || !res.Mismatches[0].AllowedBefore || res.Mismatches[0].AllowedAfter {
		t.Fatalf("Mismatches = %+v, want one true->false", res.Mismatches)
	}
	if len(res.Tuples) != 0 {
		t.Errorf("Tuples populated for non-synthesis source")
	}
}

func TestRun_ProfileOnly(t *testing.T) {
	ref := loadRef(t)
	src, err := Synthesize(ref, SynthOptions{Noise: 1})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), ref, nil, src, Options{Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Checks) == 0 {
		t.Fatal("no checks")
	}
	for _, c := range res.Checks {
		if c.After != nil {
			t.Errorf("%s: After set in profile mode", c.Name)
		}
	}
}
