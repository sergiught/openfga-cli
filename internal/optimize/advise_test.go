package optimize

import (
	"reflect"
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
	if n := len(tierIDs(rep, TierSafe)); n != 5 {
		t.Fatalf("safe findings = %d, want 5", n)
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
