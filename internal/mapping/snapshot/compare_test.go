package snapshot

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/openfga/mapper/language"

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
	if want := []language.Tuple{a}; !reflect.DeepEqual(got.Missing, want) {
		t.Errorf("Missing = %+v, want %+v", got.Missing, want)
	}
	if want := []language.Tuple{cx}; !reflect.DeepEqual(got.Changed, want) {
		t.Errorf("Changed = %+v, want %+v", got.Changed, want)
	}
	if want := []language.Tuple{cy}; !reflect.DeepEqual(got.Was, want) {
		t.Errorf("Was = %+v, want %+v", got.Was, want)
	}
	if want := []language.Tuple{d}; !reflect.DeepEqual(got.Stale, want) {
		t.Errorf("Stale = %+v, want %+v", got.Stale, want)
	}
	if got.Empty() {
		t.Error("Empty() = true, want false")
	}
}

func TestCompareWasFollowsChangedOrder(t *testing.T) {
	// Desired is unsorted; Was must line up with the sorted Changed.
	b, a := tup("user:b", "member", "org:1"), tup("user:a", "member", "org:1")
	got := Compare(
		[]language.Tuple{withCond(b, "n", nil), withCond(a, "n", nil)},
		[]language.Tuple{withCond(a, "o", nil), withCond(b, "o", nil)}, nil)
	for i, c := range got.Changed {
		if got.Was[i].User != c.User || got.Was[i].Condition != "o" {
			t.Errorf("Was[%d] = %+v, does not match Changed[%d] = %+v", i, got.Was[i], i, c)
		}
	}
	if len(got.Was) != 2 {
		t.Errorf("len(Was) = %d, want 2", len(got.Was))
	}
}

func TestCompareEmptyDesired(t *testing.T) {
	s := []language.Tuple{tup("user:a", "member", "org:1"), tup("user:b", "member", "org:1")}
	got := Compare(nil, s, []mapping.Shape{mapping.ShapeOf(s[0])})
	if !reflect.DeepEqual(got.Stale, s) {
		t.Errorf("Stale = %+v, want %+v", got.Stale, s)
	}
	if got.Empty() {
		t.Error("Empty() = true, want false")
	}
}

func changed(d, s language.Tuple) int {
	return len(Compare([]language.Tuple{d}, []language.Tuple{s}, nil).Changed)
}

func TestCompareContextNumberTypes(t *testing.T) {
	// A rendered template gives an int, a JSON read of the store a float64.
	d := withCond(tup("user:a", "member", "org:1"), "c", map[string]any{"n": 1})
	s := withCond(tup("user:a", "member", "org:1"), "c", map[string]any{"n": float64(1)})
	if got := changed(d, s); got != 0 {
		t.Errorf("Changed = %d, want 0", got)
	}
}

func TestCompareContextOrder(t *testing.T) {
	var a, b map[string]any
	if err := json.Unmarshal([]byte(`{"a":1,"b":{"x":[1,2],"y":"z"},"c":true}`), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"c":true,"b":{"y":"z","x":[1,2]},"a":1}`), &b); err != nil {
		t.Fatal(err)
	}
	d := withCond(tup("user:a", "member", "org:1"), "c", a)
	s := withCond(tup("user:a", "member", "org:1"), "c", b)
	if got := changed(d, s); got != 0 {
		t.Errorf("Changed = %d, want 0", got)
	}
}

func TestCompareContextDifferent(t *testing.T) {
	d := withCond(tup("user:a", "member", "org:1"), "c", map[string]any{"n": 1})
	s := withCond(tup("user:a", "member", "org:1"), "c", map[string]any{"n": 2})
	if got := changed(d, s); got != 1 {
		t.Errorf("Changed = %d, want 1", got)
	}
}

func TestByShape(t *testing.T) {
	ts := []language.Tuple{tup("user:a", "member", "org:1"), tup("user:b", "member", "org:2"), tup("user:a", "viewer", "doc:1")}
	got := ByShape(ts)
	if want := []ShapeCount{{mapping.ShapeOf(ts[0]), 2}, {mapping.ShapeOf(ts[2]), 1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("ByShape() = %+v, want %+v", got, want)
	}
}
