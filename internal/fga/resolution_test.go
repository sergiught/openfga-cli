package fga

import "testing"

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
	MarkGranted(root, "user:anne", func(u, rel, obj string) bool {
		return u == "user:anne" && rel == "owner" && obj == "document:x"
	})
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
