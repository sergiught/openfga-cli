package modeldiff_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/modeldiff"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
)

func mustModel(t *testing.T, dsl string) *optimize.Model {
	t.Helper()
	lm, err := modeltest.LoadModelBytes([]byte(dsl))
	if err != nil {
		t.Fatal(err)
	}
	m, err := optimize.NewModel(lm.Proto)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type want struct {
	kind, typ, rel, cond string
	detail               string
}

func assertEntries(t *testing.T, section string, got []modeldiff.Entry, wants []want) {
	t.Helper()
	if len(got) != len(wants) {
		t.Fatalf("%s: got %d entries, want %d: %+v", section, len(got), len(wants), got)
	}
	for i, w := range wants {
		if got[i].Kind != w.kind {
			t.Errorf("%s[%d]: got %v, want %v", section, i, got[i].Kind, w.kind)
		}
		if got[i].Type != w.typ {
			t.Errorf("%s[%d]: got %v, want %v", section, i, got[i].Type, w.typ)
		}
		if got[i].Relation != w.rel {
			t.Errorf("%s[%d]: got %v, want %v", section, i, got[i].Relation, w.rel)
		}
		if got[i].Condition != w.cond {
			t.Errorf("%s[%d]: got %v, want %v", section, i, got[i].Condition, w.cond)
		}
		if !strings.Contains(got[i].Detail, w.detail) {
			t.Errorf("%s[%d]: got %q, want it to contain %q", section, i, got[i].Detail, w.detail)
		}
	}
}

const base = `model
  schema 1.1
type user
type group
  relations
    define member: [user]
type folder
  relations
    define viewer: [user]
type document
  relations
    define owner: [user]
    define editor: [user]
    define viewer: [user, group#member]
`

func TestDiff(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		breaking []want
		changed  []want
		added    []want
	}{
		{name: "identical", old: base, new: base},
		{
			name:     "type removed",
			old:      base,
			new:      strings.Replace(base, "type folder\n  relations\n    define viewer: [user]\n", "", 1),
			breaking: []want{{kind: modeldiff.TypeRemoved, typ: "folder"}},
		},
		{
			name:     "relation removed",
			old:      base,
			new:      strings.Replace(base, "    define editor: [user]\n", "", 1),
			breaking: []want{{kind: modeldiff.RelationRemoved, typ: "document", rel: "editor"}},
		},
		{
			name:     "direct type removed",
			old:      base,
			new:      strings.Replace(base, "[user, group#member]", "[user]", 1),
			breaking: []want{{kind: modeldiff.DirectTypeRemoved, typ: "document", rel: "viewer", detail: "group#member"}},
		},
		{
			name:     "wildcard removed",
			old:      strings.Replace(base, "[user, group#member]", "[user, user:*]", 1),
			new:      strings.Replace(base, "[user, group#member]", "[user]", 1),
			breaking: []want{{kind: modeldiff.DirectTypeRemoved, typ: "document", rel: "viewer", detail: "user:*"}},
		},
		{
			name:     "direct replaced by rewrite",
			old:      strings.Replace(base, "[user, group#member]", "[user]", 1),
			new:      strings.Replace(base, "[user, group#member]", "owner", 1),
			breaking: []want{{kind: modeldiff.DirectTypeRemoved, typ: "document", rel: "viewer", detail: "user"}},
			changed:  []want{{kind: modeldiff.RewriteChanged, typ: "document", rel: "viewer", detail: "access may differ (access analysis not done yet)"}},
		},
		{
			name:     "condition added to element",
			old:      condModel("[user]", "timestamp", "true"),
			new:      condModel("[user with non_expired]", "timestamp", "true"),
			breaking: []want{{kind: modeldiff.DirectTypeRemoved, typ: "document", rel: "viewer", detail: "`user`"}},
			added:    []want{{kind: modeldiff.DirectTypeAdded, typ: "document", rel: "viewer", detail: "user with non_expired"}},
		},
		{
			name:    "union to intersection",
			old:     strings.Replace(base, "define editor: [user]", "define editor: [user] or owner", 1),
			new:     strings.Replace(base, "define editor: [user]", "define editor: [user] and owner", 1),
			changed: []want{{kind: modeldiff.RewriteChanged, typ: "document", rel: "editor"}},
		},
		{
			name:     "condition param removed",
			old:      condModel2("timestamp", "string", "true"),
			new:      condModel("[user with non_expired]", "timestamp", "true"),
			breaking: []want{{kind: modeldiff.ConditionParamRemoved, cond: "non_expired", detail: "parameter `s` removed from condition `non_expired`"}},
		},
		{
			name:     "condition param type changed",
			old:      condModel("[user with non_expired]", "timestamp", "true"),
			new:      condModel("[user with non_expired]", "string", "true"),
			breaking: []want{{kind: modeldiff.ConditionParamTypeChanged, cond: "non_expired", detail: "timestamp"}},
		},
		{
			name:     "condition param added is breaking",
			old:      "model\n  schema 1.1\ntype user\ntype doc\n  relations\n    define viewer: [user with c]\ncondition c(t: int) {\n  t > 0\n}\n",
			new:      "model\n  schema 1.1\ntype user\ntype doc\n  relations\n    define viewer: [user with c]\ncondition c(t: int, enabled: bool) {\n  t > 0 && enabled\n}\n",
			breaking: []want{{kind: modeldiff.ConditionParamAdded, cond: "c", detail: "enabled"}},
			changed:  []want{{kind: modeldiff.ConditionExpressionChanged, cond: "c"}},
		},
		{
			name:    "condition expression changed",
			old:     condModel("[user with non_expired]", "timestamp", "true"),
			new:     condModel("[user with non_expired]", "timestamp", "false"),
			changed: []want{{kind: modeldiff.ConditionExpressionChanged, cond: "non_expired"}},
		},
		{
			name:     "added things",
			old:      condModel("[user]", "timestamp", "true"),
			new:      addedModel,
			breaking: []want{{kind: modeldiff.ConditionParamAdded, cond: "non_expired", detail: "extra"}},
			added: []want{
				{kind: modeldiff.ConditionAdded, cond: "other"},
				{kind: modeldiff.RelationAdded, typ: "document", rel: "editor"},
				{kind: modeldiff.DirectTypeAdded, typ: "document", rel: "viewer", detail: "user:*"},
				{kind: modeldiff.TypeAdded, typ: "team"},
			},
		},
		{
			name:    "schema version",
			old:     "model\n  schema 1.1\ntype user\n",
			new:     "model\n  schema 1.2\ntype user\n",
			changed: []want{{kind: modeldiff.SchemaVersionChanged, detail: "1.1 → 1.2"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := modeldiff.Diff(mustModel(t, tc.old), mustModel(t, tc.new))
			assertEntries(t, "breaking", r.Breaking, tc.breaking)
			assertEntries(t, "changed", r.Changed, tc.changed)
			assertEntries(t, "added", r.Added, tc.added)
			if wantEmpty := len(tc.breaking)+len(tc.changed)+len(tc.added) == 0; r.Empty() != wantEmpty {
				t.Errorf("Empty() = %v, want %v", r.Empty(), wantEmpty)
			}
		})
	}
}

func TestDiffConditionRemoved(t *testing.T) {
	old := condModel("[user]", "timestamp", "true")
	nw := "model\n  schema 1.1\ntype user\ntype document\n  relations\n    define viewer: [user]\n"
	r := modeldiff.Diff(mustModel(t, old), mustModel(t, nw))
	assertEntries(t, "breaking", r.Breaking, []want{{kind: modeldiff.ConditionRemoved, cond: "non_expired"}})
}

func condModel(viewer, ptype, expr string) string {
	return "model\n  schema 1.1\ntype user\ntype document\n  relations\n    define viewer: " + viewer +
		"\ncondition non_expired(t: " + ptype + ") {\n  " + expr + "\n}\n"
}

func condModel2(t1, t2, expr string) string {
	return "model\n  schema 1.1\ntype user\ntype document\n  relations\n    define viewer: [user with non_expired]" +
		"\ncondition non_expired(t: " + t1 + ", s: " + t2 + ") {\n  " + expr + "\n}\n"
}

const addedModel = `model
  schema 1.1
type user
type team
type document
  relations
    define viewer: [user, user:*]
    define editor: [user]
condition non_expired(t: timestamp, extra: string) {
  true
}
condition other(x: string) {
  true
}
`

func TestDiffUnionOrderIgnored(t *testing.T) {
	mk := func(a, b string) *optimize.Model {
		return mustModel(t, "model\n  schema 1.1\ntype user\ntype document\n  relations\n    define a: [user]\n    define b: [user]\n    define c: [user]\n    define viewer: "+a+"\n    define editor: "+b+"\n")
	}
	r := modeldiff.Diff(mk("a or b", "(a or b) and c"), mk("b or a", "c and (b or a)"))
	if !r.Empty() {
		t.Errorf("want empty, got %+v", r)
	}
}

func TestDiffUnionOrderIgnoredInDifferenceAndIntersection(t *testing.T) {
	mk := func(a, b string) *optimize.Model {
		return mustModel(t, "model\n  schema 1.1\ntype user\ntype document\n  relations\n    define a: [user]\n    define b: [user]\n    define c: [user]\n    define viewer: "+a+"\n    define editor: "+b+"\n")
	}
	r := modeldiff.Diff(mk("(a or b) but not c", "(a or b) and c"), mk("(b or a) but not c", "c and (b or a)"))
	if !r.Empty() {
		t.Errorf("want empty, got %+v", r)
	}
}

func TestDiffSlicesNonNil(t *testing.T) {
	m := mustModel(t, base)
	b, err := json.Marshal(modeldiff.Diff(m, m))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"breaking":[]`) {
		t.Errorf("got %q, want it to contain %q", string(b), `"breaking":[]`)
	}
	if !strings.Contains(string(b), `"changed":[]`) {
		t.Errorf("got %q, want it to contain %q", string(b), `"changed":[]`)
	}
	if !strings.Contains(string(b), `"added":[]`) {
		t.Errorf("got %q, want it to contain %q", string(b), `"added":[]`)
	}
	if !strings.Contains(string(b), `"cost":[]`) {
		t.Errorf("got %q, want it to contain %q", string(b), `"cost":[]`)
	}
}

func TestDiffConditionExpressionFormatting(t *testing.T) {
	for _, tc := range []struct {
		name, old, new string
		changed        bool
	}{
		{"spacing around operator", "t > 0", "t>0", false},
		{"spacing around logical and", "a && b", "a&&b", false},
		{"string literal whitespace", `s == "a b"`, `s == "ab"`, true},
		{"real change", "t > 0", "t > 1", true},
		{"macro spacing", "xs.exists(x, x > 0)", "xs.exists(x,x>0)", false},
		{"macro swapped", "xs.all(x, x > 0)", "xs.exists(x, x > 0)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mk := func(expr string) *optimize.Model {
				return mustModel(t, "model\n  schema 1.1\ntype user\ntype document\n  relations\n    define viewer: [user with c]\n"+
					"condition c(t: int, a: bool, b: bool, s: string, xs: list<int>) {\n  "+expr+"\n}\n")
			}
			r := modeldiff.Diff(mk(tc.old), mk(tc.new))
			if got := len(r.Changed) > 0; got != tc.changed {
				t.Errorf("changed = %v, want %v: %+v", got, tc.changed, r.Changed)
			}
			if !tc.changed && len(r.Breaking)+len(r.Added)+len(r.Changed) != 0 {
				t.Errorf("want no entries, got %+v", r)
			}
		})
	}
}
