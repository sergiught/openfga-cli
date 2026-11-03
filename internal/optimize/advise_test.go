package optimize

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func tierIDs(rep *Report, tier Tier) (out []string) {
	for _, f := range rep.Findings {
		if f.Tier == tier {
			out = append(out, f.ID+" "+f.Type+"#"+f.Relation)
		}
	}
	return out
}

func TestAnalyze_ReferenceModelTiers(t *testing.T) {
	rep, err := Analyze(LoadReferenceModel(t).Proto)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(tierIDs(rep, TierSafe)); n != 4 {
		t.Fatalf("safe findings = %d, want 4", n)
	}
	if got, want := tierIDs(rep, TierCaller), []string{"C2 document#can_edit"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("caller = %v, want %v", got, want)
	}
	want := []string{"T2 document#viewer", "T1 folder#editor", "T1 folder#viewer", "T1 group#member", "T1 organization#member"}
	if got := tierIDs(rep, TierTuple); !reflect.DeepEqual(got, want) {
		t.Fatalf("tuple = %v, want %v", got, want)
	}
	if rep.Rewrite == nil {
		t.Fatal("Rewrite is nil despite safe findings")
	}
	for _, f := range rep.Findings {
		if f.Tier == TierTuple && f.ID == "T1" && !strings.Contains(f.Reason, "+1 datastore read and +1 dispatch per nesting level") {
			t.Fatalf("T1 reason: %q", f.Reason)
		}
	}
}

func TestAnalyze_WeightsStableAndInfiniteSubstituted(t *testing.T) {
	p := LoadReferenceModel(t).Proto
	a, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Findings) != len(b.Findings) {
		t.Fatal("finding counts differ")
	}
	sawInf := false
	for i := range a.Findings {
		if !reflect.DeepEqual(a.Findings[i].Weights, b.Findings[i].Weights) {
			t.Fatalf("weights differ for %s: %v vs %v", a.Findings[i].ID, a.Findings[i].Weights, b.Findings[i].Weights)
		}
		for _, v := range a.Findings[i].Weights {
			if v == -1 {
				sawInf = true
			}
			if v > 1<<20 {
				t.Fatalf("unsubstituted infinite weight in %v", a.Findings[i].Weights)
			}
		}
	}
	if !sawInf {
		t.Fatal("expected a -1 weight for an infinite relation")
	}
}

func TestAnalyze_InvalidModel(t *testing.T) {
	_, err := Analyze(&openfgav1.AuthorizationModel{SchemaVersion: "1.1", TypeDefinitions: []*openfgav1.TypeDefinition{
		{Type: "doc", Relations: map[string]*openfgav1.Userset{
			"r": {Userset: &openfgav1.Userset_ComputedUserset{ComputedUserset: &openfgav1.ObjectRelation{Relation: "missing"}}},
		}},
	}})
	if err == nil {
		t.Fatal("expected error for invalid model")
	}
}

func TestAnalyze_C1RecursionInIntersection(t *testing.T) {
	lm, err := modeltest.LoadModelBytes([]byte(`model
  schema 1.1
type user
type group
  relations
    define member: [user, group#member]
    define a: [user]
    define both: [user, group#member] and a
`))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Analyze(lm.Proto)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tierIDs(rep, TierCaller), []string{"C1 group#both"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("caller = %v, want %v", got, want)
	}
}

func TestAnalyze_NoSafeFindingsNilRewrite(t *testing.T) {
	lm, err := modeltest.LoadModelBytes([]byte("model\n  schema 1.1\ntype user\ntype doc\n  relations\n    define viewer: [user]\n"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Analyze(lm.Proto)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rewrite != nil || len(rep.Findings) != 0 {
		t.Fatalf("got %+v", rep)
	}
}

func analyzeDSL(t *testing.T, dsl string) *Report {
	t.Helper()
	lm, err := modeltest.LoadModelBytes([]byte(dsl))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Analyze(lm.Proto)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestAnalyze_C1AndT1Cases(t *testing.T) {
	const head = "model\n  schema 1.1\ntype user\n"
	tests := []struct {
		name       string
		dsl        string
		caller     []string
		tuple      []string
		wantNoSafe bool
	}{
		{
			name:   "inherited recursion in intersection is not C1",
			dsl:    head + "type group\n  relations\n    define a: [user, group#member]\n    define member: [user, group#member]\n    define b: [user]\n    define x: a and b\n",
			caller: nil,
			tuple:  []string{"T1 group#a", "T1 group#member"},
		},
		{
			name:   "own recursion in exclusion is C1",
			dsl:    head + "type group\n  relations\n    define member: [user, group#member]\n    define b: [user]\n    define x: [user, group#member] but not b\n",
			caller: []string{"C1 group#x", "C2 group#x"},
			tuple:  []string{"T1 group#member", "T1 group#x"},
		},
		{
			name:   "non-recursive userset and cross-type TTU are not T1",
			dsl:    head + "type team\n  relations\n    define member: [user]\ntype folder\n  relations\n    define viewer: [user]\ntype document\n  relations\n    define parent: [folder]\n    define viewer: [team#member] or viewer from parent\n",
			caller: nil,
			tuple:  nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := analyzeDSL(t, tc.dsl)
			if got := tierIDs(rep, TierCaller); !reflect.DeepEqual(got, tc.caller) {
				t.Fatalf("caller = %v, want %v", got, tc.caller)
			}
			if got := tierIDs(rep, TierTuple); !reflect.DeepEqual(got, tc.tuple) {
				t.Fatalf("tuple = %v, want %v", got, tc.tuple)
			}
		})
	}
}

func TestAnalyze_T2Boundary(t *testing.T) {
	const head = "model\n  schema 1.1\ntype user\ntype doc\n  relations\n    define a: [user]\n    define b: [user]\n    define c: [user]\n    define d: [user]\n"
	if got := tierIDs(analyzeDSL(t, head+"    define v: a or b or c\n"), TierTuple); got != nil {
		t.Fatalf("3 operands: tuple = %v, want none", got)
	}
	got := tierIDs(analyzeDSL(t, head+"    define v: a or b or c or d\n"), TierTuple)
	if want := []string{"T2 doc#v"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("4 operands: tuple = %v, want %v", got, want)
	}
}

// TestAnalyze_AdvisesWithoutWeightedGraph has an AND on a tuple cycle, so
// OpenFGA cannot build the weighted graph; advisories must still be reported,
// with recursion taken from the dependency graph.
func TestAnalyze_AdvisesWithoutWeightedGraph(t *testing.T) {
	m := loadInline(t, `model
  schema 1.1

type user

type folder
  relations
    define parent: [folder]
    define allowed: [user]
    define direct: [user]
    define viewer: allowed and (direct or viewer from parent)
`)
	if weightedGraphBuilds(m) {
		t.Fatal("weighted graph builds; the test needs a model where it does not")
	}
	rep, err := Analyze(m.Proto)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tierIDs(rep, TierCaller), []string{"C1 folder#viewer"}; !reflect.DeepEqual(got, want) {
		t.Errorf("caller = %v, want %v", got, want)
	}
	if got, want := tierIDs(rep, TierTuple), []string{"T1 folder#viewer"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tuple = %v, want %v", got, want)
	}
}

// TestAnalyze_FallbackInheritsInfinity: doc#viewer only reaches the recursive
// group#member, so its weight is infinite (inherited) on the weighted path and
// its userset direct type makes it T1. Without a weighted graph (team's AND on
// a tuple cycle) the fallback must report the same.
func TestAnalyze_FallbackInheritsInfinity(t *testing.T) {
	const base = `model
  schema 1.1

type user

type group
  relations
    define member: [user, group#member]

type doc
  relations
    define viewer: [group#member]
`
	const noGraph = `
type team
  relations
    define parent: [team]
    define member: [user] or both from parent
    define wide: [user] or member
    define both: member and wide
`
	if !weightedGraphBuilds(loadInline(t, base)) || weightedGraphBuilds(loadInline(t, base+noGraph)) {
		t.Fatal("test needs base to build a weighted graph and base+noGraph not to")
	}
	want := tierIDs(analyzeDSL(t, base), TierTuple)
	if !slices.Contains(want, "T1 doc#viewer") {
		t.Fatalf("weighted path: tuple = %v, want T1 doc#viewer", want)
	}
	var got []string
	for _, id := range tierIDs(analyzeDSL(t, base+noGraph), TierTuple) {
		if !strings.Contains(id, " team#") {
			got = append(got, id)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("fallback: tuple = %v, want %v", got, want)
	}
}
