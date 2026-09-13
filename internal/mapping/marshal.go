package mapping

import (
	"bytes"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// Marshal renders d as a mapping file.
//
// The node tree is built by hand rather than by tagging structs because two
// things must be stable: key order (the language documents one, and Go struct
// tags cannot express "omit this whole section"), and map ordering — `variables`
// and a tuple's `context` are YAML mappings whose order is the user's, and
// ranging a Go map would reshuffle them on every save. Templates are forced to
// double-quoted style so a literal relation and an interpolated object read
// alike; expressions are left to the emitter, which quotes them only when a
// plain scalar would not round-trip.
func Marshal(d *Document) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode}
	appendField(root, "version", quoted(Version))

	rules := &yaml.Node{Kind: yaml.SequenceNode}
	for _, r := range d.Rules {
		rules.Content = append(rules.Content, ruleNode(r))
	}
	appendField(root, "rules", rules)

	if len(d.Tests) > 0 {
		tests := &yaml.Node{Kind: yaml.SequenceNode}
		for _, tc := range d.Tests {
			tests.Content = append(tests.Content, testNode(tc))
		}
		appendField(root, "tests", tests)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("render mapping: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("render mapping: %w", err)
	}
	return buf.Bytes(), nil
}

func ruleNode(r Rule) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	appendField(n, "name", plain(r.Name))
	appendIf(n, "when", r.When != "", func() *yaml.Node { return plain(r.When) })
	appendIf(n, "action", r.Action != "", func() *yaml.Node { return plain(r.Action) })

	if len(r.Variables) > 0 {
		vars := &yaml.Node{Kind: yaml.MappingNode}
		for _, v := range r.Variables {
			appendField(vars, v.Name, plain(v.Expr))
		}
		appendField(n, "variables", vars)
	}
	if r.Iterator != nil {
		it := &yaml.Node{Kind: yaml.MappingNode}
		appendField(it, "source", plain(r.Iterator.Source))
		appendField(it, "as", plain(r.Iterator.As))
		if len(r.Iterator.Tuples) > 0 {
			appendField(it, "tuples", tupleSeq(r.Iterator.Tuples))
		}
		appendField(n, "iterator", it)
	}
	if len(r.Filters) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, f := range r.Filters {
			fn := &yaml.Node{Kind: yaml.MappingNode}
			appendField(fn, "object", quoted(f.Object))
			appendIf(fn, "user", f.User != "", func() *yaml.Node { return quoted(f.User) })
			appendIf(fn, "relation", f.Relation != "", func() *yaml.Node { return quoted(f.Relation) })
			appendIf(fn, "action", f.Action != "", func() *yaml.Node { return plain(f.Action) })
			seq.Content = append(seq.Content, fn)
		}
		appendField(n, "tuple_filters", seq)
	}
	if len(r.Tuples) > 0 {
		appendField(n, "tuples", tupleSeq(r.Tuples))
	}
	return n
}

func tupleSeq(ts []Tuple) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, t := range ts {
		tn := &yaml.Node{Kind: yaml.MappingNode}
		appendField(tn, "user", quoted(t.User))
		appendField(tn, "relation", quoted(t.Relation))
		appendField(tn, "object", quoted(t.Object))
		appendIf(tn, "when", t.When != "", func() *yaml.Node { return plain(t.When) })
		appendIf(tn, "action", t.Action != "", func() *yaml.Node { return plain(t.Action) })
		appendIf(tn, "condition", t.Condition != "", func() *yaml.Node { return plain(t.Condition) })
		if len(t.Context) > 0 {
			ctx := &yaml.Node{Kind: yaml.MappingNode}
			for _, e := range t.Context {
				appendField(ctx, e.Key, quoted(e.Template))
			}
			appendField(tn, "context", ctx)
		}
		seq.Content = append(seq.Content, tn)
	}
	return seq
}

func testNode(tc TestCase) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	appendField(n, "name", plain(tc.Name))
	appendField(n, "input", anyNode(tc.Input))
	tuples := &yaml.Node{Kind: yaml.SequenceNode}
	for _, t := range tc.ExpectTuples {
		tn := &yaml.Node{Kind: yaml.MappingNode}
		appendField(tn, "user", quoted(t.User))
		appendField(tn, "relation", quoted(t.Relation))
		appendField(tn, "object", quoted(t.Object))
		appendIf(tn, "action", t.Action != "", func() *yaml.Node { return plain(string(t.Action)) })
		appendIf(tn, "condition", t.Condition != "", func() *yaml.Node { return plain(t.Condition) })
		if len(t.Context) > 0 {
			appendField(tn, "context", anyNode(t.Context))
		}
		tuples.Content = append(tuples.Content, tn)
	}
	appendField(n, "expect_tuples", tuples)
	if len(tc.ExpectTupleFilters) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, f := range tc.ExpectTupleFilters {
			fn := &yaml.Node{Kind: yaml.MappingNode}
			appendIf(fn, "user", f.User != "", func() *yaml.Node { return quoted(f.User) })
			appendIf(fn, "relation", f.Relation != "", func() *yaml.Node { return quoted(f.Relation) })
			appendIf(fn, "object", f.Object != "", func() *yaml.Node { return quoted(f.Object) })
			appendField(fn, "action", plain(string(f.Action)))
			seq.Content = append(seq.Content, fn)
		}
		appendField(n, "expect_tuple_filters", seq)
	}
	return n
}

// anyNode renders decoded JSON (a sample event, a rendered condition context)
// with map keys sorted, so a document saved twice produces identical bytes.
func anyNode(v any) *yaml.Node {
	switch t := v.(type) {
	case map[string]any:
		n := &yaml.Node{Kind: yaml.MappingNode}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			appendField(n, k, anyNode(t[k]))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode}
		for _, it := range t {
			n.Content = append(n.Content, anyNode(it))
		}
		return n
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case string:
		return plain(t)
	default:
		n := &yaml.Node{}
		// Non-string scalars (numbers, bools) keep their YAML type; Encode into a
		// node so the emitter picks the right tag without a type switch per kind.
		if err := n.Encode(t); err != nil {
			return plain(fmt.Sprint(t))
		}
		return n
	}
}

func appendField(parent *yaml.Node, key string, val *yaml.Node) {
	parent.Content = append(parent.Content, plain(key), val)
}

func appendIf(parent *yaml.Node, key string, cond bool, val func() *yaml.Node) {
	if cond {
		appendField(parent, key, val())
	}
}

// plain leaves the style to the emitter, which quotes only when a bare scalar
// would not round-trip (a leading `{`, an embedded `: `, a number-like string).
func plain(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// quoted forces double quotes, used for template fields so a literal relation
// and an interpolated object are visually the same kind of thing.
func quoted(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: yaml.DoubleQuotedStyle}
}
