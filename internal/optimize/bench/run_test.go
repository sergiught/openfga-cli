package bench

import (
	"slices"
	"strings"
	"time"

	"context"
	"google.golang.org/protobuf/proto"
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

func TestRun_ProbesCatchDroppedUnionOperand(t *testing.T) {
	ref := loadRef(t)
	m, err := optimize.NewModel(ref)
	if err != nil {
		t.Fatal(err)
	}
	broken := proto.Clone(m.Proto).(*openfgav1.AuthorizationModel)
	for _, td := range broken.GetTypeDefinitions() {
		if td.GetType() != "document" {
			continue
		}
		// Drop the "viewer from parent" operand (index 3) from viewer's union.
		u := td.GetRelations()["viewer"].GetUnion()
		u.Child = slices.Delete(u.GetChild(), 3, 4)
	}
	src, err := Synthesize(ref, SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), ref, broken, src, Options{Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Probes != len(src.Probes) || res.Probes == 0 {
		t.Fatalf("Probes = %d, want %d", res.Probes, len(src.Probes))
	}
	found := false
	for _, mm := range res.Mismatches {
		if strings.HasPrefix(mm.Name, "probe ") && mm.AllowedBefore && !mm.AllowedAfter {
			found = true
		}
	}
	if !found {
		t.Fatalf("no probe mismatch in %+v", res.Mismatches)
	}
}

func TestRun_ProbesAgreeOnSafeRewrite(t *testing.T) {
	ref := loadRef(t)
	m, err := optimize.NewModel(ref)
	if err != nil {
		t.Fatal(err)
	}
	rw, err := optimize.Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	src, err := Synthesize(ref, SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := Run(context.Background(), ref, rw.After.Proto, src, Options{Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("probes=%d elapsed=%s", res.Probes, time.Since(start))
	if len(res.Mismatches) != 0 {
		t.Fatalf("mismatches: %+v", res.Mismatches)
	}
}

// TestRun_SynthCycleCatchesK1 runs the pre-guard K1 rewrite (folder#y: a and b
// -> a) through synthesis: lowering y's weight switches OpenFGA's resolver for
// folder#viewer and drops cycle detection under the exclusion, so the
// synthesized 2-cycle must expose a changed answer.
func TestRun_SynthCycleCatchesK1(t *testing.T) {
	const k1 = `model
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
    define y: %s
    define viewer: [user] or viewer from parent or y
type doc
  relations
    define parent: [folder]
    define can_read: [user] but not viewer from parent
`
	load := func(y string) *openfgav1.AuthorizationModel {
		lm, err := modeltest.LoadModelBytes([]byte(strings.Replace(k1, "%s", y, 1)))
		if err != nil {
			t.Fatal(err)
		}
		return lm.Proto
	}
	before, after := load("a and b"), load("a")
	src, err := Synthesize(before, SynthOptions{Noise: 1})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), before, after, src, Options{Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, mm := range res.Mismatches {
		if mm.Relation == "can_read" && mm.Object == "doc:t1" && !mm.AllowedBefore && mm.AllowedAfter {
			found = true
		}
	}
	if !found {
		t.Fatalf("no doc:t1 can_read deny->allow mismatch; mismatches=%+v unstable=%+v", res.Mismatches, res.Unstable)
	}
}

// TestSplitUnstable_AfterOnlyFlipIsMismatch feeds splitUnstable recorded
// mismatches for checks both models really answer alike, so re-running after
// contradicts the recorded after answer, as if after had flapped. Such a flip
// on a relation the rewrite changed must stay a mismatch; one on an unchanged
// relation, or one where before itself varies, is unstable.
func TestSplitUnstable_AfterOnlyFlipIsMismatch(t *testing.T) {
	load := func(dsl string) *openfgav1.AuthorizationModel {
		lm, err := modeltest.LoadModelBytes([]byte(dsl))
		if err != nil {
			t.Fatal(err)
		}
		return lm.Proto
	}
	before := load(`model
  schema 1.1
type user
type document
  relations
    define blocked: [user]
    define viewer: [user]
    define can_view: viewer
`)
	after := load(`model
  schema 1.1
type user
type document
  relations
    define blocked: [user]
    define viewer: [user] but not blocked
    define can_view: viewer
`)
	src := &Source{Tuples: []*openfgav1.TupleKey{
		tk("user:a", "blocked", "document:1"),
		tk("user:b", "viewer", "document:1"),
	}}
	mism := func(name, user, rel string, before bool) Mismatch {
		return Mismatch{Check: Check{Name: name, User: user, Relation: rel, Object: "document:1"}, AllowedBefore: before, AllowedAfter: !before}
	}
	in := []Mismatch{
		mism("rewritten", "user:b", "viewer", true),           // only after varies
		mism("reaches rewritten", "user:b", "can_view", true), // likewise, via can_view -> viewer
		mism("unchanged", "user:a", "blocked", true),          // blocked cannot reach viewer
		mism("before varies", "user:b", "viewer", false),      // before really allows
	}
	stable, unstable, err := splitUnstable(context.Background(), before, after, src, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	names := func(ms []Mismatch) (out []string) {
		for _, m := range ms {
			out = append(out, m.Name)
		}
		return out
	}
	if got, want := names(stable), []string{"rewritten", "reaches rewritten"}; !slices.Equal(got, want) {
		t.Errorf("stable = %v, want %v", got, want)
	}
	if got, want := names(unstable), []string{"unchanged", "before varies"}; !slices.Equal(got, want) {
		t.Errorf("unstable = %v, want %v", got, want)
	}
}

func TestChangedRelations_DirectTypesAndDefinitions(t *testing.T) {
	load := func(dsl string) *openfgav1.AuthorizationModel {
		lm, err := modeltest.LoadModelBytes([]byte(dsl))
		if err != nil {
			t.Fatal(err)
		}
		return lm.Proto
	}
	before := load(`model
  schema 1.1
type user
type group
  relations
    define member: [user]
type document
  relations
    define owner: [user]
    define editor: [user]
    define viewer: [user] or editor
`)
	after := load(`model
  schema 1.1
type user
type group
  relations
    define member: [user]
type document
  relations
    define owner: [user, group#member]
    define editor: [user]
    define viewer: [user]
`)
	got := changedRelations(before, after)
	slices.Sort(got)
	if want := []string{"document#owner", "document#viewer"}; !slices.Equal(got, want) {
		t.Fatalf("changed = %v, want %v", got, want)
	}
}
