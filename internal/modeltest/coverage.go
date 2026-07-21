package modeltest

import (
	"fmt"
	"sort"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// branchDef is one enumerable coverage unit within a relation's rewrite rule: a
// directly-assignable type, a wildcard, a computed/ttu edge, a subtracted
// exclusion arm, or a condition outcome. Kind classifies the branch; Label is
// a human-readable description unique within (Type, Relation); ID is unique
// across the whole model.
type branchDef struct {
	Type     string // object type
	Relation string
	Kind     string // "direct" | "wildcard" | "computed" | "ttu" | "difference-subtract" | "condition"
	Label    string // human, unique within (Type,Relation), e.g. "direct:user", "computed:owner", "ttu:parent/viewer", "but-not:banned", "condition:non_expired_grant=true"
	ID       string // stable unique id across the model: fmt.Sprintf("%s#%s::%s", Type, Relation, Label)
}

// enumerateBranches walks every type#relation rewrite rule in lm and returns
// the set of coverage branches: one per directly-assignable type (wildcards
// and conditioned types get their own branches too), one per computed/ttu
// edge, one per subtracted ("but not") exclusion arm, and two per conditioned
// direct type (an eval-true and an eval-false branch). Branches are
// deduplicated by ID. unreachable lists "<type>#<relation>" for any relation
// whose rewrite produced no branches at all — see the package doc comment in
// this file for the exact heuristic.
func enumerateBranches(lm *LoadedModel) (branches []branchDef, unreachable []string) {
	if lm == nil || lm.Proto == nil {
		return nil, nil
	}

	types := append([]*openfgav1.TypeDefinition(nil), lm.Proto.GetTypeDefinitions()...)
	sort.Slice(types, func(i, j int) bool { return types[i].GetType() < types[j].GetType() })

	seen := map[string]bool{}

	for _, td := range types {
		relNames := make([]string, 0, len(td.GetRelations()))
		for name := range td.GetRelations() {
			relNames = append(relNames, name)
		}
		sort.Strings(relNames)

		directByRelation := metadataDirectTypes(td.GetMetadata())

		for _, relName := range relNames {
			w := branchWalker{typ: td.GetType(), relation: relName, direct: directByRelation[relName]}
			relBranches := w.walk(td.GetRelations()[relName])

			if len(relBranches) == 0 {
				unreachable = append(unreachable, td.GetType()+"#"+relName)
				continue
			}
			for _, b := range relBranches {
				if seen[b.ID] {
					continue
				}
				seen[b.ID] = true
				branches = append(branches, b)
			}
		}
	}
	return branches, unreachable
}

// metadataDirectTypes extracts each relation's directly-related user types
// from the type definition's metadata, keyed by relation name.
func metadataDirectTypes(meta *openfgav1.Metadata) map[string][]*openfgav1.RelationReference {
	out := map[string][]*openfgav1.RelationReference{}
	for rel, relMeta := range meta.GetRelations() {
		out[rel] = relMeta.GetDirectlyRelatedUserTypes()
	}
	return out
}

// branchWalker recurses through a single relation's Userset rewrite tree,
// emitting branchDef values. direct holds the relation's directly-related user
// types from metadata, consulted whenever the tree hits a "this" leaf
// (metadata is per-relation, not per tree position, so the same list applies
// no matter where "this" appears in the rewrite).
type branchWalker struct {
	typ      string
	relation string
	direct   []*openfgav1.RelationReference
}

// walk mirrors the recursion in internal/fga/graph.go:rewriteEdges (union,
// intersection, difference base/subtract, computed, ttu) but, unlike that
// helper, also emits direct/wildcard/condition branches and treats the
// subtracted arm of a difference as a single first-class branch rather than
// recursing into it — an untested exclusion is the coverage hole we want to
// surface, not the internal shape of the excluded expression.
func (w branchWalker) walk(u *openfgav1.Userset) []branchDef {
	switch {
	case u == nil:
		return nil
	case u.GetThis() != nil:
		return w.directBranches()
	case u.GetComputedUserset() != nil:
		return w.computedBranch(u.GetComputedUserset())
	case u.GetTupleToUserset() != nil:
		return w.ttuBranch(u.GetTupleToUserset())
	case u.GetUnion() != nil:
		var out []branchDef
		for _, child := range u.GetUnion().GetChild() {
			out = append(out, w.walk(child)...)
		}
		return out
	case u.GetIntersection() != nil:
		var out []branchDef
		for _, child := range u.GetIntersection().GetChild() {
			out = append(out, w.walk(child)...)
		}
		return out
	case u.GetDifference() != nil:
		diff := u.GetDifference()
		out := w.walk(diff.GetBase())
		return append(out, w.subtractBranch(diff.GetSubtract()))
	default:
		return nil
	}
}

// directBranches emits one branch per directly-related user type (from
// metadata), plus a true/false pair for each one that carries a condition.
func (w branchWalker) directBranches() []branchDef {
	var out []branchDef
	for _, ref := range w.direct {
		if ref.GetType() == "" {
			continue
		}
		kind, label := "direct", "direct:"+ref.GetType()
		switch {
		case ref.GetWildcard() != nil:
			kind, label = "wildcard", "wildcard:"+ref.GetType()
		case ref.GetRelation() != "":
			label = "direct:" + ref.GetType() + "#" + ref.GetRelation()
		}
		out = append(out, w.branch(kind, label))

		// Condition branches are creditable only for concrete-type/wildcard leaves:
		// condition credit flows through direct-member leaves (see narrator.go
		// isDirectMember), which never match a userset ref (type#relation). Emitting
		// condition:true/false for a conditioned userset assignment would produce
		// branches that can never be covered — permanently uncovered noise that
		// spuriously trips --coverage-diff and depresses --coverage-min — so skip
		// them for userset refs (GetRelation() != "").
		if cond := ref.GetCondition(); cond != "" && ref.GetRelation() == "" {
			out = append(out,
				w.branch("condition", fmt.Sprintf("condition:%s=true", cond)),
				w.branch("condition", fmt.Sprintf("condition:%s=false", cond)),
			)
		}
	}
	return out
}

func (w branchWalker) computedBranch(cu *openfgav1.ObjectRelation) []branchDef {
	if cu.GetRelation() == "" {
		return nil
	}
	return []branchDef{w.branch("computed", "computed:"+cu.GetRelation())}
}

func (w branchWalker) ttuBranch(ttu *openfgav1.TupleToUserset) []branchDef {
	target := ttu.GetComputedUserset().GetRelation()
	if target == "" {
		return nil
	}
	via := ttu.GetTupleset().GetRelation()
	return []branchDef{w.branch("ttu", fmt.Sprintf("ttu:%s/%s", via, target))}
}

// subtractBranch represents the entire subtracted ("but not") expression as
// a single branch: whatever shape it has (a computed relation, a nested
// union, etc.), the coverage question is simply "was this exclusion ever
// exercised", so it does not recurse into the subtracted subtree.
func (w branchWalker) subtractBranch(sub *openfgav1.Userset) branchDef {
	return w.branch("difference-subtract", "but-not:"+describeUserset(sub))
}

func (w branchWalker) branch(kind, label string) branchDef {
	return branchDef{
		Type:     w.typ,
		Relation: w.relation,
		Kind:     kind,
		Label:    label,
		ID:       fmt.Sprintf("%s#%s::%s", w.typ, w.relation, label),
	}
}

// describeUserset produces a short, best-effort description of a userset
// expression for use inside a "but-not:<desc>" label.
func describeUserset(u *openfgav1.Userset) string {
	switch {
	case u == nil:
		return "expr"
	case u.GetThis() != nil:
		return "this"
	case u.GetComputedUserset() != nil:
		if r := u.GetComputedUserset().GetRelation(); r != "" {
			return r
		}
		return "expr"
	case u.GetTupleToUserset() != nil:
		ttu := u.GetTupleToUserset()
		return fmt.Sprintf("%s/%s", ttu.GetTupleset().GetRelation(), ttu.GetComputedUserset().GetRelation())
	case u.GetUnion() != nil:
		return describeChildren(u.GetUnion().GetChild(), "|")
	case u.GetIntersection() != nil:
		return describeChildren(u.GetIntersection().GetChild(), "&")
	case u.GetDifference() != nil:
		d := u.GetDifference()
		return fmt.Sprintf("(%s)-(%s)", describeUserset(d.GetBase()), describeUserset(d.GetSubtract()))
	default:
		return "expr"
	}
}

func describeChildren(children []*openfgav1.Userset, sep string) string {
	parts := make([]string, 0, len(children))
	for _, c := range children {
		parts = append(parts, describeUserset(c))
	}
	return strings.Join(parts, sep)
}

// relOutcome tracks whether a "type#relation" was resolved by at least one
// assertion in a run (seen), and — since condition branches are split by
// outcome — which named conditions on its own direct leaves were evaluated to
// true and/or false. condTrue/condFalse are keyed by condition name and set
// only from a direct-member leaf carrying that condition, so each records "the
// named condition was actually evaluated to this outcome", not merely "the
// relation resolved to it". Keying per condition name (rather than one shared
// sawTrue/sawFalse pair) is what stops a relation with several conditions from
// crediting an untested condition's branch off an unrelated grant.
type relOutcome struct {
	granted   map[string]bool // arm branch labels shown to grant (grant-based, from computeArmGrants)
	subtract  bool            // a difference ("but not") arm was actually exercised
	listGrant bool            // a list_objects/list_users assertion returned at least one result
	condTrue  map[string]bool
	condFalse map[string]bool
}

func newRelOutcome() *relOutcome {
	return &relOutcome{granted: map[string]bool{}, condTrue: map[string]bool{}, condFalse: map[string]bool{}}
}

// outcomeFor returns acc[key], creating it on first use.
func outcomeFor(acc map[string]*relOutcome, key string) *relOutcome {
	out := acc[key]
	if out == nil {
		out = newRelOutcome()
		acc[key] = out
	}
	return out
}

// resolvedRelations folds one assertion's coverage credit into acc: the
// per-arm grant credit computed from the marked resolution tree
// (exp.grantedArms / exp.subtractRels, see computeArmGrants) plus the
// per-condition condTrue/condFalse from the tree walk (walkExplainNode). It
// merges into acc rather than replacing it, so a run's every assertion folds
// into one shared map.
func resolvedRelations(exp *Explain, acc map[string]*relOutcome) {
	if exp == nil {
		return
	}
	walkExplainNode(exp.Tree, acc)
	for key, labels := range exp.grantedArms {
		out := outcomeFor(acc, key)
		for l := range labels {
			out.granted[l] = true
		}
	}
	for key := range exp.subtractRels {
		outcomeFor(acc, key).subtract = true
	}
}

func walkExplainNode(n *ExplainNode, acc map[string]*relOutcome) {
	if n == nil {
		return
	}
	// A conditioned direct-member leaf is the only node that credits coverage
	// from the tree walk (the condition:<c>=true/=false branches); everything
	// else is credited from the grant-based arm walk in resolvedRelations.
	if n.Rel != "" && n.DirectMember && len(n.Conditions) > 0 {
		out := outcomeFor(acc, n.Rel)
		// condTrue/condFalse credit the condition:<c>=true/=false branches, which
		// must reflect condition <c> actually being evaluated on this relation's
		// own conditioned direct leaf — not any incidental resolution outcome. A
		// condition can only be evaluated when the queried user is a direct member
		// (DirectMember): granted => the tuple's condition held (=true); denied =>
		// it failed (=false). We credit only the specific condition name(s) that
		// leaf's tuple carried (n.Conditions), so a relation with several
		// conditions can't credit an untested one off an unrelated grant, and a
		// plain no-tuple deny (not a direct member) credits nothing.
		for _, c := range n.Conditions {
			if n.Result {
				out.condTrue[c] = true
			} else {
				out.condFalse[c] = true
			}
		}
	}
	for _, c := range n.Children {
		walkExplainNode(c, acc)
	}
}

// relKey normalizes a node's own "type:id#relation" identity (fga.ResNode.Name)
// into a "type#relation" coverage key. The left of '#' is "type:id" (the type
// is the part before ':'); the right is the relation name. Names with no '#'
// (e.g. an operator/union node, which has no identity of its own) don't
// identify a relation and are skipped.
func relKey(name string) (string, bool) {
	left, right, ok := strings.Cut(name, "#")
	if !ok {
		return "", false
	}
	typ := idType(left)
	if typ == "" || right == "" {
		return "", false
	}
	return typ + "#" + right, true
}

// aggregate rolls branches up into a Coverage summary using resolved, the
// merged per-"type#relation" outcomes gathered from every assertion's
// resolution tree in a run (see resolvedRelations). A branch is covered when
// its containing type#relation was resolved by some assertion: a
// condition:<c>=true/=false branch specifically needs some assertion to have
// resolved that relation to that outcome; every other kind just needs the
// relation to have been resolved at all. unreachable (from enumerateBranches)
// passes straight through onto Coverage.Unreachable.
func aggregate(branches []branchDef, unreachable []string, resolved map[string]*relOutcome) *Coverage {
	type typeRel struct{ typ, rel string }

	var order []typeRel
	byRel := map[typeRel][]branchDef{}
	for _, b := range branches {
		k := typeRel{b.Type, b.Relation}
		if _, ok := byRel[k]; !ok {
			order = append(order, k)
		}
		byRel[k] = append(byRel[k], b)
	}

	var typeOrder []string
	byType := map[string]*TypeCov{}
	cov := &Coverage{Unreachable: unreachable, Complete: len(unreachable) == 0}

	for _, k := range order {
		rc := RelCov{Relation: k.rel}
		out := resolved[k.typ+"#"+k.rel]
		for _, b := range byRel[k] {
			rc.Total++
			covered := branchCovered(b, out)
			if covered {
				rc.Covered++
			} else {
				rc.Missed = append(rc.Missed, b.Label)
			}
			rc.Branches = append(rc.Branches, BranchCov{Label: b.Label, Kind: b.Kind, Covered: covered})
		}

		tc, ok := byType[k.typ]
		if !ok {
			tc = &TypeCov{Type: k.typ}
			byType[k.typ] = tc
			typeOrder = append(typeOrder, k.typ)
		}
		tc.Relations = append(tc.Relations, rc)
		tc.Covered += rc.Covered
		tc.Total += rc.Total

		cov.Covered += rc.Covered
		cov.Total += rc.Total
	}

	sort.Strings(typeOrder)
	for _, t := range typeOrder {
		cov.Types = append(cov.Types, *byType[t])
	}

	if cov.Total > 0 {
		cov.Percent = 100 * float64(cov.Covered) / float64(cov.Total)
	} else if len(unreachable) == 0 {
		// No branches at all (e.g. an empty model) isn't a coverage gap —
		// report 100% rather than a misleading 0%.
		cov.Percent = 100
	}

	return cov
}

// branchCovered reports whether a single branch was covered, given the outcome
// its containing relation was resolved to (nil if never resolved).
//
// Semantics — grant-based coverage: a branch counts covered only when a test
// demonstrated it can GRANT.
//   - A direct/wildcard/computed/ttu arm is covered when some check assertion's
//     resolution showed that specific arm granting the queried user
//     (out.granted, populated by computeArmGrants from the marked tree).
//   - A difference ("but not") arm is covered when its subtract actually
//     excluded a user (out.subtract).
//   - A condition:<c>=true/=false branch is covered when that named condition
//     was evaluated to that outcome on a direct-member leaf.
//
// list_objects/list_users assertions have no per-arm resolution tree, so they
// credit at relation granularity only when a list assertion returned at least
// one result (out.listGrant). An empty result proves a denial and must not
// increase grant-based coverage.
func branchCovered(b branchDef, out *relOutcome) bool {
	if out == nil {
		return false
	}
	switch b.Kind {
	case "condition":
		// Label is "condition:<name>=true" / "=false"; credit only if that named
		// condition was evaluated to the matching outcome on a direct-member leaf.
		if name, ok := strings.CutSuffix(b.Label, "=true"); ok {
			return out.condTrue[strings.TrimPrefix(name, "condition:")]
		}
		name := strings.TrimSuffix(b.Label, "=false")
		return out.condFalse[strings.TrimPrefix(name, "condition:")]
	case "difference-subtract":
		return out.subtract || out.listGrant
	default:
		return out.granted[b.Label] || out.listGrant
	}
}

// buildCoverage aggregates coverage for a completed run: it folds every
// assertion's resolution tree (and, for list_objects/list_users, a
// best-effort structural credit) into a shared per-"type#relation" outcome
// map, then rolls that up against the model the tests actually ran against
// via aggregate (branch-grained).
//
// v1 requires a single model across every task that ran — see
// resolveCoverageModelPath — since branchDef has no per-test model identity to
// enumerate against otherwise.
func buildCoverage(ws *Workspace, tasks []runTask, results []TestResult, opts Options, models *modelCache) (*Coverage, error) {
	acc := map[string]*relOutcome{}
	bounded := false
	for _, tr := range results {
		for _, ar := range tr.Assertions {
			if ar.coverageErr != nil {
				return nil, fmt.Errorf("%s: coverage trace failed: %w", tr.Name, ar.coverageErr)
			}
			if ar.Explain != nil {
				resolvedRelations(ar.Explain, acc)
				bounded = bounded || ar.Explain.Bounded
			}
			markStructuralCredit(ar, acc)
		}
	}

	modelPath, err := resolveCoverageModelPath(ws, tasks)
	if err != nil {
		return nil, err
	}
	lm, err := models.load(modelPath)
	if err != nil {
		// Surface it rather than enumerating against a nil model and reporting an
		// empty/misleading 0-branch coverage as if it were real.
		return nil, fmt.Errorf("load model for coverage: %w", err)
	}

	branches, unreachable := enumerateBranches(lm)
	cov := aggregate(branches, unreachable, acc)
	cov.Bounded = bounded
	if opts.DiffBaseModel != nil {
		cov.Diff = diffCoverage(branches, opts.DiffBaseModel, acc, opts.DiffBaseName)
	}
	return cov, nil
}

// diffCoverage reports which of the current model's branches are absent from the
// base model (i.e. added by the change), and whether each is covered.
func diffCoverage(current []branchDef, base *LoadedModel, acc map[string]*relOutcome, baseName string) *CoverageDiff {
	baseBranches, _ := enumerateBranches(base)
	baseIDs := make(map[string]bool, len(baseBranches))
	for _, b := range baseBranches {
		baseIDs[b.ID] = true
	}

	d := &CoverageDiff{Base: baseName}
	for _, b := range current {
		if baseIDs[b.ID] {
			continue
		}
		covered := branchCovered(b, acc[b.Type+"#"+b.Relation])
		d.Added = append(d.Added, DiffBranch{Type: b.Type, Relation: b.Relation, Label: b.Label, Covered: covered})
		if !covered {
			d.Uncovered++
		}
	}
	return d
}

// resolveCoverageModelPath chooses the single model a workspace's branch
// coverage is enumerated against, using the exact same test-file-overrides-
// manifest precedence as resolveModelPath (via that function), applied to
// every task that actually ran. A workspace where different test files
// override to different models has no single model coverage can honestly
// enumerate against, so more than one distinct resolved path is a usage
// error rather than a silently misleading percentage.
func resolveCoverageModelPath(ws *Workspace, tasks []runTask) (string, error) {
	seen := map[string]bool{}
	var paths []string
	for _, tk := range tasks {
		p, err := resolveModelPath(ws, tk.tf)
		if err != nil {
			return "", err
		}
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}

	switch len(paths) {
	case 0:
		return "", fmt.Errorf("no model found for coverage")
	case 1:
		return paths[0], nil
	default:
		return "", clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--coverage requires a single model; workspace uses %d (test files override model:)", len(paths)))
	}
}

// markStructuralCredit gives a list_objects/list_users assertion with at least
// one returned object/user relation-granular grant credit. Empty results prove
// denial behavior and therefore do not cover any granting arm.
func markStructuralCredit(ar AssertionResult, acc map[string]*relOutcome) {
	if ar.covKey == "" {
		return
	}
	got, ok := ar.Got.([]string)
	if !ok || len(got) == 0 {
		return
	}
	outcomeFor(acc, ar.covKey).listGrant = true
}
