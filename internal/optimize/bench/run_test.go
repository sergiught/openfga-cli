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
	// Deny checks through concurrent intersection or recursive usersets
	// (folder#can_delete, group#member, ...) vary by a read or two between
	// runs; the deny walks below are deterministic.
	for _, name := range []string{"deny document#can_view", "deny document#can_edit", "deny document#viewer"} {
		ra, rb := find(t, a.Checks, name).Before.Reads, find(t, b.Checks, name).Before.Reads
		if ra != rb {
			t.Errorf("%s: reads %d vs %d", name, ra, rb)
		}
	}
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
	for _, c := range res.Checks {
		if c.After != nil {
			t.Errorf("%s: After set in profile mode", c.Name)
		}
	}
}
