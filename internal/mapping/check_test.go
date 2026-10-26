package mapping

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCheckPassesAValidFile(t *testing.T) {
	f, _ := Load("testdata/load/commented.yaml")
	if got := Check(f, nil); len(got) != 0 {
		t.Fatalf("findings: %+v", got)
	}
}

// A model gap is reported at the value that names what the model lacks, not at
// the rule around it.
func TestCheckReportsModelGapsAtTheValue(t *testing.T) {
	f, _ := Load("testdata/load/commented.yaml")
	ix := &ModelIndex{Types: map[string][]string{"user": nil}} // no organization type
	got := Check(f, ix)
	if len(got) == 0 || got[0].Line != 7 || got[0].Column != 17 || !strings.Contains(got[0].Message, "organization") {
		t.Fatalf("findings: %+v", got)
	}
	if got[0].Field != `rules["member-added"]` {
		t.Fatalf("field = %q, want rules[\"member-added\"]", got[0].Field)
	}
}

func TestCheckFlagsAFilterTheReadAPICannotServe(t *testing.T) {
	src := []byte("version: \"1\"\nrules:\n  - name: r\n    when: \"true\"\n    tuple_filters:\n      - relation: member\n        object: \"team:\"\n    tuples:\n      - user: \"user:1\"\n        relation: member\n        object: \"team:1\"\n")
	f, err := LoadBytes("f.yaml", src)
	if err != nil {
		t.Fatal(err)
	}
	got := Check(f, nil)
	if len(got) != 1 || !strings.Contains(got[0].Message, "needs a user") {
		t.Fatalf("findings: %+v", got)
	}
	if got[0].Field != `rules["r"].tuple_filters` {
		t.Fatalf("field = %q, want rules[\"r\"].tuple_filters", got[0].Field)
	}
}

func TestCheckReportsABadExpressionAtItsPosition(t *testing.T) {
	src := []byte("version: \"1\"\nrules:\n  - name: r\n    when: \"input.type ==\"\n    tuples:\n      - user: \"user:1\"\n        relation: member\n        object: \"team:1\"\n")
	f, err := LoadBytes("f.yaml", src)
	if err != nil {
		t.Fatal(err)
	}
	got := Check(f, nil)
	if len(got) != 1 || got[0].Line == 0 {
		t.Fatalf("findings: %+v", got)
	}
}

func TestRuleRefUsesNameOrIndex(t *testing.T) {
	if got := ruleRef(2, Rule{Name: "member-added"}); got != `rules["member-added"]` {
		t.Fatalf("named = %q", got)
	}
	if got := ruleRef(2, Rule{}); got != "rules[2]" {
		t.Fatalf("unnamed = %q", got)
	}
}

func TestCheckIgnoresFiltersThatAreNotLiteralBareTypes(t *testing.T) {
	// team:t1: is one object whose id ends in a colon, not a whole type.
	for _, object := range []string{"{{ input.obj }}", "team:{{ input.x }}", "team:t1", "team:t1:"} {
		src := []byte("version: \"1\"\nrules:\n  - name: r\n    when: \"true\"\n    tuple_filters:\n      - relation: member\n        object: \"" + object + "\"\n    tuples:\n      - user: \"user:1\"\n        relation: member\n        object: \"team:1\"\n")
		f, err := LoadBytes("f.yaml", src)
		if err != nil {
			t.Fatal(err)
		}
		if got := Check(f, nil); len(got) != 0 {
			t.Fatalf("object %q: findings: %+v", object, got)
		}
	}
}

func TestFindingsFromTrimsPositionEcho(t *testing.T) {
	f, err := LoadBytes("f.yaml", []byte("version: \"1\"\nrules: [\n"))
	var le *LoadError
	if !errors.As(err, &le) || f != nil {
		t.Fatalf("want LoadError, got %v", err)
	}
	got := FindingsFrom(le.Diagnostics)
	if len(got) == 0 {
		t.Fatal("no findings")
	}
	for _, g := range got {
		if strings.HasPrefix(g.Message, "[") || strings.Contains(g.Message, "\n") || g.Line == 0 {
			t.Fatalf("finding = %+v", g)
		}
	}
}

func TestCheckFlagsAFilterObjectWithoutAType(t *testing.T) {
	src := []byte("version: \"1\"\nrules:\n  - name: r\n    when: \"true\"\n    tuple_filters:\n      - relation: member\n        object: team\n    tuples:\n      - user: \"user:1\"\n        relation: member\n        object: \"team:1\"\n")
	f, err := LoadBytes("f.yaml", src)
	if err != nil {
		t.Fatal(err)
	}
	got := Check(f, nil)
	want := Finding{Line: 7, Column: 17, Field: `rules["r"].tuple_filters`,
		Message: `the filter object "team" needs a type: use "team:" for every team, or "team:<id>"`,
		Rule:    0, Section: "filters"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("findings: %+v", got)
	}
}

func TestCheckReportsFilterModelGaps(t *testing.T) {
	src := []byte("version: \"1\"\nrules:\n  - name: r\n    when: \"true\"\n    tuple_filters:\n      - user: \"user:{{ input.u }}\"\n        relation: memebr\n        object: \"organization:\"\n        action: delete\n")
	f, err := LoadBytes("f.yaml", src)
	if err != nil {
		t.Fatal(err)
	}
	ix := &ModelIndex{Types: map[string][]string{"user": nil, "organization": {"member"}}}
	got := Check(f, ix)
	if len(got) != 1 || got[0].Message != `type "organization" has no relation "memebr"` || got[0].Line != 7 ||
		got[0].Field != `rules["r"].tuple_filters` {
		t.Fatalf("findings: %+v", got)
	}
}

// The iterator's tuples count after the rule's own, so a gap in one is placed
// in the iterator, not on the rule's tuple of the same index.
func TestCheckPlacesAnIteratorTuplesGap(t *testing.T) {
	src := []byte("version: \"1\"\nrules:\n  - name: r\n    when: \"true\"\n    tuples:\n      - user: \"user:1\"\n        relation: member\n        object: \"team:1\"\n    iterator:\n      source: input.ids\n      as: id\n      tuples:\n        - user: \"user:{{ id }}\"\n          relation: memebr\n          object: \"team:1\"\n")
	f, err := LoadBytes("f.yaml", src)
	if err != nil {
		t.Fatal(err)
	}
	ix := &ModelIndex{Types: map[string][]string{"user": nil, "team": {"member"}}}
	got := Check(f, ix)
	if len(got) != 1 || got[0].Line != 14 || got[0].Column != 21 || got[0].Message != `type "team" has no relation "memebr"` {
		t.Fatalf("findings: %+v", got)
	}
}

// ruleSrc is a one-tuple rule with the given lines spliced in after its when.
func ruleSrc(name, when, extra string) string {
	head := "  - name: " + name + "\n"
	if name == "" {
		head = "  - when: \"" + when + "\"\n"
		when = ""
	}
	out := head
	if when != "" {
		out += "    when: \"" + when + "\"\n"
	}
	return out + extra + "    tuples:\n      - user: \"user:1\"\n        relation: member\n        object: \"team:1\"\n"
}

func TestCheckNamesTheRuleOfACompileError(t *testing.T) {
	cases := []struct {
		name, src string
		rule      int
		section   string
	}{
		// mapper's validation errors name the rule: by index when it has no name...
		{"index", "version: \"1\"\nrules:\n" + ruleSrc("a", "true", "") + "  - action: write\n    when: \"true\"\n    tuples:\n      - user: \"user:1\"\n        object: \"team:1\"\n", 1, "trigger"},
		// ...and by name when it has one.
		{"name", "version: \"1\"\nrules:\n" + ruleSrc("a", "true", "") + ruleSrc("b", "true", "    action: bogus\n"), 1, "action"},
		// An evaluation error names only the part, so the line places it.
		{"line when", "version: \"1\"\nrules:\n" + ruleSrc("a", "true", "") + ruleSrc("b", "input.x ==", ""), 1, "trigger"},
		{"line variable", "version: \"1\"\nrules:\n" + ruleSrc("a", "true", "") + ruleSrc("b", "true", "    variables:\n      x: \"((\"\n"), 1, "variables"},
		{"line iterator", "version: \"1\"\nrules:\n" + ruleSrc("a", "true", "    iterator:\n      source: \"((\"\n      as: it\n"), 0, "iterator"},
		// A tuple's own when is a tuple problem.
		{"line tuple when", "version: \"1\"\nrules:\n" + ruleSrc("a", "true", "") + "  - name: b\n    when: \"true\"\n    tuples:\n      - user: \"user:1\"\n        when: \"((\"\n        relation: member\n        object: \"team:1\"\n", 1, "tuple"},
	}
	for _, c := range cases {
		f, err := LoadDraftBytes("f.yaml", []byte(c.src))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := Check(f, nil)
		if len(got) == 0 || got[0].Rule != c.rule || got[0].Section != c.section {
			t.Fatalf("%s: findings = %+v, want rule %d section %q", c.name, got, c.rule, c.section)
		}
	}
}

func TestCheckLeavesAnErrorInTestsToTheDocument(t *testing.T) {
	src := "version: \"1\"\nrules:\n" + ruleSrc("a", "true", "") + "tests:\n  - name: t\n    input:\n      x: 1\n    expect_tuples:\n      - user: \"user:1\"\n        relation: member\n"
	f, err := LoadDraftBytes("f.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := Check(f, nil)
	if len(got) == 0 || got[0].Rule != -1 || got[0].Section != "" {
		t.Fatalf("findings = %+v, want rule -1", got)
	}
}

func TestCheckFiltersAndModelWarningsCarryTheirRule(t *testing.T) {
	src := []byte("version: \"1\"\nrules:\n" + ruleSrc("a", "true", "") +
		"  - name: b\n    when: \"true\"\n    tuple_filters:\n      - relation: member\n        object: \"team:\"\n    tuples:\n      - user: \"user:1\"\n        relation: member\n        object: \"team:1\"\n")
	f, err := LoadBytes("f.yaml", src)
	if err != nil {
		t.Fatal(err)
	}
	got := Check(f, nil)
	if len(got) != 1 || got[0].Rule != 1 || got[0].Section != "filters" {
		t.Fatalf("filter findings = %+v", got)
	}
	ix := &ModelIndex{Types: map[string][]string{"user": nil}}
	got = Check(f, ix)
	var tuple *Finding
	for k := range got {
		if got[k].Section == "tuple" && got[k].Rule == 0 {
			tuple = &got[k]
		}
	}
	if tuple == nil {
		t.Fatalf("model findings = %+v, want one on rule 0, section tuple", got)
	}
}

func TestValidateJSONIsUnchanged(t *testing.T) {
	b, err := json.Marshal(Finding{Line: 1, Message: "m", Rule: 2, Section: "tuple"})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); s != `{"line":1,"message":"m"}` {
		t.Fatalf("json = %s", s)
	}
}

// mapper accepts a filter object with an empty type, but the read at apply
// refuses it, so the rule would fail each time it fires.
func TestCheckFlagsAFilterObjectWithAnEmptyType(t *testing.T) {
	for _, object := range []string{":", ":t1", ":{{ input.id }}"} {
		src := []byte("version: \"1\"\nrules:\n  - name: r\n    when: \"true\"\n    tuple_filters:\n      - user: \"user:1\"\n        object: \"" + object + "\"\n    tuples:\n      - user: \"user:1\"\n        relation: member\n        object: \"team:1\"\n")
		f, err := LoadBytes("f.yaml", src)
		if err != nil {
			t.Fatal(err)
		}
		if got := Check(f, nil); len(got) != 1 || !strings.Contains(got[0].Message, "needs a type before the colon") {
			t.Fatalf("object %q: findings: %+v", object, got)
		}
	}
}
