package fga

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// node/leaf/etc. build untyped Expand-tree fragments the way the API returns them.
func leafUsers(name string, users ...string) map[string]any {
	us := make([]any, len(users))
	for i, u := range users {
		us[i] = u
	}
	return map[string]any{"name": name, "leaf": map[string]any{"users": map[string]any{"users": us}}}
}

func leafComputed(name, userset string) map[string]any {
	return map[string]any{"name": name, "leaf": map[string]any{"computed": map[string]any{"userset": userset}}}
}

func leafTTU(name, tupleset string, computed ...string) map[string]any {
	cs := make([]any, len(computed))
	for i, c := range computed {
		cs[i] = map[string]any{"userset": c}
	}
	return map[string]any{"name": name, "leaf": map[string]any{
		"tupleToUserset": map[string]any{"tupleset": tupleset, "computed": cs}}}
}

func TestParseResolutionUnion(t *testing.T) {
	// document:roadmap#viewer := [user] or owner
	tree := map[string]any{"root": map[string]any{
		"name": "document:roadmap#viewer",
		"union": map[string]any{"nodes": []any{
			leafUsers("document:roadmap#viewer", "user:anne", "team:eng#member"),
			leafComputed("document:roadmap#viewer", "document:roadmap#owner"),
		}},
	}}
	root, ok := ParseResolution(tree)
	if !ok {
		t.Fatal("expected a root node")
	}
	if root.Name != "document:roadmap#viewer" || root.Op != ResUnion {
		t.Fatalf("root = %q op=%d, want document:roadmap#viewer / union", root.Name, root.Op)
	}
	if len(root.Children) != 2 {
		t.Fatalf("want 2 children, got %d", len(root.Children))
	}
	if c := root.Children[0]; c.Op != ResLeaf || len(c.Users) != 2 || c.Users[0] != "user:anne" || c.Users[1] != "team:eng#member" {
		t.Fatalf("child 0 = %+v, want a users leaf [user:anne team:eng#member]", c)
	}
	if c := root.Children[1]; c.Op != ResLeaf || c.Computed != "document:roadmap#owner" {
		t.Fatalf("child 1 = %+v, want a computed leaf -> owner", c)
	}
}

func TestParseResolutionTupleToUserset(t *testing.T) {
	tree := map[string]any{"root": leafTTU("repo:x#reader", "repo:x#owner", "organization:acme#repo_reader")}
	root, ok := ParseResolution(tree)
	if !ok {
		t.Fatal("expected a root node")
	}
	if root.Op != ResLeaf || root.TTUFrom != "repo:x#owner" || len(root.TTUTo) != 1 || root.TTUTo[0] != "organization:acme#repo_reader" {
		t.Fatalf("root = %+v, want a tuple-to-userset leaf", root)
	}
}

func TestParseResolutionDifference(t *testing.T) {
	tree := map[string]any{"root": map[string]any{
		"name": "document:x#viewer",
		"difference": map[string]any{
			"base":     leafUsers("document:x#viewer", "user:anne"),
			"subtract": leafUsers("document:x#blocked", "user:anne"),
		},
	}}
	root, _ := ParseResolution(tree)
	if root.Op != ResExclusion || len(root.Children) != 2 {
		t.Fatalf("root = %+v, want an exclusion with base+subtract", root)
	}
}

func TestParseResolutionNoRoot(t *testing.T) {
	if _, ok := ParseResolution(map[string]any{}); ok {
		t.Fatal("empty tree should have no root")
	}
}

func TestMarkGrantedUnion(t *testing.T) {
	// viewer := [user] or owner ; user granted via owner (computed), not direct.
	tree := map[string]any{"root": map[string]any{
		"name": "document:x#viewer",
		"union": map[string]any{"nodes": []any{
			leafUsers("document:x#viewer", "user:bob"),
			leafComputed("document:x#viewer", "document:x#owner"),
		}},
	}}
	root, _ := ParseResolution(tree)
	// anne is an owner, not a direct viewer.
	MarkGranted(root, "user:anne", GrantResolver{Check: func(u, rel, obj string) bool {
		return u == "user:anne" && rel == "owner" && obj == "document:x"
	}})
	if !root.Granted {
		t.Fatal("root should be granted (via the owner branch)")
	}
	if root.Children[0].Granted {
		t.Fatal("direct-users leaf should NOT grant anne")
	}
	if !root.Children[1].Granted {
		t.Fatal("computed(owner) leaf should grant anne")
	}
}

func TestBuildDisplayDirectLeaf(t *testing.T) {
	// owner := [user:alice] as the resolution root should render as the chain
	// object -> relation -> Direct Users -> user.
	root, _ := ParseResolution(map[string]any{"root": leafUsers("document:roadmap#owner", "user:alice")})
	MarkGranted(root, "user:alice", GrantResolver{})

	d := buildDisplay(root, "user:alice", "document:roadmap", "owner")
	if d.kind != dispObject || d.label != "document:roadmap" || !d.granted || len(d.kids) != 1 {
		t.Fatalf("root should be the granted object node with one child, got %+v", d)
	}
	rel := d.kids[0]
	if rel.kind != dispRelation || rel.label != "document:roadmap#owner" || rel.edge != "owner from" {
		t.Fatalf("expected the owner relation box with an 'owner from' edge, got %+v", rel)
	}
	if len(rel.kids) != 1 || rel.kids[0].kind != dispGroup || rel.kids[0].label != "Direct Users" {
		t.Fatalf("relation should carry a Direct Users group, got %+v", rel.kids)
	}
	grp := rel.kids[0]
	if len(grp.kids) != 1 || grp.kids[0].kind != dispUser || grp.kids[0].label != "user:alice" || !grp.kids[0].granted {
		t.Fatalf("group should hold the granted user:alice, got %+v", grp.kids)
	}
}

func TestBuildDisplayComputedFoldsIntoRelation(t *testing.T) {
	// viewer resolves through owner; owner's own expansion is a union named the
	// same. The computed leaf must fold into that box, not stack two #owner boxes.
	viewer, _ := ParseResolution(map[string]any{"root": map[string]any{
		"name":  "document:roadmap#viewer",
		"union": map[string]any{"nodes": []any{leafComputed("document:roadmap#viewer", "document:roadmap#owner")}},
	}})
	ownerSub, _ := ParseResolution(map[string]any{"root": map[string]any{
		"name":  "document:roadmap#owner",
		"union": map[string]any{"nodes": []any{leafUsers("document:roadmap#owner", "user:alice")}},
	}})
	viewer.Children[0].Children = []*ResNode{ownerSub} // simulate ExpandTree splicing owner in

	d := buildDisplay(viewer, "user:alice", "document:roadmap", "viewer")
	rel := d.kids[0] // document:roadmap#viewer
	if len(rel.kids) != 1 {
		t.Fatalf("viewer should have one branch, got %d", len(rel.kids))
	}
	owner := rel.kids[0]
	if owner.kind != dispRelation || owner.label != "document:roadmap#owner" {
		t.Fatalf("branch should be the owner relation box, got %+v", owner)
	}
	if len(owner.kids) == 1 && owner.kids[0].kind == dispRelation && owner.kids[0].label == owner.label {
		t.Fatal("owner relation must not be doubled into an identical box-in-box")
	}
}

func TestRenderResolutionRendersChain(t *testing.T) {
	root, _ := ParseResolution(map[string]any{"root": leafUsers("document:roadmap#owner", "user:alice")})
	MarkGranted(root, "user:alice", GrantResolver{})
	out := ansi.Strip(RenderResolution(root, "user:alice", "document:roadmap", "owner"))
	for _, want := range []string{"document:roadmap", "owner from", "document:roadmap#owner", "Direct Users", "user:alice"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered resolution missing %q\n%s", want, out)
		}
	}
}

func TestExpandTreeComputed(t *testing.T) {
	// document:roadmap#viewer := [user] or owner ; owner resolves to [user:alice].
	// Expand returns owner as a dead-end leaf; ExpandTree must splice its subtree in.
	root, _ := ParseResolution(map[string]any{"root": map[string]any{
		"name": "document:roadmap#viewer",
		"union": map[string]any{"nodes": []any{
			leafUsers("document:roadmap#viewer"), // no direct viewers
			leafComputed("document:roadmap#viewer", "document:roadmap#owner"),
		}},
	}})
	expand := func(obj, rel string) *ResNode {
		if obj == "document:roadmap" && rel == "owner" {
			r, _ := ParseResolution(map[string]any{"root": leafUsers("document:roadmap#owner", "user:alice")})
			return r
		}
		return nil
	}
	ExpandTree(root, "document:roadmap#viewer", expand, nil, 8, 64)

	owner := root.Children[1]
	if owner.Computed != "document:roadmap#owner" || len(owner.Children) != 1 {
		t.Fatalf("computed owner leaf should gain 1 expanded child, got %+v", owner)
	}
	if sub := owner.Children[0]; sub.Op != ResLeaf || len(sub.Users) != 1 || sub.Users[0] != "user:alice" {
		t.Fatalf("owner expansion = %+v, want users leaf [user:alice]", sub)
	}

	// The resolver confirms only the genuine membership (alice is owner of
	// document:roadmap). This proves the grant flows through the spliced owner
	// subtree — the empty direct-viewers branch and the computed(owner) leaf's
	// own Check never produce it. With CheckDirectLeaves set, direct leaves are
	// condition-aware (they defer to this resolver), so an always-deny resolver
	// would (correctly) deny even a real membership; we assert the resolved-true
	// path instead.
	MarkGranted(root, "user:alice", GrantResolver{CheckDirectLeaves: true, Check: func(u, rel, obj string) bool {
		return u == "user:alice" && rel == "owner" && obj == "document:roadmap"
	}})
	if !root.Granted || !owner.Granted || !owner.Children[0].Granted {
		t.Fatalf("nested owner->user:alice path should grant: root=%v owner=%v sub=%v",
			root.Granted, owner.Granted, owner.Children[0].Granted)
	}
	if root.Children[0].Granted {
		t.Fatal("empty direct-users branch must not grant")
	}
}

func TestExpandTreeReportsTruncation(t *testing.T) {
	// A viewer union with a computed(owner) leaf that could expand further.
	newRoot := func() *ResNode {
		root, _ := ParseResolution(map[string]any{"root": map[string]any{
			"name": "document:roadmap#viewer",
			"union": map[string]any{"nodes": []any{
				leafUsers("document:roadmap#viewer"),
				leafComputed("document:roadmap#viewer", "document:roadmap#owner"),
			}},
		}})
		return root
	}
	expand := func(obj, rel string) *ResNode {
		if obj == "document:roadmap" && rel == "owner" {
			r, _ := ParseResolution(map[string]any{"root": leafUsers("document:roadmap#owner", "user:alice")})
			return r
		}
		return nil
	}

	// depth 0: the computed(owner) arm can't be expanded → truncated.
	if !ExpandTree(newRoot(), "document:roadmap#viewer", expand, nil, 0, 64) {
		t.Error("ExpandTree should report truncation when depth is exhausted on an expandable arm")
	}
	// budget 0: same — no node budget to expand the arm.
	if !ExpandTree(newRoot(), "document:roadmap#viewer", expand, nil, 8, 0) {
		t.Error("ExpandTree should report truncation when the node budget is exhausted")
	}
	// ample depth+budget: fully expanded (owner resolves to a users leaf that
	// needs no further expansion) → not truncated.
	if ExpandTree(newRoot(), "document:roadmap#viewer", expand, nil, 8, 64) {
		t.Error("ExpandTree should NOT report truncation when the tree fully expands")
	}
}

func TestExpandTreeTupleToUserset(t *testing.T) {
	// repo:x#reader := reader from owner ; repo:x owner org:acme ; org:acme#reader := [user:anne].
	root, _ := ParseResolution(map[string]any{"root": leafTTU("repo:x#reader", "repo:x#owner", "organization#reader")})
	expand := func(obj, rel string) *ResNode {
		if obj == "org:acme" && rel == "reader" {
			r, _ := ParseResolution(map[string]any{"root": leafUsers("org:acme#reader", "user:anne")})
			return r
		}
		return nil
	}
	tupleset := func(object, relation string) []string {
		if object == "repo:x" && relation == "owner" {
			return []string{"org:acme"}
		}
		return nil
	}
	ExpandTree(root, "repo:x#reader", expand, tupleset, 8, 64)
	if len(root.Children) != 1 {
		t.Fatalf("TTU leaf should expand to 1 child, got %d", len(root.Children))
	}
	if sub := root.Children[0]; sub.Op != ResLeaf || len(sub.Users) != 1 || sub.Users[0] != "user:anne" {
		t.Fatalf("TTU expansion = %+v, want users leaf [user:anne]", sub)
	}
}

func TestExpandTreeCycleTerminates(t *testing.T) {
	// folder:x#viewer := owner ; owner := viewer — a cycle that must not loop.
	root, _ := ParseResolution(map[string]any{"root": leafComputed("folder:x#viewer", "folder:x#owner")})
	calls := 0
	expand := func(obj, rel string) *ResNode {
		if calls++; calls > 50 {
			t.Fatal("ExpandTree failed to terminate on a cyclic model")
		}
		switch rel {
		case "owner":
			r, _ := ParseResolution(map[string]any{"root": leafComputed("folder:x#owner", "folder:x#viewer")})
			return r
		default: // viewer
			r, _ := ParseResolution(map[string]any{"root": leafComputed("folder:x#viewer", "folder:x#owner")})
			return r
		}
	}
	ExpandTree(root, "folder:x#viewer", expand, nil, 8, 64)
	// The cycle guard stops re-expanding folder:x#viewer/owner once seen on the path.
	if calls > 5 {
		t.Errorf("cycle guard should stop quickly; expand called %d times", calls)
	}
}

func TestMarkGrantedTupleToUserset(t *testing.T) {
	// repo:x#reader granted via "reader from owner": repo:x owner organization:acme,
	// and anne has repo_reader on organization:acme.
	tree := map[string]any{"root": leafTTU("repo:x#reader", "repo:x#owner", "organization#repo_reader")}
	root, _ := ParseResolution(tree)
	MarkGranted(root, "user:anne", GrantResolver{
		Check: func(u, rel, obj string) bool {
			return u == "user:anne" && rel == "repo_reader" && obj == "organization:acme"
		},
		Tupleset: func(object, relation string) []string {
			if object == "repo:x" && relation == "owner" {
				return []string{"organization:acme"}
			}
			return nil
		},
	})
	if !root.Granted {
		t.Fatal("TTU leaf should grant anne via organization:acme#repo_reader")
	}
}

func TestLeafGrantsDirectMembershipWithoutFlag(t *testing.T) {
	// A direct-user leaf with CheckDirectLeaves:false must grant by membership
	// even when a provided Check would deny — the flag gates the deferral, so a
	// context-free caller (e.g. the playground) keeps cheap membership behavior.
	root, _ := ParseResolution(map[string]any{"root": leafUsers("document:x#owner", "user:alice")})
	denied := MarkGranted(root, "user:alice", GrantResolver{Check: func(u, rel, obj string) bool {
		return false // an always-deny Check that must be ignored without the flag
	}})
	if !denied || !root.Granted {
		t.Fatal("direct membership must grant when CheckDirectLeaves is false, regardless of Check")
	}

	// With the flag set, the same always-deny Check now gates the leaf.
	root2, _ := ParseResolution(map[string]any{"root": leafUsers("document:x#owner", "user:alice")})
	granted := MarkGranted(root2, "user:alice", GrantResolver{CheckDirectLeaves: true, Check: func(u, rel, obj string) bool {
		return false
	}})
	if granted || root2.Granted {
		t.Fatal("with CheckDirectLeaves true, an always-deny Check must deny the direct leaf")
	}
}

func TestLeafGrantsUsersetValueResolvesArm(t *testing.T) {
	// document:budget#org_suspended := [organization#member], assigned to the
	// members of organization:acme (a userset value, not a literal user). The
	// queried user:carol is not listed literally, but IS a member of acme, so a
	// context-aware resolver must resolve the arm and grant the leaf — this is
	// what makes an intermediate exclusion/intersection node match the engine.
	root, _ := ParseResolution(map[string]any{"root": leafUsers("document:budget#org_suspended", "organization:acme#member")})
	granted := MarkGranted(root, "user:carol", GrantResolver{CheckDirectLeaves: true, Check: func(u, rel, obj string) bool {
		return u == "user:carol" && rel == "member" && obj == "organization:acme"
	}})
	if !granted || !root.Granted {
		t.Fatal("userset value organization:acme#member must grant a member (user:carol) under CheckDirectLeaves")
	}

	// The playground default (CheckDirectLeaves false) must NOT issue a per-leaf
	// Check for a userset value: it keeps cheap literal-only matching, so a
	// non-literal user does not grant even though the (unused) Check would say so.
	root2, _ := ParseResolution(map[string]any{"root": leafUsers("document:budget#org_suspended", "organization:acme#member")})
	calls := 0
	if MarkGranted(root2, "user:carol", GrantResolver{Check: func(u, rel, obj string) bool {
		calls++
		return true
	}}) {
		t.Fatal("without CheckDirectLeaves a userset value must not grant a non-literal user")
	}
	if calls != 0 {
		t.Fatalf("playground default must not Check userset arms per-leaf, got %d calls", calls)
	}
}

func TestLeafGrantsTypedPublicWildcard(t *testing.T) {
	// document:1#viewer := [user:*] — a typed public wildcard. A concrete query
	// for user:anne is not listed literally but IS admitted by the wildcard, so
	// leafGrants must grant it (matching the engine and narrator.isDirectMember).
	// Previously the literal check missed "user:*" and splitUserset("user:*")
	// was skipped, so the leaf wrongly denied.
	root, _ := ParseResolution(map[string]any{"root": leafUsers("document:1#viewer", "user:*")})
	if !MarkGranted(root, "user:anne", GrantResolver{}) || !root.Granted {
		t.Fatal("typed public wildcard user:* must grant a concrete user:anne")
	}

	// A different type's user is NOT admitted by user:* — the wildcard is typed.
	root2, _ := ParseResolution(map[string]any{"root": leafUsers("document:1#viewer", "user:*")})
	if MarkGranted(root2, "team:eng", GrantResolver{}) || root2.Granted {
		t.Fatal("user:* must not grant a differently-typed subject (team:eng)")
	}
}

func TestMarkGrantedExclusionWithUsersetSubtrahend(t *testing.T) {
	// can_delete := owner but not org_suspended, where org_suspended resolves to
	// the members of organization:acme. carol is an owner AND an acme member, so
	// the subtrahend is TRUE and the exclusion must deny — the flagship case the
	// resolution tree previously got wrong (subtrahend left unresolved → false →
	// exclusion wrongly true).
	tree := map[string]any{"root": map[string]any{
		"name": "document:budget#can_delete",
		"difference": map[string]any{
			"base":     leafUsers("document:budget#owner", "user:carol"),
			"subtract": leafUsers("document:budget#org_suspended", "organization:acme#member"),
		},
	}}
	root, _ := ParseResolution(tree)
	acmeMember := func(u, rel, obj string) bool {
		return u == "user:carol" && rel == "member" && obj == "organization:acme"
	}
	if MarkGranted(root, "user:carol", GrantResolver{CheckDirectLeaves: true, Check: acmeMember}) {
		t.Fatal("exclusion must deny: carol is a member of the suspended org")
	}
	if sub := root.Children[1]; !sub.Granted {
		t.Fatal("the userset subtrahend node must resolve TRUE for the suspended member")
	}
}

func TestLeafGrantsNilCheckDoesNotPanic(t *testing.T) {
	// A tree carrying a computed userset and a tuple-to-userset leaf, marked with
	// a zero-value GrantResolver (nil Check, nil Tupleset, CheckDirectLeaves
	// false), must not panic on any branch and must fall back to membership /
	// not-granted semantics.
	root, _ := ParseResolution(map[string]any{"root": map[string]any{
		"name": "document:x#viewer",
		"union": map[string]any{"nodes": []any{
			leafUsers("document:x#viewer", "user:alice"),
			leafComputed("document:x#viewer", "document:x#owner"),
			leafTTU("document:x#viewer", "document:x#parent", "folder#viewer"),
		}},
	}})

	granted := MarkGranted(root, "user:alice", GrantResolver{}) // must not panic
	if !granted || !root.Granted {
		t.Fatal("direct membership (user:alice) should grant even with a nil-Check resolver")
	}
	if !root.Children[0].Granted {
		t.Fatal("direct-users leaf should grant by membership")
	}
	if root.Children[1].Granted {
		t.Fatal("computed leaf must not grant when Check is nil")
	}
	if root.Children[2].Granted {
		t.Fatal("tuple-to-userset leaf must not grant when Check is nil")
	}

	// And a user with no direct membership resolves to not-granted, not a panic.
	root2, _ := ParseResolution(map[string]any{"root": map[string]any{
		"name": "document:x#viewer",
		"union": map[string]any{"nodes": []any{
			leafComputed("document:x#viewer", "document:x#owner"),
			leafTTU("document:x#viewer", "document:x#parent", "folder#viewer"),
		}},
	}})
	if MarkGranted(root2, "user:bob", GrantResolver{}) {
		t.Fatal("no membership + nil Check should deny, not panic or grant")
	}
}

func TestRenderResolutionSanitizesTerminalControls(t *testing.T) {
	attack := "\x1b]52;c;YXR0YWNr\x07"
	root := &ResNode{Name: "viewer" + attack, Users: []string{"user:anne" + attack}}
	got := RenderResolution(root, "user:anne"+attack, "document:1"+attack, "viewer"+attack)
	plain := ansi.Strip(got)
	if strings.Contains(got, attack) || strings.ContainsAny(plain, "\x1b\x07") {
		t.Fatalf("resolution retained terminal controls: %q", got)
	}
}
