package optimize

import (
	"maps"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

const defaultSeed = 1

func TestProperty_RewritePreservesCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("property test is slow")
	}
	seed := uint64(defaultSeed)
	custom := false
	if s := os.Getenv("OPTIMIZE_SEED"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			t.Fatalf("bad OPTIMIZE_SEED: %v", err)
		}
		seed, custom = v, true
	}
	r := rand.New(rand.NewPCG(seed, 0)) //nolint:gosec // deterministic test generator
	t.Logf("seed=%d", seed)

	eng, err := modeltest.NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	const models = 150
	withApplied := 0
	perRule := map[string]int{}
	totalChecks, trueChecks, diffUnion, unstable, blocked, combined := 0, 0, 0, 0, 0, 0
	for range models {
		var m *Model
		for m == nil {
			if p := genModel(r); p != nil {
				m, _ = NewModel(p)
			}
		}
		rw, err := Rewrite(m)
		dsl, _ := m.DSL()
		if err != nil {
			t.Fatalf("seed=%d rewrite: %v\nmodel:\n%s", seed, err, dsl)
		}
		if rw.Blocked != "" {
			blocked++
		}
		// A recursive exclusion plus an AND on a tuple cycle elsewhere: the
		// model-wide gate must stop every rewrite.
		if _, ok := m.Relation("kd", "can_read"); ok {
			if _, ok := m.Relation("kt", "both"); ok {
				combined++
				if len(rw.Applied) != 0 || rw.Blocked == "" {
					t.Fatalf("seed=%d gate open on recursive exclusion: blocked=%q applied=%+v\nmodel:\n%s", seed, rw.Blocked, rw.Applied, dsl)
				}
			}
		}
		if len(rw.Applied) == 0 {
			continue
		}
		tuples := genTuples(r, m.Proto, 30)
		checks := allChecks(m.Proto)
		before := runChecks(t, eng, rw.Before.Proto, tuples, checks)
		withApplied++
		for _, f := range rw.Applied {
			perRule[f.ID]++
			// first finding per relation is applied on the original def
			if us, ok := rw.Before.Relation(f.Type, f.Relation); ok && us.GetDifference().GetBase().GetUnion() != nil {
				diffUnion++
			}
		}
		for _, v := range before {
			totalChecks++
			if v {
				trueChecks++
			}
		}
		after := runChecks(t, eng, rw.After.Proto, tuples, checks)
		if !maps.Equal(before, after) && onlyUnaffectedDiffer(rw, before, after) {
			// Checks on relations that cannot reach a rewritten relation
			// evaluate identical trees in both models, so a difference there
			// is engine nondeterminism.
			unstable++
			continue
		}
		if !maps.Equal(before, after) && engineUnstable(t, eng, rw.Before.Proto, tuples, checks, before) {
			// A cycle through exclusion makes the engine's own answer vary
			// between runs of the SAME model (seeds 3 and 5), so there is
			// nothing for a rewrite to preserve.
			unstable++
			continue
		}
		if !maps.Equal(before, after) {
			var diff []checkKey
			for k, v := range before {
				if after[k] != v {
					diff = append(diff, k)
				}
			}
			t.Fatalf("seed=%d answers differ\nmodel:\n%s\napplied=%+v\ntuples=%v\ndiffering=%v", seed, dsl, rw.Applied, tuples, diff)
		}
	}
	t.Logf("models with applied findings: %d/%d; per rule: %v", withApplied, models, perRule)
	t.Logf("skipped engine-unstable models: %d", unstable)
	t.Logf("models blocked by a recursive exclusion: %d (%d with an AND on a tuple cycle elsewhere)", blocked, combined)
	t.Logf("findings on difference-with-union-base relations: %d", diffUnion)
	if !custom && diffUnion == 0 {
		t.Error("no finding landed on a union base under exclusion")
	}
	ratio := float64(trueChecks) / float64(max(totalChecks, 1))
	t.Logf("true checks: %d/%d (%.1f%%)", trueChecks, totalChecks, ratio*100)
	if !custom {
		if ratio < 0.15 {
			t.Errorf("true-answer ratio %.1f%% below 15%%: test would be weak", ratio*100)
		}
		if unstable > 3 {
			t.Errorf("%d engine-unstable models skipped; the skip policy is hiding too much", unstable)
		}
		if combined == 0 {
			t.Error("no model combined a recursive exclusion with an AND on a tuple cycle")
		}
		if withApplied < 20 {
			t.Errorf("vacuous: only %d/%d models had applied findings", withApplied, models)
		}
		for _, id := range []string{"S1", "S2", "S4"} {
			if perRule[id] == 0 {
				t.Errorf("rule %s never fired", id)
			}
		}
	}
}

// onlyUnaffectedDiffer reports whether every differing check is on a relation
// that cannot reach any relation rewritten in rw.
func onlyUnaffectedDiffer(rw *RewriteResult, before, after map[checkKey]bool) bool {
	graph := dependencyGraph(rw.Before)
	var rewritten []string
	for _, f := range rw.Applied {
		rewritten = append(rewritten, f.Type+"#"+f.Relation)
	}
	for k, v := range before {
		if after[k] == v {
			continue
		}
		typ, _, _ := strings.Cut(k.object, ":")
		reach := reachable(graph, []string{typ + "#" + k.relation})
		for _, r := range rewritten {
			if reach[r] {
				return false
			}
		}
	}
	return true
}

// engineUnstable reports whether re-running the original model ever yields
// answers other than ref.
func engineUnstable(t *testing.T, eng modeltest.Engine, m *openfgav1.AuthorizationModel, tuples []*openfgav1.TupleKey, checks []checkKey, ref map[checkKey]bool) bool {
	t.Helper()
	for range 8 {
		if !maps.Equal(ref, runChecks(t, eng, m, tuples, checks)) {
			return true
		}
	}
	return false
}
