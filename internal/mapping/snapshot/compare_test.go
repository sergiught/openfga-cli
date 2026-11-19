package snapshot

import (
	"testing"

	"github.com/openfga/mapper/language"
	"github.com/stretchr/testify/require"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func tup(user, rel, obj string) language.Tuple {
	return language.Tuple{User: user, Relation: rel, Object: obj}
}

func withCond(t language.Tuple, name string, ctx map[string]any) language.Tuple {
	t.Condition = name
	t.Context = ctx
	return t
}

func TestCompare(t *testing.T) {
	a := tup("user:a", "member", "org:1")
	b := tup("user:b", "member", "org:1")
	cx := withCond(tup("user:c", "member", "org:1"), "x", nil)
	cy := withCond(tup("user:c", "member", "org:1"), "y", nil)
	d := tup("user:d", "member", "org:1")
	e := tup("user:e", "viewer", "doc:1")
	owned := []mapping.Shape{mapping.ShapeOf(a)}

	got := Compare([]language.Tuple{a, b, cx}, []language.Tuple{b, cy, d, e}, owned)
	require.Equal(t, []language.Tuple{a}, got.Missing)
	require.Equal(t, []language.Tuple{cx}, got.Changed)
	require.Equal(t, []language.Tuple{d}, got.Stale)
	require.False(t, got.Empty())
}

func TestCompareEmptyDesired(t *testing.T) {
	s := []language.Tuple{tup("user:a", "member", "org:1"), tup("user:b", "member", "org:1")}
	got := Compare(nil, s, []mapping.Shape{mapping.ShapeOf(s[0])})
	require.Equal(t, s, got.Stale)
	require.False(t, got.Empty())
}

func TestCompareContextOrder(t *testing.T) {
	d := withCond(tup("user:a", "member", "org:1"), "c", map[string]any{"a": 1, "b": 2})
	s := withCond(tup("user:a", "member", "org:1"), "c", map[string]any{"b": 2, "a": 1})
	require.True(t, Compare([]language.Tuple{d}, []language.Tuple{s}, nil).Empty())
}

func TestByShape(t *testing.T) {
	ts := []language.Tuple{tup("user:a", "member", "org:1"), tup("user:b", "member", "org:2"), tup("user:a", "viewer", "doc:1")}
	got := ByShape(ts)
	require.Equal(t, []ShapeCount{{mapping.ShapeOf(ts[0]), 2}, {mapping.ShapeOf(ts[2]), 1}}, got)
}
