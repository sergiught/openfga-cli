package fga

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
