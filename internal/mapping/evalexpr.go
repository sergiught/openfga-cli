package mapping

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openfga/mapper"
)

// evalPrefix makes the probe tuple's user a valid type:id whatever the value
// renders to; EvalExpr strips it again before decoding.
const evalPrefix = "x:"

// evalProbe names the variable EvalExpr declares for the expression.
const evalProbe = "ofga_eval_probe"

// EvalExpr evaluates expr against event the way mapper evaluates an iterator
// source or a variable: input is the event, and variables are vars, each one
// seeing the ones declared before it.
//
// mapper exports no single-expression evaluator, so this builds a one-rule
// document that declares expr as one more variable after vars, walks a
// one-element list holding that variable and renders the element with toJSON,
// compiles and evaluates it through mapper's public API, and decodes the JSON
// back. expr is never spliced into other expression text, so it compiles on its
// own, in the same scope, and whatever mapper evaluates — json_path, ??,
// functions, variables, comments — evaluates here the same way, because it is
// mapper doing it.
func EvalExpr(ctx context.Context, vars []Variable, expr string, event map[string]any) (any, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, errors.New("empty expression")
	}
	probe := Rule{
		Name:      "eval",
		When:      "true",
		Variables: append(append([]Variable(nil), vars...), Variable{Name: evalProbe, Expr: expr}),
		Iterator: &Iterator{
			Source: "[variables." + evalProbe + "]",
			As:     "item",
			Tuples: []Tuple{{User: evalPrefix + "{{ toJSON(item) }}", Relation: "r", Object: "o:o"}},
		},
	}
	src, err := Marshal(&Document{Rules: []Rule{probe}})
	if err != nil {
		return nil, err
	}
	m, err := mapper.Compile(src, mapper.WithTimeout(EvalTimeout))
	if err != nil {
		return nil, err
	}
	res, err := m.Evaluate(ctx, event)
	if err != nil {
		return nil, err
	}
	// The probe walks a one-element list into one tuple with no when, so this
	// holds: a top-level comma in expr is a syntax error, not a longer list.
	// The check stays as a defence, should mapper ever emit other than one.
	if len(res.Tuples) != 1 {
		return nil, fmt.Errorf("%s is not one value", expr)
	}
	var v any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(res.Tuples[0].User, evalPrefix)), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// IteratorItem is the first element r's iterator walks in event: the value its
// alias names inside the iterator's tuples. It is false when r has no iterator,
// or its source does not evaluate to a non-empty list.
func IteratorItem(ctx context.Context, r Rule, event map[string]any) (any, bool) {
	if r.Iterator == nil || event == nil {
		return nil, false
	}
	v, err := EvalExpr(ctx, r.Variables, r.Iterator.Source, event)
	if err != nil {
		return nil, false
	}
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	return list[0], true
}
