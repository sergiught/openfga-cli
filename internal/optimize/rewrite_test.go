package optimize

import (
	"slices"
	"strings"
	"testing"
)

func TestRewrite_ReferenceModelAppliesExactlyFive(t *testing.T) {
	rw, err := Rewrite(LoadReferenceModel(t))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rw.Applied))
	for _, f := range rw.Applied {
		got = append(got, f.ID+" "+f.Type+"#"+f.Relation+": "+f.Before+" -> "+f.After)
	}
	want := []string{
		"S1 organization#can_view: member or admin -> member",
		"S1 folder#viewer: [user, group#member] or editor or owner or viewer from parent -> [user, group#member] or editor or viewer from parent",
		"S2 folder#can_delete: owner and editor -> owner",
		"S1 document#viewer: [user] or editor or owner or viewer from parent or member from org -> [user] or editor or viewer from parent or member from org",
		"S4 document#can_view: viewer or can_view from parent -> viewer",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("applied findings:\n got %q\nwant %q", got, want)
	}
	again, err := Rewrite(rw.After)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Applied) != 0 {
		t.Fatalf("not idempotent: %+v", again.Applied)
	}
	dsl, err := rw.After.DSL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dsl, "define can_delete: owner\n") {
		t.Fatalf("missing rewritten can_delete in:\n%s", dsl)
	}
	// Before is untouched.
	orig, _ := rw.Before.DSL()
	if !strings.Contains(orig, "define can_delete: owner and editor") {
		t.Fatalf("Before was mutated:\n%s", orig)
	}
}

func TestRewrite_FixpointCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define r0: [user]\n")
	for i := 1; i <= 12; i++ {
		b.WriteString("    define r" + itoa(i) + ": r" + itoa(i-1) + "\n")
	}
	// Wide union of aliases that are all equivalent: one drop per pass.
	b.WriteString("    define v: r0")
	for i := 1; i <= 12; i++ {
		b.WriteString(" or r" + itoa(i))
	}
	b.WriteString("\n")
	rw, err := Rewrite(loadInline(t, b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(rw.Applied) != 10 {
		t.Fatalf("applied %d findings, want exactly 10 (cap)", len(rw.Applied))
	}
	if _, err := rw.After.DSL(); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}
