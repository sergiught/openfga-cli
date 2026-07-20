package fga

import (
	"slices"
	"strings"
)

// This file turns an OpenFGA Expand response — the userset tree that grants a
// relation — into a structured ResNode tree the playground can render. The
// Expand tree arrives as an untyped, recursive map[string]any (its shape is
// schema-dependent), so we walk it defensively.

// ResOp identifies how a resolution node combines its children.
type ResOp int

const (
	ResLeaf         ResOp = iota // a direct-users / computed / tuple-to-userset leaf
	ResUnion                     // any child grants
	ResIntersection              // all children grant
	ResExclusion                 // base grants and subtract does not (difference)
)

// ResNode is one node of a Check resolution. Each node resolves a single
// object#relation, either as a leaf (direct users, a computed userset, or a
// tuple-to-userset) or as a boolean combination of child nodes.
type ResNode struct {
	Name     string     // the "object#relation" this node resolves
	Op       ResOp      // how Children combine (ResLeaf → no children)
	Children []*ResNode // operands for union / intersection / exclusion

	// Leaf payloads — at most one is populated when Op == ResLeaf:
	Users    []string // direct users/usersets, e.g. ["user:anne", "team:eng#member"]
	Computed string   // a computed userset, e.g. "document:roadmap#owner"
	TTUFrom  string   // tuple-to-userset: the tupleset relation, e.g. "document:x#parent"
	TTUTo    []string // the computed usersets reached through that tupleset

	Granted bool // set by MarkGranted: this node reaches the queried user
}

// GrantResolver supplies the live lookups MarkGranted needs. Check reports
// whether `user` has `relation` on `object`. Tupleset returns the objects
// related to `object` via `relation` — the "user" side of matching tuples —
// and may be nil to skip tuple-to-userset resolution.
type GrantResolver struct {
	Check    func(user, relation, object string) bool
	Tupleset func(object, relation string) []string
	// CheckDirectLeaves, when true, makes a direct-user leaf defer to Check so
	// tuple conditions are honored; this requires Check to forward the request
	// context (Context/ContextualTuples) so an ABAC-conditioned direct tuple is
	// evaluated against the same context the engine used. When false (the
	// default) direct membership from the Expand tree is trusted as-is — the
	// cheap, context-free behavior a caller with no context (e.g. the
	// playground) wants, avoiding a remote Check per direct leaf.
	CheckDirectLeaves bool
}

// MarkGranted annotates each node with whether it grants `user`. Direct-user
// leaves match the user string exactly, computed usersets resolve via a Check,
// and tuple-to-userset leaves read the tupleset then Check the computed
// relation on each related object. It returns the root's grant status.
func MarkGranted(root *ResNode, user string, r GrantResolver) bool {
	if root == nil {
		return false
	}
	switch root.Op {
	case ResLeaf:
		if len(root.Children) > 0 {
			// An expanded computed-userset / tuple-to-userset leaf (see
			// ExpandTree): it grants when any reference it expands into grants.
			root.Granted = false
			for _, c := range root.Children {
				if MarkGranted(c, user, r) {
					root.Granted = true
				}
			}
		} else {
			root.Granted = leafGrants(root, user, r)
		}
	case ResUnion:
		root.Granted = false
		for _, c := range root.Children {
			if MarkGranted(c, user, r) {
				root.Granted = true
			}
		}
	case ResIntersection:
		root.Granted = len(root.Children) > 0
		for _, c := range root.Children {
			if !MarkGranted(c, user, r) {
				root.Granted = false
			}
		}
	case ResExclusion:
		base, sub := false, false
		if len(root.Children) > 0 {
			base = MarkGranted(root.Children[0], user, r)
		}
		if len(root.Children) > 1 {
			sub = MarkGranted(root.Children[1], user, r)
		}
		root.Granted = base && !sub
	}
	return root.Granted
}

// GrantedPath returns a pruned copy of the tree keeping only the branch(es)
// that reach the user — the ACL resolution path. It returns nil when nothing
// grants (e.g. a denied check). Call MarkGranted first.
func GrantedPath(n *ResNode) *ResNode {
	if n == nil || !n.Granted {
		return nil
	}
	c := *n
	c.Children = nil
	for _, k := range n.Children {
		if g := GrantedPath(k); g != nil {
			c.Children = append(c.Children, g)
		}
	}
	return &c
}

// Expander resolves an object#relation to its (single-level) Expand subtree,
// or nil when it can't be expanded (API error, or no such resolution).
type Expander func(object, relation string) *ResNode

// ExpandTree recursively expands computed-userset and tuple-to-userset leaves
// in place, attaching each reference's own resolution subtree so nested
// branches appear instead of dead-end leaves — e.g. a `viewer` node that
// resolves through `owner` gains `owner`'s subtree (and, in turn, its users)
// as a child.
//
// OpenFGA's Expand API resolves only one level, so this issues a fresh expand
// per referenced relation via the `expand` callback; `tupleset` lists the
// objects a tuple-to-userset points at. rootRef ("object#relation" of root)
// seeds cycle detection. maxDepth bounds recursion depth and maxNodes bounds
// the total number of expansions (i.e. extra API calls), so a deep or cyclic
// model can't fan out unbounded. Call this before MarkGranted.
// ExpandTree returns whether it was truncated: an expandable arm (a computed or
// tuple-to-userset leaf) was left unexpanded because the depth or node budget
// ran out. A cycle stop does NOT count as truncation (it is correct). A caller
// computing coverage from the tree should treat a truncated tree as partial —
// arms below the cut were never evaluated and so can't be credited.
func ExpandTree(root *ResNode, rootRef string, expand Expander, tupleset func(object, relation string) []string, maxDepth, maxNodes int) bool {
	if root == nil || expand == nil {
		return false
	}
	budget := maxNodes
	truncated := false
	expandNode(root, expand, tupleset, maxDepth, map[string]bool{rootRef: true}, &budget, &truncated)
	return truncated
}

func expandNode(n *ResNode, expand Expander, tupleset func(string, string) []string, depth int, path map[string]bool, budget *int, truncated *bool) {
	if n == nil {
		return
	}
	// Descend into existing structural children (union / intersection / difference).
	for _, c := range n.Children {
		expandNode(c, expand, tupleset, depth, path, budget, truncated)
	}
	if n.Op != ResLeaf || len(n.Children) > 0 {
		return
	}
	// Only an unexpanded leaf gets expanded, and only while depth remains. If
	// depth ran out on a leaf that COULD have expanded, the tree is truncated.
	expandable := n.Computed != "" || (n.TTUFrom != "" && len(n.TTUTo) > 0 && tupleset != nil)
	if depth <= 0 {
		if expandable {
			*truncated = true
		}
		return
	}
	switch {
	case n.Computed != "":
		attachExpansion(n, n.Computed, expand, tupleset, depth, path, budget, truncated)
	case n.TTUFrom != "" && len(n.TTUTo) > 0 && tupleset != nil:
		tObj, tRel, ok := splitUserset(n.TTUFrom)
		if !ok {
			return
		}
		for _, x := range tupleset(tObj, tRel) {
			for _, cu := range n.TTUTo {
				rel := cu
				if _, r2, ok := splitUserset(cu); ok {
					rel = r2
				}
				attachExpansion(n, x+"#"+rel, expand, tupleset, depth, path, budget, truncated)
			}
		}
	}
}

// attachExpansion expands the object#relation `ref` and, on success, appends its
// (recursively expanded) subtree as a child of `parent`. It is a no-op on a
// cycle (ref already on the ancestry path) or an exhausted budget; the latter
// sets *truncated so the caller knows the tree is partial.
func attachExpansion(parent *ResNode, ref string, expand Expander, tupleset func(string, string) []string, depth int, path map[string]bool, budget *int, truncated *bool) {
	if path[ref] {
		return // cycle — correct termination, not truncation
	}
	if *budget <= 0 {
		*truncated = true
		return
	}
	obj, rel, ok := splitUserset(ref)
	if !ok {
		return
	}
	*budget--
	sub := expand(obj, rel)
	if sub == nil {
		return
	}
	next := make(map[string]bool, len(path)+1)
	for k := range path {
		next[k] = true
	}
	next[ref] = true
	expandNode(sub, expand, tupleset, depth-1, next, budget, truncated)
	parent.Children = append(parent.Children, sub)
}

func leafGrants(n *ResNode, user string, r GrantResolver) bool {
	switch {
	case len(n.Users) > 0:
		if slices.Contains(n.Users, user) || slices.Contains(n.Users, publicWildcard(user)) {
			// The user is directly assigned here — either listed literally or
			// admitted by the typed public wildcard for its type (e.g. "user:*"
			// admits "user:anne"), mirroring narrator.go isDirectMember and the
			// engine. Without the wildcard match a concrete query returned false
			// even though the engine grants, corrupting --explain narration and
			// --coverage attribution. The assignment may carry
			// an ABAC condition the Expand tree doesn't evaluate. A resolver that
			// opts into CheckDirectLeaves (and forwards the request context)
			// defers to Check so a conditioned direct tuple only grants when the
			// condition holds against the same context the engine used. A
			// resolver that does not opt in — the default, e.g. the playground's
			// context-free callback — trusts the direct membership from the
			// Expand tree and skips the extra Check, keeping the cheap,
			// context-free behavior.
			if r.CheckDirectLeaves && r.Check != nil {
				if obj, rel, ok := splitUserset(n.Name); ok {
					return r.Check(user, rel, obj)
				}
			}
			return true
		}
		// The user isn't listed literally, but a userset value here
		// (type:id#relation, e.g. "organization:acme#member") admits every user
		// that holds that relation on that object. Resolving that membership
		// needs a live Check, so — like the conditioned direct-leaf deferral
		// above — it's gated behind CheckDirectLeaves: context-aware callers
		// (the runner) resolve the arm so intermediate nodes match the engine,
		// while the context-free default (the playground) keeps its cheap
		// literal-only matching and never issues a per-leaf Check.
		if r.CheckDirectLeaves && r.Check != nil {
			for _, u := range n.Users {
				obj, rel, ok := splitUserset(u)
				if !ok || rel == "" {
					continue // a literal user or public wildcard, not a userset
				}
				if r.Check(user, rel, obj) {
					return true
				}
			}
		}
		return false
	case n.Computed != "":
		// A computed userset can only be resolved by a live Check; without one,
		// treat the branch as not granting rather than dereferencing a nil Check.
		if r.Check == nil {
			return false
		}
		if obj, rel, ok := splitUserset(n.Computed); ok {
			return r.Check(user, rel, obj)
		}
	case n.TTUFrom != "" && r.Tupleset != nil && r.Check != nil:
		// The object relates to some X via the tupleset; the user is granted if
		// they hold one of the computed relations on any such X. Needs a live
		// Check to resolve the computed relation, so a nil Check skips it (not
		// granted) instead of panicking.
		tObj, tRel, ok := splitUserset(n.TTUFrom)
		if !ok {
			return false
		}
		for _, x := range r.Tupleset(tObj, tRel) {
			for _, cu := range n.TTUTo {
				rel := cu
				if _, r2, ok := splitUserset(cu); ok {
					rel = r2
				}
				if r.Check(user, rel, x) {
					return true
				}
			}
		}
	}
	return false
}

func splitUserset(s string) (object, relation string, ok bool) {
	return strings.Cut(s, "#")
}

// publicWildcard returns the typed public wildcard for a user id
// (e.g. "user:anne" → "user:*"), or "" when user carries no type.
func publicWildcard(user string) string {
	if i := strings.IndexByte(user, ':'); i >= 0 {
		return user[:i] + ":*"
	}
	return ""
}

// ParseResolution builds a ResNode tree from an Expand response's untyped tree
// (openfga.ExpandResponse.Tree). It returns false when the tree has no root.
func ParseResolution(tree map[string]any) (*ResNode, bool) {
	root, ok := resMap(tree["root"])
	if !ok {
		return nil, false
	}
	return parseResNode(root), true
}

func parseResNode(m map[string]any) *ResNode {
	n := &ResNode{Name: resStr(m["name"])}
	switch {
	case resHas(m, "leaf"):
		n.Op = ResLeaf
		parseResLeaf(n, resMapK(m, "leaf"))
	case resHas(m, "union"):
		n.Op = ResUnion
		n.Children = parseResNodes(resMapK(m, "union"))
	case resHas(m, "intersection"):
		n.Op = ResIntersection
		n.Children = parseResNodes(resMapK(m, "intersection"))
	case resHas(m, "difference"):
		n.Op = ResExclusion
		d := resMapK(m, "difference")
		if b, ok := resMap(d["base"]); ok {
			n.Children = append(n.Children, parseResNode(b))
		}
		if s, ok := resMap(d["subtract"]); ok {
			n.Children = append(n.Children, parseResNode(s))
		}
	}
	return n
}

func parseResNodes(m map[string]any) []*ResNode {
	list, _ := m["nodes"].([]any)
	out := make([]*ResNode, 0, len(list))
	for _, it := range list {
		if cm, ok := resMap(it); ok {
			out = append(out, parseResNode(cm))
		}
	}
	return out
}

func parseResLeaf(n *ResNode, leaf map[string]any) {
	if u, ok := resMap(leaf["users"]); ok {
		for _, x := range resSlice(u["users"]) {
			if s := resStr(x); s != "" {
				n.Users = append(n.Users, s)
			}
		}
		return
	}
	if c, ok := resMap(leaf["computed"]); ok {
		n.Computed = resStr(c["userset"])
		return
	}
	if t, ok := resMap(leaf["tupleToUserset"]); ok {
		n.TTUFrom = resStr(t["tupleset"])
		for _, x := range resSlice(t["computed"]) {
			if cm, ok := resMap(x); ok {
				n.TTUTo = append(n.TTUTo, resStr(cm["userset"]))
			}
		}
	}
}

// --- defensive accessors for the untyped tree ---

func resMap(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }
func resMapK(m map[string]any, k string) map[string]any {
	r, _ := resMap(m[k])
	return r
}
func resHas(m map[string]any, k string) bool { _, ok := m[k]; return ok }
func resStr(v any) string                    { s, _ := v.(string); return s }
func resSlice(v any) []any                   { s, _ := v.([]any); return s }
