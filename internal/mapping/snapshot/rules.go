package snapshot

import (
	"slices"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/sergiught/openfga-cli/internal/mapping"
)

// parse compiles a "when" expression to its syntax tree. An expression that
// does not parse yields nil.
func parse(when string) ast.Node {
	tree, err := parser.Parse(when)
	if err != nil {
		return nil
	}
	return tree.Node
}

// stringLiterals collects the value of every string literal in the tree.
type stringLiterals struct{ out []string }

func (v *stringLiterals) Visit(n *ast.Node) {
	if s, ok := (*n).(*ast.StringNode); ok {
		v.out = append(v.out, s.Value)
	}
}

// literals returns every string literal in a "when" expression, taken from its
// parsed tree so comments and escapes cannot shift the pairing. An expression
// that does not parse has none.
func literals(when string) []string {
	root := parse(when)
	if root == nil {
		return nil
	}
	v := &stringLiterals{}
	ast.Walk(&root, v)
	return v.out
}

// isInputType reports whether n is input.type (or input?.type).
func isInputType(n ast.Node) bool {
	if c, ok := n.(*ast.ChainNode); ok {
		n = c.Node
	}
	m, ok := n.(*ast.MemberNode)
	if !ok {
		return false
	}
	id, ok := m.Node.(*ast.IdentifierNode)
	if !ok || id.Value != "input" {
		return false
	}
	p, ok := m.Property.(*ast.StringNode)
	return ok && p.Value == "type"
}

func isString(n ast.Node, value string) bool {
	s, ok := n.(*ast.StringNode)
	return ok && s.Value == value
}

// matches reports whether the expression can be true because input.type equals
// name: a comparison with ==, or membership in an in-list, joined by && or ||.
// Negation never counts.
func matches(n ast.Node, name string) bool {
	switch n := n.(type) {
	case *ast.ChainNode:
		return matches(n.Node, name)
	case *ast.BinaryNode:
		switch n.Operator {
		case "||", "or", "&&", "and":
			return matches(n.Left, name) || matches(n.Right, name)
		case "==":
			return isInputType(n.Left) && isString(n.Right, name) ||
				isInputType(n.Right) && isString(n.Left, name)
		case "in":
			list, ok := n.Right.(*ast.ArrayNode)
			if !ok || !isInputType(n.Left) {
				return false
			}
			return slices.ContainsFunc(list.Nodes, func(e ast.Node) bool { return isString(e, name) })
		}
	}
	return false
}

// optsIn reports whether the "when" expression matches events of type name.
func optsIn(when, name string) bool {
	root := parse(when)
	return root != nil && matches(root, name)
}

// mentionsInputType reports whether the tree reads input.type anywhere.
type mentionsInputType struct{ found bool }

func (v *mentionsInputType) Visit(n *ast.Node) {
	if isInputType(*n) {
		v.found = true
	}
}

func names(r mapping.Rule, name string) bool { return optsIn(r.When, name) }

// IsSnapshotRule reports whether r's "when" compares input.type with any of
// types.
func IsSnapshotRule(r mapping.Rule, types []Type) bool {
	for _, t := range types {
		if names(r, t.Name) {
			return true
		}
	}
	return false
}

// Requested returns the types some rule opts in to, in types' order.
func Requested(doc *mapping.Document, types []Type) []string {
	var out []string
	for _, t := range types {
		for _, r := range doc.Rules {
			if names(r, t.Name) {
				out = append(out, t.Name)
				break
			}
		}
	}
	return out
}

// Unknown returns the quoted literals starting "snapshot." that no type
// names, sorted and deduplicated.
func Unknown(doc *mapping.Document, types []Type) []string {
	var out []string
	for _, r := range doc.Rules {
		for _, lit := range literals(r.When) {
			if !strings.HasPrefix(lit, "snapshot.") || slices.Contains(out, lit) {
				continue
			}
			// A prefix such as "snapshot." (for startsWith) names no type.
			if strings.HasSuffix(lit, ".") && slices.ContainsFunc(types, func(t Type) bool { return strings.HasPrefix(t.Name, lit) }) {
				continue
			}
			if !slices.ContainsFunc(types, func(t Type) bool { return t.Name == lit }) {
				out = append(out, lit)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Suggestion is a rule that handles a live event but not its snapshot twin.
type Suggestion struct{ Rule, Clause string }

// Suggestions lists the non-snapshot rules whose "when" names a type's
// Mirrors event, with the clause that would opt them in.
func Suggestions(doc *mapping.Document, types []Type) []Suggestion {
	var out []Suggestion
	for _, r := range doc.Rules {
		if IsSnapshotRule(r, types) {
			continue
		}
		for _, t := range types {
			if names(r, t.Mirrors) {
				out = append(out, Suggestion{Rule: r.Name, Clause: `|| input.type == "` + t.Name + `"`})
			}
		}
	}
	return out
}

// NotChecked is a rule whose tuples a snapshot cannot vouch for.
type NotChecked struct {
	Rule   string
	Reason string
	Shapes []mapping.Shape
}

// ownsTuple reports whether a template's own "when" lets it fire on snapshot
// events. A "when" that never reads input.type does; one that reads it must
// compare it with a snapshot type; one that does not parse never does.
func ownsTuple(when string, types []Type) bool {
	if strings.TrimSpace(when) == "" {
		return true
	}
	root := parse(when)
	if root == nil {
		return false
	}
	m := &mentionsInputType{}
	ast.Walk(&root, m)
	if !m.found {
		return true
	}
	return slices.ContainsFunc(types, func(t Type) bool { return matches(root, t.Name) })
}

// splitTemplates divides r's tuple templates into those a snapshot event can
// fire and those a "when" of their own keeps it from.
func splitTemplates(r mapping.Rule, types []Type) (kept, dropped mapping.Rule) {
	kept, dropped = r, r
	// Only the types the rule itself opts in to ever reach its templates.
	var mine []Type
	for _, t := range types {
		if names(r, t.Name) {
			mine = append(mine, t)
		}
	}
	split := func(ts []mapping.Tuple) (k, d []mapping.Tuple) {
		for _, t := range ts {
			if ownsTuple(t.When, mine) {
				k = append(k, t)
			} else {
				d = append(d, t)
			}
		}
		return k, d
	}
	kept.Tuples, dropped.Tuples = split(r.Tuples)
	if r.Iterator != nil {
		it, dt := *r.Iterator, *r.Iterator
		it.Tuples, dt.Tuples = split(r.Iterator.Tuples)
		kept.Iterator, dropped.Iterator = &it, &dt
	}
	return kept, dropped
}

// Coverage returns the shapes snapshot rules write, and the rules whose
// tuples fall outside that: non-snapshot rules writing shapes no snapshot
// rule owns, snapshot rules with templated types or relations, and snapshot
// rules with templates whose own "when" leaves out snapshot events.
func Coverage(doc *mapping.Document, types []Type) (owned []mapping.Shape, notChecked []NotChecked) {
	for _, r := range doc.Rules {
		if IsSnapshotRule(r, types) {
			kept, _ := splitTemplates(r, types)
			shapes, _ := mapping.RuleShapes(kept)
			for _, s := range shapes {
				if !slices.Contains(owned, s) {
					owned = append(owned, s)
				}
			}
		}
	}
	for _, r := range doc.Rules {
		if IsSnapshotRule(r, types) {
			kept, dropped := splitTemplates(r, types)
			if _, unshaped := mapping.RuleShapes(kept); unshaped > 0 {
				notChecked = append(notChecked, NotChecked{
					Rule:   r.Name,
					Reason: "the type or relation is templated, so stale tuples cannot be told apart",
				})
			}
			shapes, _ := mapping.RuleShapes(dropped)
			var missing []mapping.Shape
			var text []string
			for _, s := range shapes {
				if !slices.Contains(owned, s) {
					missing = append(missing, s)
					text = append(text, s.String())
				}
			}
			if len(missing) > 0 {
				notChecked = append(notChecked, NotChecked{
					Rule:   r.Name,
					Reason: "a tuple's own when leaves out snapshot events; not compared: " + strings.Join(text, ", "),
					Shapes: missing,
				})
			}
			continue
		}
		shapes, _ := mapping.RuleShapes(r)
		var missing []mapping.Shape
		var text []string
		for _, s := range shapes {
			if !slices.Contains(owned, s) {
				missing = append(missing, s)
				text = append(text, s.String())
			}
		}
		if len(missing) > 0 {
			notChecked = append(notChecked, NotChecked{
				Rule:   r.Name,
				Reason: "no snapshot rule writes " + strings.Join(text, ", "),
				Shapes: missing,
			})
		}
	}
	return owned, notChecked
}
