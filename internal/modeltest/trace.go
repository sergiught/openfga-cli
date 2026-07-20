package modeltest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	protojson "google.golang.org/protobuf/encoding/protojson"

	"github.com/sergiught/openfga-cli/internal/fga"
)

// Recursive resolution safety bounds mirror the playground: how deep the tree
// recurses and how many extra Expand calls it may make, so a deep or cyclic
// model can't fan the tree out without limit.
const (
	traceMaxDepth = 8
	traceMaxNodes = 64
)

// trace produces a resolution narrative for a Check assertion. It fetches the
// engine's Expand (userset) tree, runs it through internal/fga's proven
// resolution pipeline — the same one the playground uses — and renders the
// resulting tree as an ExplainNode. The verdict comes from the engine's own
// Check so it always matches the assertion outcome.
//
// The tree is faithful to the engine: it re-expands computed-userset and
// tuple-to-userset leaves via fresh Expand calls (bounded by traceMaxDepth /
// traceMaxNodes) and marks the branch that grants the queried user with live
// engine Checks.
func trace(ctx context.Context, lm *LoadedModel, eng Resolver, sc Scope, r CheckReq) (*Explain, error) {
	verdict, err := eng.Check(ctx, sc, r)
	if err != nil {
		return nil, fmt.Errorf("trace verdict check: %w", err)
	}

	// tupleset lists the "user" side of tuples for object#relation, used to
	// expand and resolve tuple-to-userset branches.
	tupleset := func(object, relation string) []string {
		xs, terr := eng.Read(ctx, sc, object, relation)
		if terr != nil {
			return nil
		}
		return xs
	}

	// expand fetches one level of resolution for object#relation and parses it
	// into a ResNode, so ExpandTree can splice nested branches in place of
	// dead-end leaves.
	expand := func(object, relation string) *fga.ResNode {
		tree, eerr := eng.Expand(ctx, sc, object, relation)
		if eerr != nil {
			return nil
		}
		m, merr := protoTreeToMap(tree)
		if merr != nil {
			return nil
		}
		sub, ok := fga.ParseResolution(m)
		if !ok {
			return nil
		}
		return sub
	}

	root := expand(r.Object, r.Relation)
	if root == nil {
		// No resolution tree (e.g. relation not expandable); still return the
		// engine verdict so callers have a faithful outcome.
		return &Explain{Verdict: verdict}, nil
	}

	bounded := fga.ExpandTree(root, r.Object+"#"+r.Relation, expand, tupleset, traceMaxDepth, traceMaxNodes)

	// check forwards the original request's Context and ContextualTuples so nested
	// resolution is condition-aware — a directly-assigned tuple guarded by an ABAC
	// condition must be evaluated against the same context the engine's Check used,
	// or the tree would grant a branch the engine denies. It backs both MarkGranted
	// and the userset-value attribution in computeArmGrants.
	check := func(user, relation, object string) bool {
		ok, cerr := eng.Check(ctx, sc, CheckReq{
			User:             user,
			Relation:         relation,
			Object:           object,
			Context:          r.Context,
			ContextualTuples: r.ContextualTuples,
		})
		return cerr == nil && ok
	}

	fga.MarkGranted(root, r.User, fga.GrantResolver{
		// Opt into condition-aware direct-leaf checking: an ABAC-conditioned direct
		// tuple must be evaluated against the request context, not trusted as bare
		// membership, or the tree would disagree with the engine's verdict.
		CheckDirectLeaves: true,
		Check:             check,
		Tupleset:          tupleset,
	})

	// condFor reports the ABAC condition name(s) on the direct tuple(s) that make
	// user a direct member of object#relation — reading both the literal user and
	// the public wildcard for its type — so coverage can credit the specific
	// condition branch exercised. Reads are memoized per object#relation since a
	// tree can revisit the same node.
	condCache := map[string]map[string][]string{}
	condFor := func(object, relation, user string) []string {
		key := object + "#" + relation
		byUser, ok := condCache[key]
		if !ok {
			byUser, _ = eng.ReadConditions(ctx, sc, object, relation)
			if byUser == nil {
				byUser = map[string][]string{}
			}
			condCache[key] = byUser
		}
		out := append([]string(nil), byUser[user]...)
		return append(out, byUser[idType(user)+":*"]...)
	}

	arms, subtract := computeArmGrants(root, r.User, check)

	return &Explain{
		Verdict:      verdict,
		Tree:         toExplainNode(root, r.User, condFor),
		Bounded:      bounded,
		grantedArms:  arms,
		subtractRels: subtract,
	}, nil
}

// protoTreeToMap converts a proto UsersetTree into the untyped map shape that
// fga.ParseResolution expects, reusing protojson so the JSON matches the shape
// the SDK-backed playground pipeline consumes.
func protoTreeToMap(tree *openfgav1.UsersetTree) (map[string]any, error) {
	b, err := protojson.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("marshal userset tree: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("unmarshal userset tree: %w", err)
	}
	return m, nil
}

// toExplainNode converts a resolved fga.ResNode tree into an ExplainNode tree.
// user is the queried subject, used to mark direct-member leaves (see
// ExplainNode.DirectMember) so coverage can distinguish a condition denial
// from a plain no-tuple denial.
func toExplainNode(n *fga.ResNode, user string, condFor func(object, relation, user string) []string) *ExplainNode {
	if n == nil {
		return nil
	}
	rel, _ := relKey(n.Name)
	directMember := isDirectMember(n, user)
	node := &ExplainNode{
		Label:        nodeLabel(n),
		Rel:          rel,
		Result:       n.Granted,
		Reason:       nodeReason(n),
		DirectMember: directMember,
	}
	if directMember {
		if obj, relation, ok := strings.Cut(n.Name, "#"); ok {
			node.Conditions = condFor(obj, relation, user)
		}
	}
	for _, c := range n.Children {
		node.Children = append(node.Children, toExplainNode(c, user, condFor))
	}
	return node
}

// isDirectMember reports whether user is a direct member of n's own direct-user
// leaf: either listed literally (e.g. "user:anne") or admitted by a public
// wildcard for the user's type (e.g. "user:*"). This mirrors the direct-leaf
// match in fga.leafGrants, so a false Result on such a node can only come from
// a failed ABAC condition — the signal a condition:<c>=false branch needs.
func isDirectMember(n *fga.ResNode, user string) bool {
	if user == "" {
		return false
	}
	wildcard := idType(user) + ":*"
	for _, u := range n.Users {
		if u == user || u == wildcard {
			return true
		}
	}
	return false
}

// nodeLabel builds a human-readable label for a resolution node, surfacing the
// computed userset or tuple-to-userset reference when present so the resolution
// path (e.g. "document:1#owner") is visible in the tree.
func nodeLabel(n *fga.ResNode) string {
	switch {
	case n.Computed != "":
		return n.Computed
	case n.TTUFrom != "":
		if len(n.TTUTo) > 0 {
			return n.TTUFrom + " → " + strings.Join(n.TTUTo, ", ")
		}
		return n.TTUFrom
	case len(n.Users) > 0 && n.Name != "":
		return n.Name + " [" + strings.Join(n.Users, ", ") + "]"
	case len(n.Users) > 0:
		return strings.Join(n.Users, ", ")
	default:
		return n.Name
	}
}

// Resolution reason labels for ExplainNode.Reason. Shared constants so the
// producer (nodeReason) and consumers (the explain/coverage walkers) can't
// drift apart on a bare string.
const (
	reasonUnion        = "union"
	reasonIntersection = "intersection"
	reasonExclusion    = "exclusion"
)

// computeArmGrants walks the marked resolution tree and records, per
// "type#relation", the arm branch labels shown to GRANT the queried user
// (grant-based coverage), plus the relations whose difference ("but not") arm
// was actually exercised. An arm is credited only when its node granted, so a
// branch counts covered only when a test demonstrated it can grant. The labels
// produced here mirror those enumerateBranches emits (see branchWalker) so
// branchCovered can match them.
func computeArmGrants(root *fga.ResNode, user string, check func(user, relation, object string) bool) (arms map[string]map[string]bool, subtract map[string]bool) {
	arms = map[string]map[string]bool{}
	subtract = map[string]bool{}
	userType := idType(user)

	credit := func(key, label string) {
		if arms[key] == nil {
			arms[key] = map[string]bool{}
		}
		arms[key][label] = true
	}

	var visit func(n *fga.ResNode)
	visit = func(n *fga.ResNode) {
		if n == nil {
			return
		}
		key, hasKey := relKey(n.Name)

		switch n.Op {
		case fga.ResExclusion:
			// children[0] is the base (its arms belong to this relation); the rest
			// are the subtracted ("but not") arms. The but-not branch is exercised
			// when a subtract arm actually granted — the user is in the excluded set,
			// so the exclusion did work. Don't descend into the subtract subtree for
			// arm crediting: enumerateBranches models the whole subtracted expression
			// as one difference-subtract branch.
			if len(n.Children) > 0 {
				visit(n.Children[0])
				if hasKey {
					for _, s := range n.Children[1:] {
						if s.Granted {
							subtract[key] = true
						}
					}
				}
			}
			return
		case fga.ResLeaf:
			if hasKey && n.Granted {
				switch {
				case n.Computed != "":
					credit(key, "computed:"+relPart(n.Computed))
				case n.TTUFrom != "":
					for _, t := range n.TTUTo {
						credit(key, "ttu:"+relPart(n.TTUFrom)+"/"+relPart(t))
					}
				case len(n.Users) > 0:
					creditDirectLeaf(n, user, userType, key, credit, check)
				}
			}
		}
		// Recurse into arms (union/intersection children) and spliced expansions
		// (a computed/ttu leaf's resolved subtree).
		for _, c := range n.Children {
			visit(c)
		}
	}
	visit(root)
	return arms, subtract
}

// creditDirectLeaf credits the direct/wildcard branch(es) a granting direct-users
// leaf exercised for the queried user: a literal match credits direct:<type>, a
// public-wildcard match credits wildcard:<type>, and a grant reached only through
// a userset value (type:id#relation) credits direct:<type>#<relation>. check
// attributes the userset case: a leaf may list several userset values but only
// some admit the user, so only those that Check confirms are credited (grant-based
// coverage credits only the arm that actually granted this check).
func creditDirectLeaf(n *fga.ResNode, user, userType, key string, credit func(key, label string), check func(user, relation, object string) bool) {
	matched := false
	for _, u := range n.Users {
		switch {
		case u == user:
			credit(key, "direct:"+idType(u))
			matched = true
		case u == userType+":*":
			credit(key, "wildcard:"+userType)
			matched = true
		}
	}
	if matched {
		return
	}
	// Granted but not via a literal/wildcard entry — the grant came through a
	// userset value; credit only the value(s) that actually admit the user.
	for _, u := range n.Users {
		if i := strings.IndexByte(u, '#'); i >= 0 {
			obj, rel := u[:i], u[i+1:]
			if check == nil || check(user, rel, obj) {
				credit(key, "direct:"+idType(obj)+"#"+rel)
			}
		}
	}
}

// relPart returns the relation name from a "type:id#relation" or
// "type#relation" string (the part after '#'), or the string unchanged when it
// has no '#'.
func relPart(s string) string {
	if i := strings.IndexByte(s, '#'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// nodeReason describes how a node combines its children.
func nodeReason(n *fga.ResNode) string {
	switch n.Op {
	case fga.ResUnion:
		return reasonUnion
	case fga.ResIntersection:
		return reasonIntersection
	case fga.ResExclusion:
		return reasonExclusion
	default:
		return ""
	}
}
