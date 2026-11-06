package modeldiff_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sergiught/openfga-cli/internal/modeldiff"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
)

func mustModel(t *testing.T, dsl string) *optimize.Model {
	t.Helper()
	lm, err := modeltest.LoadModelBytes([]byte(dsl))
	require.NoError(t, err)
	m, err := optimize.NewModel(lm.Proto)
	require.NoError(t, err)
	return m
}

type want struct {
	kind, typ, rel, cond string
	detail               string
}

func assertEntries(t *testing.T, section string, got []modeldiff.Entry, wants []want) {
	t.Helper()
	require.Len(t, got, len(wants), "%s: %+v", section, got)
	for i, w := range wants {
		require.Equal(t, w.kind, got[i].Kind, "%s[%d]", section, i)
		require.Equal(t, w.typ, got[i].Type, "%s[%d]", section, i)
		require.Equal(t, w.rel, got[i].Relation, "%s[%d]", section, i)
		require.Equal(t, w.cond, got[i].Condition, "%s[%d]", section, i)
		require.Contains(t, got[i].Detail, w.detail, "%s[%d]", section, i)
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
			breaking: []want{{kind: modeldiff.ConditionParamRemoved, cond: "non_expired", detail: "s"}},
		},
		{
			name:     "condition param type changed",
			old:      condModel("[user with non_expired]", "timestamp", "true"),
			new:      condModel("[user with non_expired]", "string", "true"),
			breaking: []want{{kind: modeldiff.ConditionParamTypeChanged, cond: "non_expired", detail: "timestamp"}},
		},
		{
			name:    "condition expression changed",
			old:     condModel("[user with non_expired]", "timestamp", "true"),
			new:     condModel("[user with non_expired]", "timestamp", "false"),
			changed: []want{{kind: modeldiff.ConditionExpressionChanged, cond: "non_expired"}},
		},
		{
			name: "added things",
			old:  condModel("[user]", "timestamp", "true"),
			new:  addedModel,
			added: []want{
				{kind: modeldiff.ConditionParamAdded, cond: "non_expired", detail: "extra"},
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
			require.Equal(t, len(tc.breaking)+len(tc.changed)+len(tc.added) == 0, r.Empty())
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
	require.True(t, r.Empty(), "%+v", r)
}

func TestDiffSlicesNonNil(t *testing.T) {
	m := mustModel(t, base)
	b, err := json.Marshal(modeldiff.Diff(m, m))
	require.NoError(t, err)
	require.Contains(t, string(b), `"breaking":[]`)
	require.Contains(t, string(b), `"changed":[]`)
	require.Contains(t, string(b), `"added":[]`)
	require.Contains(t, string(b), `"cost":[]`)
}
