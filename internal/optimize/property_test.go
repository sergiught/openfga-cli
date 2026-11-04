package optimize

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

const defaultSeed = 1

// maxPropertyRelations caps the size of a generated model so the per-model
// check grid stays small.
const maxPropertyRelations = 12

// countRelations counts the relations of the random t* types; the fixed-size
// exclusion and cycle shapes genModel appends are not part of the cap.
func countRelations(m *openfgav1.AuthorizationModel) int {
	n := 0
	for _, td := range m.GetTypeDefinitions() {
		if !strings.HasPrefix(td.GetType(), "t") {
			continue
		}
		n += len(td.GetRelations())
	}
	return n
}

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
	r := rand.New(rand.NewPCG(seed, 0))  //nolint:gosec // deterministic test generator
	rc := rand.New(rand.NewPCG(seed, 1)) //nolint:gosec // conditions and wildcards
	t.Logf("seed=%d", seed)

	eng, err := modeltest.NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	const models = 150
	withApplied := 0
	perRule := map[string]int{}
	totalChecks, trueChecks, diffUnion, unstableChecks, blocked, combined, condWild := 0, 0, 0, 0, 0, 0, 0
	for range models {
		var m *Model
		for m == nil {
			if p := genModel(r, rc); p != nil && countRelations(p) <= maxPropertyRelations {
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
		if hasConditionOrWildcard(m) {
			condWild++
			if w, err := Weights(m); err == nil && !sameWeights(w, rw.After) {
				t.Fatalf("seed=%d weights changed in a model with conditions or wildcards\nmodel:\n%s\napplied=%+v", seed, dsl, rw.Applied)
			}
		}
		tuples := genTuples(r, rc, m.Proto, 30)
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
			if v == allow {
				trueChecks++
			}
		}
		after := runChecks(t, eng, rw.After.Proto, tuples, checks)
		if !maps.Equal(before, after) && onlyUnaffectedDiffer(rw, before, after) {
			// Checks on relations that cannot reach a rewritten relation
			// evaluate identical trees in both models, so a difference there
			// is engine nondeterminism.
			unstableChecks += countDiffering(before, after)
			continue
		}
		if !maps.Equal(before, after) {
			// The engine's own answer can vary between runs of the SAME
			// model (seeds 3 and 5). Drop the checks whose original answer
			// varies, and after-only variance off the rewritten paths; the
			// rest must still agree.
			unstableChecks += dropUnstable(rw, before, after,
				engineUnstable(t, eng, rw.Before.Proto, tuples, checks, before),
				engineUnstable(t, eng, rw.After.Proto, tuples, checks, after))
		}
		if !maps.Equal(before, after) {
			var diff []string
			for k, v := range before {
				if after[k] != v {
					diff = append(diff, fmt.Sprintf("%+v before=%s after=%s", k, v, after[k]))
				}
			}
			slices.Sort(diff)
			t.Fatalf("seed=%d answers differ\nmodel:\n%s\napplied=%+v\ntuples=%v\ndiffering=%v", seed, dsl, rw.Applied, tuples, diff)
		}
	}
	t.Logf("models with applied findings: %d/%d; per rule: %v", withApplied, models, perRule)
	t.Logf("skipped engine-unstable checks: %d", unstableChecks)
	t.Logf("models blocked by a recursive exclusion: %d (%d with an AND on a tuple cycle elsewhere)", blocked, combined)
	t.Logf("findings on difference-with-union-base relations: %d", diffUnion)
	t.Logf("models with applied findings and conditions or wildcards: %d", condWild)
	if !custom && diffUnion == 0 {
		t.Error("no finding landed on a union base under exclusion")
	}
	ratio := float64(trueChecks) / float64(max(totalChecks, 1))
	t.Logf("true checks: %d/%d (%.1f%%)", trueChecks, totalChecks, ratio*100)
	if !custom {
		if ratio < 0.15 {
			t.Errorf("true-answer ratio %.1f%% below 15%%: test would be weak", ratio*100)
		}
		// The engine's nondeterminism makes this count vary between runs of
		// the same seed (0 to 4 observed on seed 1 over 7 runs, out of ~29k
		// checks). The cap replaces an earlier cap of 3 whole models, each
		// carrying ~400 checks, so it hides far less than before.
		if unstableChecks > 10 {
			t.Errorf("%d engine-unstable checks skipped; the skip policy is hiding too much", unstableChecks)
		}
		if combined == 0 {
			t.Error("no model combined a recursive exclusion with an AND on a tuple cycle")
		}
		if condWild == 0 {
			t.Error("no rewritten model had conditions or wildcards")
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
func onlyUnaffectedDiffer(rw *RewriteResult, before, after map[checkKey]answer) bool {
	affected := affectedBy(rw)
	for k, v := range before {
		if after[k] != v && affected(k) {
			return false
		}
	}
	return true
}

// affectedBy returns a predicate reporting whether a check's relation can
// reach a relation rewritten in rw.
func affectedBy(rw *RewriteResult) func(checkKey) bool {
	graph := dependencyGraph(rw.Before)
	var rewritten []string
	for _, f := range rw.Applied {
		rewritten = append(rewritten, f.Type+"#"+f.Relation)
	}
	return func(k checkKey) bool {
		typ, _, _ := strings.Cut(k.object, ":")
		reach := reachable(graph, []string{typ + "#" + k.relation})
		for _, r := range rewritten {
			if reach[r] {
				return true
			}
		}
		return false
	}
}

// engineUnstable returns the checks whose answer changes in any of 8 reruns
// of model m against ref. It never short-circuits, so every unstable key is
// found.
func engineUnstable(t *testing.T, eng modeltest.Engine, m *openfgav1.AuthorizationModel, tuples []*openfgav1.TupleKey, checks []checkKey, ref map[checkKey]answer) map[checkKey]bool {
	t.Helper()
	out := map[checkKey]bool{}
	for range 8 {
		for k, v := range runChecks(t, eng, m, tuples, checks) {
			if ref[k] != v {
				out[k] = true
			}
		}
	}
	return out
}

// countDiffering returns how many keys answer differently in before and after.
func countDiffering(before, after map[checkKey]answer) int {
	n := 0
	for k, v := range before {
		if after[k] != v {
			n++
		}
	}
	return n
}

// dropUnstable deletes from both maps the checks whose answer is engine noise
// and returns how many it deleted: every check that varies when the original
// model is re-run, and a check that varies only on the rewritten model when
// its relation cannot reach a rewritten one. A check that varies only after
// the rewrite on a rewritten path is kept, since the rewrite may be what made
// it unstable.
func dropUnstable(rw *RewriteResult, before, after map[checkKey]answer, beforeUnstable, afterUnstable map[checkKey]bool) int {
	affected := affectedBy(rw)
	dropped := map[checkKey]bool{}
	maps.Copy(dropped, beforeUnstable)
	for k := range afterUnstable {
		if !affected(k) {
			dropped[k] = true
		}
	}
	for k := range dropped {
		delete(before, k)
		delete(after, k)
	}
	return len(dropped)
}

func TestProperty_RewriteSubsetPreservesCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("property test is slow")
	}
	eng, err := modeltest.NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	withSubset := 0
	for seed := uint64(1); seed <= 40; seed++ {
		r := rand.New(rand.NewPCG(seed, 0))  //nolint:gosec // deterministic test generator
		rc := rand.New(rand.NewPCG(seed, 1)) //nolint:gosec // conditions and wildcards
		var m *Model
		for m == nil {
			if p := genModel(r, rc); p != nil {
				m, _ = NewModel(p)
			}
		}
		full, err := Rewrite(m)
		if err != nil {
			t.Fatal(err)
		}
		pick := rand.New(rand.NewPCG(seed, 2)) //nolint:gosec // deterministic test generator
		accept := map[FindingKey]bool{}
		for _, k := range Keys(full.Applied) {
			if pick.IntN(2) == 0 {
				accept[k] = true
			}
		}
		rw, err := RewriteSubset(m, accept)
		dsl, _ := m.DSL()
		if err != nil {
			t.Fatalf("seed=%d rewrite subset: %v\nmodel:\n%s", seed, err, dsl)
		}
		for _, k := range Keys(rw.Applied) {
			if !accept[k] {
				t.Fatalf("seed=%d applied unaccepted %v", seed, k)
			}
		}
		if len(rw.Applied) == 0 {
			continue
		}
		withSubset++
		tuples := genTuples(r, rc, m.Proto, 30)
		checks := allChecks(m.Proto)
		before := runChecks(t, eng, rw.Before.Proto, tuples, checks)
		after := runChecks(t, eng, rw.After.Proto, tuples, checks)
		if maps.Equal(before, after) || onlyUnaffectedDiffer(rw, before, after) {
			continue
		}
		dropUnstable(rw, before, after,
			engineUnstable(t, eng, rw.Before.Proto, tuples, checks, before),
			engineUnstable(t, eng, rw.After.Proto, tuples, checks, after))
		if maps.Equal(before, after) {
			continue
		}
		t.Fatalf("seed=%d subset answers differ\nmodel:\n%s\napplied=%+v\ntuples=%v", seed, dsl, rw.Applied, tuples)
	}
	t.Logf("seeds with a non-empty subset: %d/40", withSubset)
	if withSubset < 5 {
		t.Errorf("vacuous: only %d/40 seeds applied a subset", withSubset)
	}
}
