package optimize

import (
	"strings"
	"testing"
)

func findingsFor(t *testing.T, rule rule, dsl string) []Finding {
	t.Helper()
	m := loadInline(t, dsl)
	return rule(m, BuildLattice(m))
}

const hdr = "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n"

func TestS1_SkipsSubtractedSide(t *testing.T) {
	fs := findingsFor(t, ruleS1, hdr+
		"    define a: [user]\n    define b: [user] or a\n    define c: [user]\n    define v: c but not (a or b)\n")
	for _, f := range fs {
		if f.Relation == "v" {
			t.Fatalf("unexpected finding on subtracted side: %+v", f)
		}
	}
}

func TestS1_FiresOnBaseSideOfDifference(t *testing.T) {
	fs := findingsFor(t, ruleS1, hdr+
		"    define a: [user]\n    define b: [user] or a\n    define c: [user]\n    define v: (a or b) but not c\n")
	var got *Finding
	for i := range fs {
		if fs[i].Relation == "v" {
			got = &fs[i]
		}
	}
	if got == nil {
		t.Fatal("expected S1 on base side")
	}
	if got.After != "b but not c" {
		t.Fatalf("After = %q", got.After)
	}
}

func TestS1_SkipsInsideIntersection(t *testing.T) {
	fs := findingsFor(t, ruleS1, hdr+
		"    define a: [user]\n    define b: [user] or a\n    define c: [user]\n    define v: (a or b) and c\n")
	for _, f := range fs {
		if f.Relation == "v" {
			t.Fatalf("unexpected finding inside intersection: %+v", f)
		}
	}
}

func TestS1_AliasOperandKeepsTheAlias(t *testing.T) {
	m := loadInline(t, hdr+"    define a: [user]\n    define b: a\n    define v: a or b\n")
	rw, err := Rewrite(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(rw.Applied) != 1 {
		t.Fatalf("applied = %+v, want exactly one", rw.Applied)
	}
	if f := rw.Applied[0]; f.Relation != "v" || f.After != "b" {
		t.Fatalf("got %+v, want v: a or b -> b", f)
	}
}

func TestS1_NeverDropsDirectOperand(t *testing.T) {
	// The direct term [user] of v is only contained in v itself.
	fs := findingsFor(t, ruleS1, hdr+"    define a: [user]\n    define v: [user] or a\n")
	if len(fs) != 0 {
		t.Fatalf("unexpected findings: %+v", fs)
	}
}

func TestS2_TwoOperandOnly(t *testing.T) {
	base := hdr + "    define a: [user]\n    define b: [user] or a\n    define c: [user]\n"
	if fs := findingsFor(t, ruleS2, base+"    define v: a and b and c\n"); len(fs) != 0 {
		t.Fatalf("three-operand intersection: %+v", fs)
	}
	fs := findingsFor(t, ruleS2, base+"    define v: a and b\n")
	if len(fs) != 1 || fs[0].After != "a" || fs[0].Before != "a and b" {
		t.Fatalf("two-operand: %+v", fs)
	}
	// Not the relation's whole definition.
	if fs := findingsFor(t, ruleS2, base+"    define v: (a and b) but not c\n"); len(fs) != 0 {
		t.Fatalf("nested intersection: %+v", fs)
	}
}

func TestS3_DuplicateOperand(t *testing.T) {
	m := loadInline(t, hdr+"    define owner: [user]\n    define v: owner or owner\n")
	fs := ruleS3(m, BuildLattice(m))
	if len(fs) != 1 || fs[0].ID != "S3" || fs[0].After != "owner" {
		t.Fatalf("got %+v", fs)
	}
}

func TestS4_DifferentTuplesetNoFinding(t *testing.T) {
	dsl := "model\n  schema 1.1\n\ntype user\n\ntype folder\n  relations\n    define x: [user]\n    define y: [user] or x\n\n" +
		"type doc\n  relations\n    define parent: [folder]\n    define org: [folder]\n    define v: x from parent or y from org\n"
	if fs := findingsFor(t, ruleS4, dsl); len(fs) != 0 {
		t.Fatalf("unexpected: %+v", fs)
	}
	same := strings.Replace(dsl, "y from org", "y from parent", 1)
	fs := findingsFor(t, ruleS4, same)
	if len(fs) != 1 || fs[0].After != "y from parent" {
		t.Fatalf("same tupleset: %+v", fs)
	}
}
