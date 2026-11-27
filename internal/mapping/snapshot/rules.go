package snapshot

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/openfga/mapper/language"
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
	// Tuples are the contested tuples a rule's entry stands for, nil for
	// every other entry.
	Tuples []language.Tuple
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

// excludes reports whether the expression provably leaves out events of type
// name: a comparison or in-list of input.type that does not name it, joined by
// && (either side is enough) or || (both sides are needed). Anything else,
// including negation and !=, does not exclude.
func excludes(n ast.Node, name string) bool {
	switch n := n.(type) {
	case *ast.ChainNode:
		return excludes(n.Node, name)
	case *ast.BinaryNode:
		switch n.Operator {
		case "||", "or":
			return excludes(n.Left, name) && excludes(n.Right, name)
		case "&&", "and":
			return excludes(n.Left, name) || excludes(n.Right, name)
		case "==":
			if isInputType(n.Left) {
				s, ok := n.Right.(*ast.StringNode)
				return ok && s.Value != name
			}
			if isInputType(n.Right) {
				s, ok := n.Left.(*ast.StringNode)
				return ok && s.Value != name
			}
		case "in":
			list, ok := n.Right.(*ast.ArrayNode)
			if !ok || !isInputType(n.Left) {
				return false
			}
			for _, e := range list.Nodes {
				if _, ok := e.(*ast.StringNode); !ok {
					return false
				}
			}
			return !slices.ContainsFunc(list.Nodes, func(e ast.Node) bool { return isString(e, name) })
		}
	}
	return false
}

// firesOn reports whether a "when" expression can be true for an event of type
// name. An empty expression or one that does not parse is taken to.
func firesOn(when, name string) bool {
	root := parse(when)
	return root == nil || !excludes(root, name)
}

// firedTemplates returns r with only the templates an event of type name can
// fire, or false when r's own "when" leaves that event out.
func firedTemplates(r mapping.Rule, name string) (mapping.Rule, bool) {
	if !firesOn(r.When, name) {
		return r, false
	}
	keep := func(ts []mapping.Tuple) (out []mapping.Tuple) {
		for _, t := range ts {
			if firesOn(t.When, name) {
				out = append(out, t)
			}
		}
		return out
	}
	fired := r
	fired.Tuples = keep(r.Tuples)
	if r.Iterator != nil {
		it := *r.Iterator
		it.Tuples = keep(r.Iterator.Tuples)
		fired.Iterator = &it
	}
	return fired, true
}

// removes reports whether r removes tuples: it has tuple filters, or its
// action is delete, or one of its templates is.
func removes(r mapping.Rule) bool {
	del := func(t mapping.Tuple) bool { return t.Action == "delete" }
	return len(r.Filters) > 0 || r.Action == "delete" || slices.ContainsFunc(r.Tuples, del) ||
		(r.Iterator != nil && slices.ContainsFunc(r.Iterator.Tuples, del))
}

// Removers turns contested tuples into NOT CHECKED entries, one for each
// rule that removes tuples and can fire on the event type that contested
// them, whether a later event wrote the tuple after the removal, an event of
// the type that wrote it removed it, or a window left out events that might
// have. A rule can have an entry of each kind.
func Removers(doc *mapping.Document, contested []Contested) []NotChecked {
	if len(contested) == 0 {
		return nil
	}
	claimed := make([]bool, len(contested))
	var out []NotChecked
	entry := func(rule, reason string, ts []language.Tuple) NotChecked {
		var shapes []mapping.Shape
		var text []string
		for _, t := range ts {
			s := mapping.ShapeOf(t)
			if !slices.Contains(shapes, s) {
				shapes = append(shapes, s)
				text = append(text, s.String())
			}
		}
		noun := "tuples"
		if len(ts) == 1 {
			noun = "tuple"
		}
		return NotChecked{
			Rule:   rule,
			Reason: fmt.Sprintf("%s: %d %s of %s", reason, len(ts), noun, strings.Join(text, ", ")),
			Shapes: shapes,
			Tuples: ts,
		}
	}
	const reason = "removes tuples that other events write, and a snapshot does not say which happened last; not compared"
	unreadReason := func(by string) string {
		return "read with --since: could have removed these for a " + by + " that was not read; not compared"
	}
	for _, r := range doc.Rules {
		if !removes(r) {
			continue
		}
		var ts []language.Tuple
		var unreadOrder []string
		unread := map[string][]language.Tuple{}
		for i, c := range contested {
			if !firesOn(r.When, c.By) {
				continue
			}
			claimed[i] = true
			if !c.Unread {
				ts = append(ts, c.Tuple)
				continue
			}
			if _, ok := unread[c.By]; !ok {
				unreadOrder = append(unreadOrder, c.By)
			}
			unread[c.By] = append(unread[c.By], c.Tuple)
		}
		if len(ts) > 0 {
			out = append(out, entry(r.Name, reason, ts))
		}
		for _, by := range unreadOrder {
			out = append(out, entry(r.Name, unreadReason(by), unread[by]))
		}
	}
	var order []string
	rest := map[string][]language.Tuple{}
	for i, c := range contested {
		if !claimed[i] {
			if _, ok := rest[c.By]; !ok {
				order = append(order, c.By)
			}
			rest[c.By] = append(rest[c.By], c.Tuple)
		}
	}
	for _, by := range order {
		out = append(out, entry(by, reason, rest[by]))
	}
	return out
}

// unreadTemplate is a removal's user, relation and object as written in the
// mapping, and the windowed type whose rule it belongs to. A field that is
// empty reaches anything.
type unreadTemplate struct {
	by                     string
	user, relation, object string
}

// reaches reports whether a value the template renders to can be v, judged from
// the text alone. The mapper renders a field by joining its literal text with
// the values of its expressions, so everything before the first "{{" starts the
// result. A template without "{{" is exact, except an object that is a type
// prefix, which is every object of the type, as a tuple filter reads it.
func reaches(template, v string, object bool) bool {
	if template == "" {
		return true
	}
	if i := strings.Index(template, "{{"); i >= 0 {
		return strings.HasPrefix(v, template[:i])
	}
	if i := strings.IndexByte(template, ':'); object && i > 0 && i == len(template)-1 {
		return strings.HasPrefix(v, template)
	}
	return template == v
}

// UnreadRemovals returns, for the types read by window, a function that says
// which of them has a rule whose removal can match a tuple, or false when none
// can. Only the removals of templates an event of the type can fire count: the
// rule's tuple filters of either action and its delete tuples. It returns nil
// when no rule qualifies.
func UnreadRemovals(doc *mapping.Document, windowed []string) func(language.Tuple) (string, bool) {
	var removals []unreadTemplate
	for _, n := range windowed {
		for _, r := range doc.Rules {
			if !removes(r) {
				continue
			}
			fired, ok := firedTemplates(r, n)
			if !ok {
				continue
			}
			for _, f := range fired.Filters {
				removals = append(removals, unreadTemplate{n, f.User, f.Relation, f.Object})
			}
			ts := slices.Clone(fired.Tuples)
			if fired.Iterator != nil {
				ts = append(ts, fired.Iterator.Tuples...)
			}
			for _, t := range ts {
				if t.Action == "delete" || r.Action == "delete" {
					removals = append(removals, unreadTemplate{n, t.User, t.Relation, t.Object})
				}
			}
		}
	}
	if len(removals) == 0 {
		return nil
	}
	return func(t language.Tuple) (string, bool) {
		for _, x := range removals {
			if reaches(x.user, t.User, false) && reaches(x.relation, t.Relation, false) && reaches(x.object, t.Object, true) {
				return x.by, true
			}
		}
		return "", false
	}
}

// Coverage returns the shapes snapshot rules write, and the rules whose
// tuples fall outside that: non-snapshot rules writing shapes no snapshot
// rule owns, snapshot rules with templated types or relations, and snapshot
// rules with templates whose own "when" leaves out snapshot events.
// windowed names the types the provider read by window since a time. Users
// outside the window send no event, so any rule that can fire on one of those
// types, snapshot rule or not, cannot tell which stored tuples are stale: its
// shapes are not owned and it is listed with the window. A template of such a
// rule with a templated type or relation can write any shape, so nothing is
// owned. With no windowed names since is ignored.
func Coverage(doc *mapping.Document, types []Type, windowed []string, since time.Time) (owned []mapping.Shape, notChecked []NotChecked) {
	// fire returns the windowed types a rule can fire on, the shapes its
	// templates write for them, and how many of those templates have no shape.
	fire := func(r mapping.Rule) (w []string, shapes []mapping.Shape, unshaped int) {
		for _, n := range windowed {
			fired, ok := firedTemplates(r, n)
			if !ok {
				continue
			}
			w = append(w, n)
			ss, u := mapping.RuleShapes(fired)
			unshaped += u
			for _, s := range ss {
				if !slices.Contains(shapes, s) {
					shapes = append(shapes, s)
				}
			}
		}
		return w, shapes, unshaped
	}
	var windowedShapes []mapping.Shape
	anyTemplated := false
	for _, r := range doc.Rules {
		_, shapes, unshaped := fire(r)
		windowedShapes = append(windowedShapes, shapes...)
		anyTemplated = anyTemplated || unshaped > 0
	}
	for _, r := range doc.Rules {
		if IsSnapshotRule(r, types) && !anyTemplated {
			kept, _ := splitTemplates(r, types)
			shapes, _ := mapping.RuleShapes(kept)
			for _, s := range shapes {
				if !slices.Contains(owned, s) && !slices.Contains(windowedShapes, s) {
					owned = append(owned, s)
				}
			}
		}
	}
	for _, r := range doc.Rules {
		windowTemplated := false
		if w, shapes, unshaped := fire(r); len(shapes) > 0 || unshaped > 0 {
			at := since.UTC().Format("2006-01-02 15:04:05")
			if len(shapes) > 0 {
				text := make([]string, len(shapes))
				for i, s := range shapes {
					text[i] = s.String()
				}
				notChecked = append(notChecked, NotChecked{
					Rule: r.Name,
					Reason: fmt.Sprintf("read with --since: only %s changed since %s UTC; not compared: %s",
						strings.Join(w, ", "), at, strings.Join(text, ", ")),
					Shapes: shapes,
				})
			}
			if unshaped > 0 {
				windowTemplated = true
				notChecked = append(notChecked, NotChecked{
					Rule: r.Name,
					Reason: fmt.Sprintf("read with --since: only %s changed since %s UTC, and the type or relation is templated, so no stale tuple can be told apart",
						strings.Join(w, ", "), at),
				})
			}
		}
		if IsSnapshotRule(r, types) {
			kept, dropped := splitTemplates(r, types)
			if _, unshaped := mapping.RuleShapes(kept); unshaped > 0 && !windowTemplated {
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
			if !slices.Contains(owned, s) && !slices.Contains(windowedShapes, s) {
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

// Writes returns a predicate that is true for a tuple the mapping's rules can
// write: one whose relation and object type match a shape of any rule, snapshot
// or not. The user type is not compared, because a wildcard or userset user
// changes it and a desired tuple must never be filtered out of the stored set.
// A rule that writes a templated type or relation makes every tuple match.
func Writes(doc *mapping.Document) func(language.Tuple) bool {
	type key struct{ relation, objectType string }
	keys := map[key]bool{}
	for _, r := range doc.Rules {
		shapes, unshaped := mapping.RuleShapes(r)
		if unshaped > 0 {
			return func(language.Tuple) bool { return true }
		}
		for _, s := range shapes {
			keys[key{s.Relation, s.ObjectType}] = true
		}
	}
	return func(t language.Tuple) bool {
		s := mapping.ShapeOf(t)
		return keys[key{s.Relation, s.ObjectType}]
	}
}
