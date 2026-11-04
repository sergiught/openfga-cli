package bench

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func loadRef(t *testing.T) *openfgav1.AuthorizationModel {
	t.Helper()
	data, err := os.ReadFile("../testdata/reference.fga")
	if err != nil {
		t.Fatal(err)
	}
	lm, err := modeltest.LoadModelBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	return lm.Proto
}

func tk(user, relation, object string) *openfgav1.TupleKey {
	return &openfgav1.TupleKey{User: user, Relation: relation, Object: object}
}

func hasTuple(ts []*openfgav1.TupleKey, want *openfgav1.TupleKey) bool {
	return slices.ContainsFunc(ts, func(g *openfgav1.TupleKey) bool {
		return g.GetUser() == want.GetUser() && g.GetRelation() == want.GetRelation() && g.GetObject() == want.GetObject()
	})
}

func TestSynthesize_ReferenceModelIsDeterministic(t *testing.T) {
	ref := loadRef(t)
	a, err := Synthesize(ref, SynthOptions{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Synthesize(ref, SynthOptions{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Tuples) != len(b.Tuples) {
		t.Fatalf("tuple counts differ: %d vs %d", len(a.Tuples), len(b.Tuples))
	}
	for i := range a.Tuples {
		if a.Tuples[i].String() != b.Tuples[i].String() {
			t.Fatalf("tuple %d differs: %v vs %v", i, a.Tuples[i], b.Tuples[i])
		}
	}
	if !reflect.DeepEqual(a.Checks, b.Checks) {
		t.Fatal("checks differ across runs")
	}
	for _, w := range []*openfgav1.TupleKey{
		tk("folder:chain6", "parent", "folder:chain5"),
		tk("folder:chain1", "parent", "folder:t1"),
		tk("group:chain3#member", "member", "group:chain2"),
		tk("group:chain1#member", "member", "group:t1"),
	} {
		if !hasTuple(a.Tuples, w) {
			t.Errorf("missing tuple %v", w)
		}
	}
	want := Check{Name: "deny document#can_view", User: "user:nobody", Relation: "can_view", Object: "document:t1"}
	if !slices.Contains(a.Checks, want) {
		t.Errorf("missing check %+v in %+v", want, a.Checks)
	}
}

func TestSynthesize_SkipsWildcardAndConditioned(t *testing.T) {
	data := []byte(`model
  schema 1.1
type user
type doc
  relations
    define viewer: [user:*]
    define owner: [user with cond]
    define editor: [user, user with cond]
condition cond(x: string) {
  x == "a"
}
`)
	lm, err := modeltest.LoadModelBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	src, err := Synthesize(lm.Proto, SynthOptions{Noise: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Skipped) != 2 {
		t.Fatalf("Skipped = %v, want 2 entries", src.Skipped)
	}
	for _, tu := range src.Tuples {
		if tu.GetRelation() != "editor" {
			t.Errorf("unexpected tuple %v", tu)
		}
	}
}

func TestSynthesize_DenyChecksUseModelSubjectType(t *testing.T) {
	lm, err := modeltest.LoadModelBytes([]byte(`model
  schema 1.1
type employee
type contractor
type document
  relations
    define viewer: [employee]
    define editor: [employee, contractor]
`))
	if err != nil {
		t.Fatal(err)
	}
	src, err := Synthesize(lm.Proto, SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"deny document#viewer":              "employee:nobody",
		"deny document#editor (contractor)": "contractor:nobody",
		"deny document#editor (employee)":   "employee:nobody",
	}
	got := map[string]string{}
	for _, c := range src.Checks {
		if len(c.Name) > 4 && c.Name[:4] == "deny" && !strings.Contains(c.User, "#") {
			got[c.Name] = c.User
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deny checks = %v, want %v", got, want)
	}
}

func TestSynthesize_ProbesCoverIndirectPaths(t *testing.T) {
	src, err := Synthesize(loadRef(t), SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := Check{
		Name: "probe user:folder_viewer_1 can_view document:t1", User: "user:folder_viewer_1", Relation: "can_view", Object: "document:t1",
	}
	if !slices.Contains(src.Probes, want) {
		t.Fatalf("probes lack %+v", want)
	}
	seen := map[Check]bool{}
	for _, p := range src.Probes {
		if seen[p] {
			t.Fatalf("duplicate probe %+v", p)
		}
		seen[p] = true
	}
	again, err := Synthesize(loadRef(t), SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(src.Probes, again.Probes) {
		t.Fatal("probes not deterministic")
	}
}

func TestSynthesize_UsersetSubjectChecks(t *testing.T) {
	src, err := Synthesize(loadRef(t), SynthOptions{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	index := func(name string) int {
		return slices.IndexFunc(src.Checks, func(c Check) bool { return c.Name == name })
	}
	want := []Check{
		{Name: "probe document#can_view (document#viewer)", User: "document:t1#viewer", Relation: "can_view", Object: "document:t1"},
		{Name: "probe folder#viewer (folder#parent)", User: "folder:t1#parent", Relation: "viewer", Object: "folder:t1"},
	}
	for _, w := range want {
		if !slices.Contains(src.Checks, w) {
			t.Errorf("missing check %+v", w)
		}
	}
	for _, c := range src.Checks {
		if strings.HasPrefix(c.User, "group:t1#") {
			t.Errorf("group has one relation, unexpected userset check %+v", c)
		}
	}
	us := index(want[0].Name)
	lastDeny, firstAllow := -1, -1
	for i, c := range src.Checks {
		if c.Relation != "can_view" || c.Object != "document:t1" {
			continue
		}
		if strings.HasPrefix(c.Name, "deny document#can_view") && !strings.Contains(c.User, "#") {
			lastDeny = i
		}
		if strings.HasPrefix(c.Name, "allow document#can_view") && firstAllow < 0 {
			firstAllow = i
		}
	}
	deny, allow := lastDeny, firstAllow
	if us < 0 || deny < 0 || us < deny {
		t.Errorf("userset check at %d precedes deny at %d", us, deny)
	}
	if allow >= 0 && us > allow {
		t.Errorf("userset check at %d follows allow at %d", us, allow)
	}
}

func TestSynthesize_UsersetChecksAreProbes(t *testing.T) {
	src, err := Synthesize(loadRef(t), SynthOptions{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range src.Checks {
		if strings.Contains(c.User, "#") {
			found = true
			if !strings.HasPrefix(c.Name, "probe ") {
				t.Errorf("userset check %q should be named probe ...", c.Name)
			}
		}
	}
	if !found {
		t.Fatal("no userset-subject checks synthesized")
	}
}

// TestSynthesize_RelationsWithoutMetadata pins that a JSON model omitting the
// metadata of computed-only relations synthesizes the same checks and probes.
func TestSynthesize_RelationsWithoutMetadata(t *testing.T) {
	want, err := Synthesize(loadRef(t), SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := loadRef(t)
	stripped := 0
	for _, td := range m.GetTypeDefinitions() {
		for rel, meta := range td.GetMetadata().GetRelations() {
			if len(meta.GetDirectlyRelatedUserTypes()) == 0 {
				delete(td.GetMetadata().GetRelations(), rel)
				stripped++
			}
		}
	}
	if stripped == 0 {
		t.Fatal("reference model has no computed-only relation to strip")
	}
	got, err := Synthesize(m, SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Checks, want.Checks) {
		t.Errorf("checks differ without metadata:\n got %+v\nwant %+v", got.Checks, want.Checks)
	}
	if !reflect.DeepEqual(got.Probes, want.Probes) {
		t.Errorf("probes differ without metadata: got %d, want %d", len(got.Probes), len(want.Probes))
	}
}

// TestSynthesize_ReportsUnqueriedRelations pins that a model whose only
// subject type defines relations yields a notice per unqueried relation
// rather than a silently empty benchmark.
func TestSynthesize_ReportsUnqueriedRelations(t *testing.T) {
	lm, err := modeltest.LoadModelBytes([]byte("model\n  schema 1.1\ntype account\n  relations\n    define follower: [account]\n"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := Synthesize(lm.Proto, SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Checks)+len(src.Probes) != 0 {
		t.Fatalf("checks %v probes %v; the test model should yield none", src.Checks, src.Probes)
	}
	if want := "account#follower: no synthesized check or probe queries it"; !slices.Contains(src.Skipped, want) {
		t.Errorf("skipped = %v, want it to contain %q", src.Skipped, want)
	}
}

func TestSynthesize_QueriedRelationsAreNotReported(t *testing.T) {
	src, err := Synthesize(loadRef(t), SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range src.Skipped {
		if strings.Contains(s, "no synthesized check") {
			t.Errorf("reference model reports %q", s)
		}
	}
}
