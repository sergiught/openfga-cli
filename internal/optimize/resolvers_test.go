package optimize

import (
	"strings"
	"testing"
)

const class2DSL = `model
  schema 1.1
type user
type folder
  relations
    define parent: [folder]
    define viewer: [user with cx, user:*]
    define can_view: viewer from parent or can_view from parent or viewer
condition cx(x: int) {
  x < 10
}
`

func TestResolverProfile_Class2SwitchesTTURecursive(t *testing.T) {
	before, ok := resolverProfile(loadInline(t, class2DSL))
	if !ok {
		t.Fatal("before: profile not ok")
	}
	after, ok := resolverProfile(loadInline(t, strings.Replace(class2DSL, "viewer from parent or ", "", 1)))
	if !ok {
		t.Fatal("after: profile not ok")
	}
	const key = "folder#can_view|user|ttu-recursive|parent→can_view"
	if before[key] {
		t.Errorf("before[%s] = true, want false", key)
	}
	if !after[key] {
		t.Errorf("after[%s] = false, want true", key)
	}
	if sameResolvers(before, after) {
		t.Error("sameResolvers = true, want false")
	}
}

func TestResolverProfile_ReferenceModelUnchangedByRewrite(t *testing.T) {
	rw, err := Rewrite(LoadReferenceModel(t))
	if err != nil {
		t.Fatal(err)
	}
	before, ok1 := resolverProfile(rw.Before)
	after, ok2 := resolverProfile(rw.After)
	if !ok1 || !ok2 {
		t.Fatalf("profiles not ok: %v %v", ok1, ok2)
	}
	if len(before) != 32 {
		t.Errorf("len(before) = %d, want 32", len(before))
	}
	if !sameResolvers(before, after) {
		t.Error("reference rewrite changed the resolver profile")
	}
}

func TestResolverProfile_NoGraphReportsFalse(t *testing.T) {
	m := loadInline(t, hdr+"    define parent: [doc]\n    define member: [user] or both from parent\n    define wide: [user] or member\n    define both: member and wide\n")
	got, ok := resolverProfile(m)
	if ok || got != nil {
		t.Errorf("resolverProfile = (%v, %v), want (nil, false)", got, ok)
	}
}

func TestSameResolvers_KeyOnlyInAfterIsAChange(t *testing.T) {
	if sameResolvers(map[string]bool{"a": true}, map[string]bool{"a": true, "b": false}) {
		t.Error("key only in after must be a change")
	}
	if !sameResolvers(map[string]bool{"a": true, "b": false}, map[string]bool{"a": true}) {
		t.Error("key only in before must not be a change")
	}
}

// T#r is an intersection whose second operand lacks user type "emp", so the
// object relation has no weight for emp while the userset node U#s does.
const usersetOnlyDSL = `model
  schema 1.1
type user
type emp
type U
  relations
    define s: [user, emp]
type T
  relations
    define o: [user]
    define r: [U#s] and o
`

func TestResolverProfile_UsersetWeight2CoversUsersetOnlyUserType(t *testing.T) {
	m := loadInline(t, usersetOnlyDSL)
	w, err := Weights(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w["T#r"]["emp"]; ok {
		t.Fatalf("precondition: T#r unexpectedly has weight for emp: %v", w["T#r"])
	}
	p, ok := resolverProfile(m)
	if !ok {
		t.Fatal("profile not ok")
	}
	const key = "T#r|emp|userset-weight2|U#s"
	if _, ok := p[key]; !ok {
		t.Errorf("profile lacks %s; keys: %v", key, p)
	}
}
