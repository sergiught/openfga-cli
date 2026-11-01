package bench

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/proto"

	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
)

// ErrSetup marks failures creating the embedded server or writing the model
// and tuples to it (e.g. tuples that do not fit the model); match with errors.Is.
var ErrSetup = errors.New("benchmark setup failed")

// Options configures Run.
type Options struct {
	Iterations int            // timed checks per check; default 100
	ServerOpts map[string]any // embedded server options
}

type probe struct {
	allowed bool
	m       Metrics
}

// Run benchmarks before (and after, when non-nil) over src. Checks run in
// source order against sequential servers: one for before, then a fresh one
// for after. Deny and allow are classified by the before answer.
func Run(ctx context.Context, before, after *openfgav1.AuthorizationModel, src *Source, o Options) (*Result, error) {
	iters := o.Iterations
	if iters <= 0 {
		iters = 100
	}
	withProbes := after != nil
	b, bAns, err := probeModel(ctx, before, src, iters, o.ServerOpts, withProbes)
	if err != nil {
		return nil, fmt.Errorf("before: %w", err)
	}
	var (
		a    []probe
		aAns []bool
	)
	if after != nil {
		if a, aAns, err = probeModel(ctx, after, src, iters, o.ServerOpts, withProbes); err != nil {
			return nil, fmt.Errorf("after: %w", err)
		}
	}

	res := &Result{
		Source:     src.Kind,
		Seed:       src.Seed,
		TupleCount: len(src.Tuples),
		Skipped:    src.Skipped,
		Checks:     make([]CheckResult, 0, len(src.Checks)),
		Mismatches: []Mismatch{},
		Unstable:   []Mismatch{},
	}
	if src.Kind == "synthesis" {
		for _, t := range src.Tuples {
			res.Tuples = append(res.Tuples, Tuple{User: t.GetUser(), Relation: t.GetRelation(), Object: t.GetObject()})
		}
	}
	for i, c := range src.Checks {
		cr := CheckResult{Check: c, Allowed: b[i].allowed, Before: b[i].m}
		if a != nil {
			m := a[i].m
			cr.After = &m
			if a[i].allowed != b[i].allowed {
				res.Mismatches = append(res.Mismatches, Mismatch{Check: c, AllowedBefore: b[i].allowed, AllowedAfter: a[i].allowed})
			}
		}
		t := &res.Totals
		switch {
		case b[i].allowed:
			t.AllowReadsBefore += b[i].m.Reads
			if a != nil {
				t.AllowReadsAfter += a[i].m.Reads
			}
		default:
			t.DenyReadsBefore += b[i].m.Reads
			t.DenyDispatchesBefore += b[i].m.Dispatches
			if a != nil {
				t.DenyReadsAfter += a[i].m.Reads
				t.DenyDispatchesAfter += a[i].m.Dispatches
			}
		}
		res.Checks = append(res.Checks, cr)
	}
	if after != nil {
		res.Probes = len(src.Probes)
		for i, c := range src.Probes {
			if bAns[i] != aAns[i] {
				res.Mismatches = append(res.Mismatches, Mismatch{Check: c, AllowedBefore: bAns[i], AllowedAfter: aAns[i]})
			}
		}
		if res.Mismatches, res.Unstable, err = splitUnstable(ctx, before, after, src, o.ServerOpts, res.Mismatches); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// reruns is how many times splitUnstable re-checks a mismatch on the before
// model.
const reruns = 5

// splitUnstable moves engine nondeterminism to unstable: a mismatch whose
// answer varies when the before model is re-run (a cycle under an exclusion
// can make OpenFGA answer differently run to run), or whose relation cannot
// reach any relation the rewrite changed, so both models evaluate the same
// tree. A mismatch where only the after model varies on a rewritten path is
// kept: the rewrite may be what made the answer unstable.
func splitUnstable(ctx context.Context, before, after *openfgav1.AuthorizationModel, src *Source, serverOpts map[string]any, mism []Mismatch) (stable, unstable []Mismatch, err error) {
	unstable = []Mismatch{}
	if len(mism) == 0 {
		return mism, unstable, nil
	}
	// A relation that reached a changed one only in before reaches, in
	// after, the first changed relation on that path, so after's graph is
	// enough.
	affected := optimize.Dependents(&optimize.Model{Proto: after}, changedRelations(before, after))
	bVar, err := varies(ctx, before, src, serverOpts, mism, func(m Mismatch) bool { return m.AllowedBefore })
	if err != nil {
		return nil, nil, fmt.Errorf("before: %w", err)
	}
	stable = []Mismatch{}
	for i, m := range mism {
		typ, _, _ := strings.Cut(m.Object, ":")
		if bVar[i] || !affected[typ+"#"+m.Relation] {
			unstable = append(unstable, m)
		} else {
			stable = append(stable, m)
		}
	}
	return stable, unstable, nil
}

// changedRelations lists the relations ("type#rel") whose definition or
// directly related types differ between before and after, including any that
// exist in only one of them.
func changedRelations(before, after *openfgav1.AuthorizationModel) []string {
	type def struct {
		us     *openfgav1.Userset
		direct *openfgav1.RelationMetadata
	}
	defs := func(m *openfgav1.AuthorizationModel) map[string]def {
		out := map[string]def{}
		for _, td := range m.GetTypeDefinitions() {
			for rel, us := range td.GetRelations() {
				out[td.GetType()+"#"+rel] = def{us, td.GetMetadata().GetRelations()[rel]}
			}
		}
		return out
	}
	b, a := defs(before), defs(after)
	var changed []string
	for k, ad := range a {
		bd, ok := b[k]
		if !ok || !proto.Equal(bd.us, ad.us) ||
			!slices.EqualFunc(bd.direct.GetDirectlyRelatedUserTypes(), ad.direct.GetDirectlyRelatedUserTypes(),
				func(x, y *openfgav1.RelationReference) bool { return proto.Equal(x, y) }) {
			changed = append(changed, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			changed = append(changed, k)
		}
	}
	return changed
}

// varies reports, per mismatch, whether any of reruns checks on model answers
// other than want.
func varies(ctx context.Context, model *openfgav1.AuthorizationModel, src *Source, serverOpts map[string]any, mism []Mismatch, want func(Mismatch) bool) ([]bool, error) {
	eng, err := modeltest.NewEmbeddedEngine(serverOpts)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSetup, err)
	}
	defer func() { _ = eng.Close() }()
	storeID, modelID, err := eng.Setup(ctx, model, src.Tuples)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSetup, err)
	}
	scope := modeltest.Scope{StoreID: storeID, ModelID: modelID}
	out := make([]bool, len(mism))
	for i, m := range mism {
		for range reruns {
			allowed, err := eng.Check(ctx, scope, modeltest.CheckReq{User: m.User, Relation: m.Relation, Object: m.Object})
			if err != nil {
				return nil, fmt.Errorf("%s: %w", m.Name, err)
			}
			if allowed != want(m) {
				out[i] = true
				break
			}
		}
	}
	return out, nil
}

func probeModel(ctx context.Context, model *openfgav1.AuthorizationModel, src *Source, iters int, serverOpts map[string]any, withProbes bool) ([]probe, []bool, error) {
	eng, counters, err := modeltest.NewCountingEmbeddedEngine(serverOpts)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrSetup, err)
	}
	defer func() { _ = eng.Close() }()

	storeID, modelID, err := eng.Setup(ctx, model, src.Tuples)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrSetup, err)
	}
	scope := modeltest.Scope{StoreID: storeID, ModelID: modelID}

	out := make([]probe, 0, len(src.Checks))
	for _, c := range src.Checks {
		req := modeltest.CheckReq{User: c.User, Relation: c.Relation, Object: c.Object}

		settle(counters)
		counters.Reset()
		tagCtx, dispatches := modeltest.WithDispatchTags(ctx)
		allowed, err := eng.Check(tagCtx, scope, req)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", c.Name, err)
		}
		settle(counters)
		p := probe{allowed: allowed, m: Metrics{Reads: counters.Reads(), Dispatches: dispatches()}}

		durs := make([]float64, 0, iters)
		for range iters {
			start := time.Now()
			if _, err := eng.Check(ctx, scope, req); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", c.Name, err)
			}
			durs = append(durs, float64(time.Since(start))/float64(time.Millisecond))
		}
		slices.Sort(durs)
		p.m.P50Ms = percentile(durs, 50)
		p.m.P95Ms = percentile(durs, 95)
		out = append(out, p)
	}
	var answers []bool
	if withProbes {
		answers = make([]bool, 0, len(src.Probes))
		for _, c := range src.Probes {
			allowed, err := eng.Check(ctx, scope, modeltest.CheckReq{User: c.User, Relation: c.Relation, Object: c.Object})
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", c.Name, err)
			}
			answers = append(answers, allowed)
		}
	}
	return out, answers, nil
}

// percentile returns the nearest-rank percentile of sorted values.
func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (len(sorted)*p + 99) / 100
	return sorted[max(idx, 1)-1]
}

// settle waits for reads from goroutines a finished Check abandoned (short-
// circuited branches) so they don't bleed into the next counting window.
func settle(c *modeltest.Counters) {
	const quiet, limit = 2 * time.Millisecond, 50 * time.Millisecond
	deadline := time.Now().Add(limit)
	last, since := c.Reads(), time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Microsecond)
		if cur := c.Reads(); cur != last {
			last, since = cur, time.Now()
		} else if time.Since(since) >= quiet {
			return
		}
	}
}
