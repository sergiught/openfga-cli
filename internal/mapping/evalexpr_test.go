package mapping_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func identitiesEvent() map[string]any {
	return map[string]any{
		"type": "user.created",
		"data": map[string]any{"object": map[string]any{"identities": []any{
			map[string]any{"connection": "c1", "user_id": "u1"},
			map[string]any{"connection": "c2"},
		}}},
	}
}

// A13: Lookup treated json_path's second argument as one key and knew nothing
// of ??, functions or variables. EvalExpr hands the expression to mapper, so
// every form mapper accepts resolves to what mapper resolves it to.
func TestEvalExprEvaluatesWhatMapperEvaluates(t *testing.T) {
	ev := identitiesEvent()
	want := ev["data"].(map[string]any)["object"].(map[string]any)["identities"]
	vars := []mapping.Variable{{Name: "ids", Expr: "input.data.object.identities"}}
	for _, expr := range []string{
		`input.data.object.identities`,
		`json_path(input, "data.object.identities")`,
		`input.data.object.identities ?? []`,
		`variables.ids`,
	} {
		got, err := mapping.EvalExpr(context.Background(), vars, expr, ev)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %#v, want %#v", expr, got, want)
		}
	}
	if got, err := mapping.EvalExpr(context.Background(), nil, `len(input.data.object.identities)`, ev); err != nil || got != 2 {
		t.Fatalf("len(...) = %#v, %v; want 2", got, err)
	}
	if got, err := mapping.EvalExpr(context.Background(), nil, `input.missing`, ev); err != nil || got != nil {
		t.Fatalf("a missing key = %#v, %v; want nil, no error", got, err)
	}
}

func TestEvalExprReportsWhatDoesNotEvaluate(t *testing.T) {
	ev := identitiesEvent()
	for _, expr := range []string{"", "   ", "1 +", "input.data.object.identities[9].connection", "1, 2"} {
		if got, err := mapping.EvalExpr(context.Background(), nil, expr, ev); err == nil {
			t.Fatalf("%q = %#v, want an error", expr, got)
		}
	}
	if _, err := mapping.EvalExpr(context.Background(), nil, "input.type", nil); err == nil {
		t.Fatal("a nil event evaluated")
	}
}

// Review focus: sample values are the user's own payload. Unicode keys and a
// value carrying terminal control bytes come back exactly, so nothing between
// mapper and the screen mangles them (sanitizing is the renderer's job).
func TestEvalExprKeepsUnicodeAndControlBytes(t *testing.T) {
	ev := map[string]any{"日本語": "値", "x": "\x1b[31mred\x1b[0m\u0007"}
	if got, err := mapping.EvalExpr(context.Background(), nil, `input.日本語`, ev); err != nil || got != "値" {
		t.Fatalf("input.日本語 = %#v, %v", got, err)
	}
	if got, err := mapping.EvalExpr(context.Background(), nil, `input.x`, ev); err != nil || got != ev["x"] {
		t.Fatalf("input.x = %#v, %v; want %#v", got, err, ev["x"])
	}
}

func TestIteratorItemIsTheFirstElementTheIteratorWalks(t *testing.T) {
	ev := identitiesEvent()
	first := map[string]any{"connection": "c1", "user_id": "u1"}
	for _, src := range []string{
		`json_path(input, "data.object.identities")`,
		`input.data.object.identities ?? []`,
		`variables.ids`,
	} {
		r := mapping.Rule{
			Variables: []mapping.Variable{{Name: "ids", Expr: "input.data.object.identities"}},
			Iterator:  &mapping.Iterator{Source: src, As: "identity"},
		}
		got, ok := mapping.IteratorItem(context.Background(), r, ev)
		if !ok || !reflect.DeepEqual(got, first) {
			t.Fatalf("%s: item = %#v, %v; want %#v", src, got, ok, first)
		}
	}
	for name, r := range map[string]mapping.Rule{
		"no iterator":  {},
		"empty list":   {Iterator: &mapping.Iterator{Source: "[]", As: "x"}},
		"not a list":   {Iterator: &mapping.Iterator{Source: "input.type", As: "x"}},
		"broken":       {Iterator: &mapping.Iterator{Source: "input.data.object.identities[", As: "x"}},
		"missing list": {Iterator: &mapping.Iterator{Source: "input.nope ?? []", As: "x"}},
	} {
		if got, ok := mapping.IteratorItem(context.Background(), r, ev); ok {
			t.Fatalf("%s: item = %#v, want none", name, got)
		}
	}
}

// Finding 1: expr is evaluated as a variable of its own, never pasted into other
// expression text, so a trailing comment is fine and unbalanced brackets are not.
func TestEvalExprCompilesTheExpressionOnItsOwn(t *testing.T) {
	ev := map[string]any{"ids": []any{map[string]any{"id": "u1"}}}
	got, err := mapping.EvalExpr(context.Background(), nil, "input.ids // the ids", ev)
	if err != nil || !reflect.DeepEqual(got, ev["ids"]) {
		t.Fatalf("a trailing comment = %#v, %v; want the list", got, err)
	}
	for _, expr := range []string{"input.ids][0", "input.ids) + (input.ids"} {
		if got, err := mapping.EvalExpr(context.Background(), nil, expr, ev); err == nil {
			t.Fatalf("%q = %#v, want an error", expr, got)
		}
	}
}

// The probe declares one more variable after the rule's own, so it must not
// take a name the rule already uses.
func TestEvalExprLeavesTheRulesVariableNamesAlone(t *testing.T) {
	vars := []mapping.Variable{{Name: "ofga_eval_probe", Expr: `"taken"`}}
	if got, err := mapping.EvalExpr(context.Background(), vars, `variables.ofga_eval_probe + "!"`, identitiesEvent()); err != nil || got != "taken!" {
		t.Fatalf("got %#v, %v; want \"taken!\"", got, err)
	}
}

// An integer id above 2^53 is exact in the decoded event, and stays exact
// through EvalExpr and IteratorItem.
func TestEvalExprKeepsLargeIntegersExact(t *testing.T) {
	ev, err := mapping.DecodeEvent([]byte(`{"type":"x","ids":[{"id":9007199254740993}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := mapping.EvalExpr(context.Background(), nil, "input.ids[0].id", ev); err != nil || got != 9007199254740993 {
		t.Fatalf("got %#v, %v; want 9007199254740993", got, err)
	}
	item, ok := mapping.IteratorItem(context.Background(),
		mapping.Rule{Iterator: &mapping.Iterator{Source: "input.ids", As: "i"}}, ev)
	if want := map[string]any{"id": 9007199254740993}; !ok || !reflect.DeepEqual(item, want) {
		t.Fatalf("item = %#v, %v; want %#v", item, ok, want)
	}
}
