package bench

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/optimize"
)

// SynthOptions tunes Synthesize. Zero values mean the defaults: seed 1, depth
// 6, 3 objects per type, 100 noise objects per type.
type SynthOptions struct {
	Seed           uint64
	Depth          int
	ObjectsPerType int
	Noise          int
}

// leaf is a synthesizable direct tuple template on a relation.
type leaf struct {
	relation string
	// user returns the tuple user for the n-th object (or noise object).
	user func(n int, noise bool) string
	// isUser marks leaves whose user is a synthesized user type object.
	isUser   bool
	userType string
}

// Synthesize builds a deterministic tuple set and checks from m.
func Synthesize(m *openfgav1.AuthorizationModel, o SynthOptions) (*Source, error) {
	if o.Seed == 0 {
		o.Seed = 1
	}
	if o.Depth <= 0 {
		o.Depth = 6
	}
	if o.ObjectsPerType <= 0 {
		o.ObjectsPerType = 3
	}
	if o.Noise <= 0 {
		o.Noise = 100
	}
	model, err := optimize.NewModel(m)
	if err != nil {
		return nil, err
	}
	weights, err := optimize.Weights(model)
	if err != nil {
		return nil, err
	}

	src := &Source{Kind: "synthesis", Seed: o.Seed}
	probeUsers := map[string]bool{}
	rng := rand.New(rand.NewPCG(o.Seed, o.Seed)) //nolint:gosec // deterministic synthesis, not security
	seen := map[string]bool{}
	add := func(user, relation, object string) {
		k := object + "#" + relation + "@" + user
		if seen[k] {
			return
		}
		seen[k] = true
		src.Tuples = append(src.Tuples, &openfgav1.TupleKey{User: user, Relation: relation, Object: object})
	}

	tds := slices.Clone(m.GetTypeDefinitions())
	sort.Slice(tds, func(i, j int) bool { return tds[i].GetType() < tds[j].GetType() })

	for _, td := range tds {
		typ := td.GetType()
		rels := td.GetMetadata().GetRelations()
		names := make([]string, 0, len(rels))
		for r := range rels {
			names = append(names, r)
		}
		sort.Strings(names)

		var leaves []leaf
		for _, rel := range names {
			chainObj, chainSet := false, false
			restrictions := rels[rel].GetDirectlyRelatedUserTypes()
			for _, rr := range restrictions {
				ot, orel := rr.GetType(), rr.GetRelation()
				label := fmt.Sprintf("%s#%s", typ, rel)
				switch {
				case rr.GetWildcard() != nil:
					src.Skipped = append(src.Skipped, fmt.Sprintf("%s: wildcard restriction %s:*", label, ot))
				case rr.GetCondition() != "" && !hasUnconditioned(restrictions, ot, orel):
					src.Skipped = append(src.Skipped, fmt.Sprintf("%s: conditioned-only restriction %s (condition %s)", label, restrictionName(ot, orel), rr.GetCondition()))
				case rr.GetCondition() != "":
					// the unconditioned variant covers it
				case orel == "" && ot == typ:
					chainObj = true
				case orel == rel && ot == typ:
					chainSet = true
				case orel == "" && isUserType(m, ot):
					leaves = append(leaves, leaf{relation: rel, isUser: true, userType: ot, user: func(n int, noise bool) string {
						if noise {
							return fmt.Sprintf("%s:noise%d", ot, n)
						}
						return fmt.Sprintf("%s:%s_%s_%d", ot, typ, rel, n)
					}})
				case orel == "":
					leaves = append(leaves, leaf{relation: rel, user: func(int, bool) string { return ot + ":t1" }})
				default:
					leaves = append(leaves, leaf{relation: rel, user: func(int, bool) string { return fmt.Sprintf("%s:t1#%s", ot, orel) }})
				}
			}
			if chainObj {
				for i := 1; i < o.Depth; i++ {
					add(fmt.Sprintf("%s:chain%d", typ, i+1), rel, fmt.Sprintf("%s:chain%d", typ, i))
				}
				add(typ+":chain1", rel, typ+":t1")
				// A 2-cycle under t1 so checks and probes exercise OpenFGA's
				// cycle detection, which the optimizer's guard depends on.
				add(typ+":cyc2", rel, typ+":cyc1")
				add(typ+":cyc1", rel, typ+":cyc2")
				add(typ+":cyc1", rel, typ+":t1")
			}
			if chainSet {
				for i := 1; i <= 2; i++ {
					add(fmt.Sprintf("%s:chain%d#%s", typ, i+1, rel), rel, fmt.Sprintf("%s:chain%d", typ, i))
				}
				add(fmt.Sprintf("%s:chain1#%s", typ, rel), rel, typ+":t1")
			}
		}

		for _, l := range leaves {
			if l.isUser {
				for n := 1; n <= 2; n++ {
					probeUsers[l.user(n, false)] = true
				}
			}
			for n := 1; n <= o.ObjectsPerType; n++ {
				add(l.user(n, false), l.relation, fmt.Sprintf("%s:t%d", typ, n))
			}
		}
		if len(leaves) > 0 {
			for n := 1; n <= o.Noise; n++ {
				l := leaves[rng.IntN(len(leaves))]
				add(l.user(n, true), l.relation, fmt.Sprintf("%s:noise%d", typ, n))
			}
		}

		// Checks: one deny per relation a user can reach, one allow
		// candidate per direct user leaf.
		for _, rel := range names {
			var subjects []string
			for st := range weights[typ+"#"+rel] {
				if isUserType(m, st) {
					subjects = append(subjects, st)
				}
			}
			sort.Strings(subjects)
			for _, st := range subjects {
				name := fmt.Sprintf("deny %s#%s", typ, rel)
				if len(subjects) > 1 {
					name += " (" + st + ")"
				}
				src.Checks = append(src.Checks, Check{Name: name, User: st + ":nobody", Relation: rel, Object: typ + ":t1"})
			}
			// Userset subjects (T:t1#other) probe rewrites that only hold for
			// concrete users.
			if len(names) >= 2 {
				for _, other := range names {
					if other == rel {
						continue
					}
					src.Checks = append(src.Checks, Check{
						Name:     fmt.Sprintf("probe %s#%s (%s#%s)", typ, rel, typ, other),
						User:     fmt.Sprintf("%s:t1#%s", typ, other),
						Relation: rel,
						Object:   typ + ":t1",
					})
				}
			}
			var userLeaves []leaf
			for _, l := range leaves {
				if l.relation == rel && l.isUser {
					userLeaves = append(userLeaves, l)
				}
			}
			for _, l := range userLeaves {
				name := fmt.Sprintf("allow %s#%s", typ, rel)
				if len(userLeaves) > 1 {
					name += " (" + l.userType + ")"
				}
				src.Checks = append(src.Checks, Check{Name: name, User: l.user(1, false), Relation: rel, Object: typ + ":t1"})
			}
		}
	}

	// Probes: every synthesized leaf user against every relation, so rewritten
	// indirect paths are compared too (answers only, untimed).
	users := make([]string, 0, len(probeUsers))
	for u := range probeUsers {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, u := range users {
		for _, td := range tds {
			names := make([]string, 0, len(td.GetMetadata().GetRelations()))
			for r := range td.GetMetadata().GetRelations() {
				names = append(names, r)
			}
			sort.Strings(names)
			for _, r := range names {
				src.Probes = append(src.Probes, Check{
					Name: fmt.Sprintf("probe %s %s %s:t1", u, r, td.GetType()), User: u, Relation: r, Object: td.GetType() + ":t1",
				})
			}
		}
	}
	return src, nil
}

func hasUnconditioned(rs []*openfgav1.RelationReference, typ, rel string) bool {
	for _, r := range rs {
		if r.GetType() == typ && r.GetRelation() == rel && r.GetWildcard() == nil && r.GetCondition() == "" {
			return true
		}
	}
	return false
}

func restrictionName(typ, rel string) string {
	if rel == "" {
		return typ
	}
	return typ + "#" + rel
}

// isUserType reports whether typ defines no relations (a leaf subject type).
func isUserType(m *openfgav1.AuthorizationModel, typ string) bool {
	for _, td := range m.GetTypeDefinitions() {
		if td.GetType() == typ {
			return len(td.GetRelations()) == 0
		}
	}
	return false
}
