package optimize

import (
	"maps"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"

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
		if len(rw.Applied) == 0 {
			continue
		}
		withApplied++
		for _, f := range rw.Applied {
			perRule[f.ID]++
		}
		tuples := genTuples(r, m.Proto, 30)
		checks := allChecks(m.Proto)
		before := runChecks(t, eng, rw.Before.Proto, tuples, checks)
		after := runChecks(t, eng, rw.After.Proto, tuples, checks)
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
	if !custom {
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
