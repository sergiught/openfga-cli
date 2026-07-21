package modeltest

import (
	"context"
	"fmt"
)

// evaluateAssertions runs every check/list_objects/list_users assertion in
// tt against scope, in declaration order. Since the YAML assertions maps
// have no inherent order, relation keys within a single case are sorted so
// results are deterministic across runs. It also attaches a resolution
// explanation (Options.Explain) to each assertion that failed, or to every
// assertion when opts.Explain == "full".
func evaluateAssertions(ctx context.Context, lm *LoadedModel, opts Options, scope Scope, tt Test) ([]AssertionResult, error) {
	eng := opts.Engine
	var out []AssertionResult
	traceCache := newTraceCache()

	for _, cc := range tt.Check {
		ctxTuples, err := toProtoTuples(cc.ContextualTuples)
		if err != nil {
			return nil, err
		}

		users, objects, err := cc.subjects()
		if err != nil {
			return nil, err
		}

		// A grouped check shares one assertions block across every
		// user × object × relation combination.
		for _, user := range users {
			for _, object := range objects {
				for _, relation := range sortedKeys(cc.Assertions) {
					expected := cc.Assertions[relation]
					req := CheckReq{
						User:             user,
						Relation:         relation,
						Object:           object,
						Context:          cc.Context,
						ContextualTuples: ctxTuples,
					}
					got, err := eng.Check(ctx, scope, req)
					if err != nil {
						return nil, fmt.Errorf("check %s %s %s: %w", user, relation, object, err)
					}

					ar := AssertionResult{
						Kind:     kindCheck,
						Subject:  fmt.Sprintf("check %s %s %s", user, relation, object),
						Expected: expected,
						Got:      got,
						Passed:   got == expected,
					}
					if shouldExplain(opts, ar.Passed) {
						exp, traceErr := explainCheck(ctx, lm, eng, scope, req, got, ar.Passed, traceCache)
						ar.Explain = exp
						if traceErr != nil && opts.Coverage {
							ar.coverageErr = traceErr
						}
					}
					out = append(out, ar)
				}
			}
		}
	}

	for _, lc := range tt.ListObjects {
		for _, relation := range sortedKeys(lc.Assertions) {
			expected := sortedSet(lc.Assertions[relation])
			got, err := eng.ListObjects(ctx, scope, ListObjectsReq{
				User:     lc.User,
				Relation: relation,
				Type:     lc.Type,
				Context:  lc.Context,
			})
			if err != nil {
				return nil, fmt.Errorf("list_objects %s %s %s: %w", lc.User, relation, lc.Type, err)
			}
			gotSorted := sortedSet(got)

			ar := AssertionResult{
				Kind:     kindListObjects,
				Subject:  fmt.Sprintf("list_objects %s %s %s", lc.User, relation, lc.Type),
				Expected: expected,
				Got:      gotSorted,
				Passed:   equalSets(expected, gotSorted),
				covKey:   lc.Type + "#" + relation,
			}
			if shouldExplain(opts, ar.Passed) {
				ar.Explain = &Explain{SetDiff: setDiff(expected, gotSorted)}
			}
			out = append(out, ar)
		}
	}

	for _, lc := range tt.ListUsers {
		for _, relation := range sortedKeys(lc.Assertions) {
			expected := sortedSet(lc.Assertions[relation].Users)
			got, err := eng.ListUsers(ctx, scope, ListUsersReq{
				Object:   lc.Object,
				Relation: relation,
				Filters:  lc.UserFilter,
				Context:  lc.Context,
			})
			if err != nil {
				return nil, fmt.Errorf("list_users %s %s: %w", lc.Object, relation, err)
			}
			gotSorted := sortedSet(got)

			ar := AssertionResult{
				Kind:     kindListUsers,
				Subject:  fmt.Sprintf("list_users %s %s", lc.Object, relation),
				Expected: expected,
				Got:      gotSorted,
				Passed:   equalSets(expected, gotSorted),
				covKey:   idType(lc.Object) + "#" + relation,
			}
			if shouldExplain(opts, ar.Passed) {
				ar.Explain = &Explain{SetDiff: setDiff(expected, gotSorted)}
			}
			out = append(out, ar)
		}
	}

	return out, nil
}

// shouldExplain reports whether an assertion should get an explanation
// attached: always on failure, on every assertion (pass or fail) when
// opts.Explain == "full", and likewise when opts.Coverage is on — the
// resolution tree it produces is what coverage traces against, so even a
// passing check needs one.
func shouldExplain(opts Options, passed bool) bool {
	return !passed || opts.Explain == "full" || opts.Coverage
}

// explainCheck builds the explanation for a check assertion: the resolution
// tree via trace, plus — only when the assertion actually failed (a denial
// that was expected to pass) — a best-effort nearest-miss suggestion. A
// passing expected-deny (got == false, passed == true) skips nearestMiss
// entirely: probing for a tuple that would grant something the test intends
// to deny is both wasted work and a confusing hint on a passing assertion.
// Trace errors are returned separately so coverage can refuse to publish a
// misleading partial result; callers that only requested human explanation
// may still treat them as best-effort.
func explainCheck(ctx context.Context, lm *LoadedModel, eng Resolver, scope Scope, req CheckReq, got, passed bool, cache *traceCache) (*Explain, error) {
	exp, err := traceWithVerdict(ctx, lm, eng, scope, req, got, cache)
	if err != nil {
		return nil, err
	}
	if !got && !passed {
		if miss, missErr := nearestMiss(ctx, lm, eng, scope, req); missErr == nil {
			exp.NearestMiss = miss
		}
	}
	return exp, nil
}
