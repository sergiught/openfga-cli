package modeltest

import (
	"context"
	"fmt"
	"io"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/style"
)

// nearestMissMaxCandidates bounds how many one-tuple probes nearestMiss will
// run, so a wide model can't turn a single failed check into an unbounded
// number of engine.Check calls.
const nearestMissMaxCandidates = 32

// nearestMissCandidate is one probe: the tuple(s) to add as contextual tuples
// and the human-readable description to return if they flip the verdict.
type nearestMissCandidate struct {
	tuples   []*openfgav1.TupleKey
	describe string
}

// nearestMiss finds the shallowest single tuple that, if it existed, would
// flip a currently-failing check to true, so a failure explanation can
// suggest "the tuple you're probably missing". It never mutates the store:
// every candidate is probed as a contextual tuple on top of r's own.
//
// Candidates are enumerated from the target relation's rewrite structure
// (via internal/fga.ParseModel) and tried shallowest-first: a direct grant on
// the target relation itself, then a direct grant on a relation the target
// computes from, then a tuple-to-userset link plus grant. Enumeration is
// capped at nearestMissMaxCandidates and at depth 2, so it never crawls the
// whole model. If no candidate flips the verdict, it returns "" (not an
// error) — the caller simply omits the nearest-miss line.
func nearestMiss(ctx context.Context, lm *LoadedModel, eng Resolver, sc Scope, r CheckReq) (string, error) {
	g := fga.ParseModel(lm.SDK)
	candidates := nearestMissCandidates(g, r)

	for _, c := range candidates {
		probe := CheckReq{
			User:             r.User,
			Relation:         r.Relation,
			Object:           r.Object,
			Context:          r.Context,
			ContextualTuples: append(append([]*openfgav1.TupleKey{}, r.ContextualTuples...), c.tuples...),
		}
		ok, err := eng.Check(ctx, sc, probe)
		if err != nil {
			// A candidate can be structurally invalid to write as a tuple
			// (e.g. a concrete user probed against a wildcard-only relation
			// that this enumeration failed to special-case) — skip it rather
			// than aborting the whole search.
			continue
		}
		if ok {
			return c.describe, nil
		}
	}

	return "", nil
}

// nearestMissCandidates enumerates bounded, shallow one-tuple (or, for
// tuple-to-userset, link+grant tuple-pair) candidates for r, ordered
// shallowest-first: direct grants on the target relation, then direct grants
// on relations the target computes from, then tuple-to-userset paths.
func nearestMissCandidates(g fga.Graph, r CheckReq) []nearestMissCandidate {
	objType := idType(r.Object)
	userType := idType(r.User)

	tn := findTypeNode(g, objType)
	if tn == nil {
		return nil
	}
	rel := findRelationNode(tn, r.Relation)
	if rel == nil {
		return nil
	}

	var cands []nearestMissCandidate
	seen := map[string]bool{}

	add := func(c nearestMissCandidate, key string) {
		if len(cands) >= nearestMissMaxCandidates || seen[key] {
			return
		}
		seen[key] = true
		cands = append(cands, c)
	}

	// Depth 1: a direct grant on the target relation itself.
	if u := directProbeUser(rel.Edges, userType, r.User); u != "" {
		add(directCandidate(u, r.Relation, r.Object), r.Relation)
	}

	// Depth ~1.5: a direct grant on a relation the target computes from.
	for _, e := range rel.Edges {
		if e.Kind != "computed" {
			continue
		}
		computedRel := findRelationNode(tn, e.Label)
		if computedRel == nil {
			continue
		}
		if u := directProbeUser(computedRel.Edges, userType, r.User); u != "" {
			add(directCandidate(u, e.Label, r.Object), e.Label)
		}
	}

	// Depth 2: a tuple-to-userset link plus the grant on the linked object.
	for _, e := range rel.Edges {
		if e.Kind != "ttu" {
			continue
		}
		target, via, ok := fga.SplitTTU(e.Label)
		if !ok {
			continue
		}
		viaRel := findRelationNode(tn, via)
		if viaRel == nil {
			continue
		}
		for _, ve := range viaRel.Edges {
			if ve.Kind != "direct" {
				continue
			}
			parentType := idType(ve.Label)
			parentNode := findTypeNode(g, parentType)
			if parentNode == nil {
				continue
			}
			targetRel := findRelationNode(parentNode, target)
			if targetRel == nil {
				continue
			}
			if u := directProbeUser(targetRel.Edges, userType, r.User); u != "" {
				linkObject := parentType + ":nearest-miss"
				link := &openfgav1.TupleKey{User: linkObject, Relation: via, Object: r.Object}
				grant := &openfgav1.TupleKey{User: u, Relation: target, Object: linkObject}
				describe := fmt.Sprintf(
					"tuples (%s, %s, %s) and (%s, %s, %s) would grant it",
					linkObject, via, r.Object, u, target, linkObject,
				)
				add(nearestMissCandidate{tuples: []*openfgav1.TupleKey{link, grant}, describe: describe}, via+"|"+target)
			}
		}
	}

	return cands
}

// directCandidate builds a single-tuple candidate (user, relation, object)
// with the human-readable description used in the explanation.
func directCandidate(user, relation, object string) nearestMissCandidate {
	return nearestMissCandidate{
		tuples:   []*openfgav1.TupleKey{{User: user, Relation: relation, Object: object}},
		describe: fmt.Sprintf("a tuple (%s, %s, %s) would grant it", user, relation, object),
	}
}

// directProbeUser scans direct edges for one admitting userType and returns
// the user id to probe with, or "" if none match. Userset-typed labels
// ("group#member") never match here — satisfying them needs its own tuple
// chain, out of scope for a depth-2 probe. When the plain type is directly
// assignable (label == userType), it returns the concrete user id; when only
// the wildcard is (label == userType+":*"), it returns the wildcard form
// (userType+":*"), since a concrete-user tuple would fail store validation
// against a wildcard-only relation. A concrete match is preferred over a
// wildcard one if both are present.
func directProbeUser(edges []fga.RelationEdge, userType, user string) string {
	wildcard := false
	for _, e := range edges {
		if e.Kind != "direct" {
			continue
		}
		if e.Label == userType {
			return user
		}
		if e.Label == userType+":*" {
			wildcard = true
		}
	}
	if wildcard {
		return userType + ":*"
	}
	return ""
}

// findTypeNode returns the TypeNode named name, or nil.
func findTypeNode(g fga.Graph, name string) *fga.TypeNode {
	for i := range g.Types {
		if g.Types[i].Name == name {
			return &g.Types[i]
		}
	}
	return nil
}

// findRelationNode returns the Relation named name on t, or nil.
func findRelationNode(t *fga.TypeNode, name string) *fga.Relation {
	for i := range t.Relations {
		if t.Relations[i].Name == name {
			return &t.Relations[i]
		}
	}
	return nil
}

// RenderExplain writes a narrative of why a check / list_objects / list_users
// assertion failed to w, styled via internal/style (which downsamples to
// plain text under NO_COLOR/non-TTY at the writer layer). It only reads ar
// and never calls the engine.
//
// It always starts with the expected-vs-got line. For a check assertion it
// then renders ar.Explain.Tree: the full dead-ended resolution tree (plus any
// nearest-miss suggestion) when the check unexpectedly came back false, or
// just the granting path when it unexpectedly came back true. For
// list_objects/list_users it renders ar.Explain.SetDiff.
func RenderExplain(w io.Writer, ar AssertionResult) {
	fmt.Fprintf(w, "expected: %s    got: %s\n", style.Success.Render(formatExpectedGot(ar.Expected)), style.Failure.Render(formatExpectedGot(ar.Got)))

	if ar.Explain == nil {
		return
	}

	switch ar.Kind {
	case kindCheck:
		if ar.Explain.Tree == nil {
			return
		}
		if ar.Explain.Verdict {
			// Unexpected true: show only the path that granted it.
			renderExplainTree(w, flooredRoot(grantingPathNode(ar.Explain.Tree), ar.Explain.Verdict), "", "")
			return
		}
		// Unexpected false: show every dead-ended branch, then the suggestion.
		renderExplainTree(w, flooredRoot(ar.Explain.Tree, ar.Explain.Verdict), "", "")
		if ar.Explain.NearestMiss != "" {
			fmt.Fprintf(w, "nearest miss: %s\n", ar.Explain.NearestMiss)
		}
	case kindListObjects, kindListUsers:
		if ar.Explain.SetDiff != nil {
			renderSetDiff(w, ar.Explain.SetDiff)
		}
	}
}

// formatExpectedGot renders an AssertionResult.Expected/Got value for the
// expected/got line: a []string (list_objects/list_users) as a
// comma-separated "[a, b, c]", anything else (a check's bool) via fmt.Sprint.
func formatExpectedGot(v any) string {
	if s, ok := v.([]string); ok {
		return "[" + strings.Join(s, ", ") + "]"
	}
	return fmt.Sprint(v)
}

// renderExplainTree writes n and its children as an indented ASCII tree,
// prefix being the accumulated indentation and connector the "├─ "/"└─ "/""
// glyph that leads into n.
func renderExplainTree(w io.Writer, n *ExplainNode, prefix, connector string) {
	fmt.Fprintf(w, "%s%s%s [%s]", style.Faint.Render(prefix), style.Faint.Render(connector), n.Label, resultMarker(n.Result))
	if n.Reason != "" {
		fmt.Fprintf(w, "%s", style.Faint.Render(fmt.Sprintf(" — %s", n.Reason)))
	}
	fmt.Fprintln(w)

	childPrefix := prefix
	if connector == "└─ " {
		childPrefix += "   "
	} else if connector != "" {
		childPrefix += "│  "
	}
	for i, c := range n.Children {
		conn := "├─ "
		if i == len(n.Children)-1 {
			conn = "└─ "
		}
		renderExplainTree(w, c, childPrefix, conn)
	}
}

// flooredRoot is a display-only safeguard against a residual divergence
// between the computed tree root and the engine's authoritative verdict. If
// the root's Result already agrees with verdict (the normal case) it returns n
// unchanged; otherwise it returns a shallow copy whose Result is the engine
// verdict, so a user can never see a root badge that contradicts the got:
// line. The copy shares n's children and leaves n itself untouched, so
// crossvalidate_test (which reads the trace tree's computed Result before any
// rendering) still genuinely catches MarkGranted regressions.
func flooredRoot(n *ExplainNode, verdict bool) *ExplainNode {
	if n == nil || n.Result == verdict {
		return n
	}
	c := *n
	c.Result = verdict
	return &c
}

// grantingPathNode copies n, keeping the Result==true children that explain why
// n granted. For a union (or an exclusion, whose granting base is a single true
// child) any one true arm suffices, so it keeps just the first — drawing a
// single unbranched chain. For an intersection, though, the node grants only
// when ALL its children are true, so keeping just the first would drop the other
// required arms and mis-narrate the path; there it keeps every true child.
func grantingPathNode(n *ExplainNode) *ExplainNode {
	out := &ExplainNode{Label: n.Label, Result: n.Result, Reason: n.Reason}
	if n.Reason == reasonIntersection {
		for _, c := range n.Children {
			if c.Result {
				out.Children = append(out.Children, grantingPathNode(c))
			}
		}
		return out
	}
	for _, c := range n.Children {
		if c.Result {
			out.Children = []*ExplainNode{grantingPathNode(c)}
			break
		}
	}
	return out
}

// resultMarker renders an ExplainNode.Result as a truthy/falsy tag, colored
// success/failure (and degrading to the plain "true"/"false" text under
// NO_COLOR/non-TTY, since that downsampling happens at the writer layer).
func resultMarker(result bool) string {
	if result {
		return style.Success.Render("true")
	}
	return style.Failure.Render("false")
}

// renderSetDiff writes a list_objects/list_users SetDiff as +unexpected /
// -missing lines.
func renderSetDiff(w io.Writer, sd *SetDiff) {
	if len(sd.Unexpected) > 0 {
		fmt.Fprintf(w, "+unexpected: %s\n", strings.Join(sd.Unexpected, ", "))
	}
	if len(sd.Missing) > 0 {
		fmt.Fprintf(w, "-missing: %s\n", strings.Join(sd.Missing, ", "))
	}
}
