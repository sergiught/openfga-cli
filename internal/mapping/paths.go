package mapping

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxExample bounds what the path picker shows per row so one long blob cannot
// push the expression off screen.
const maxExample = 64

// maxDepth bounds how far Paths descends into a sample event. Sample events are
// pasted in by the user, so nothing guarantees they are shallow; past this many
// levels we stop rather than risk an unbounded walk.
const maxDepth = 32

// Path is one addressable field of a sample event, ready to drop into an
// expression or a template.
type Path struct {
	Expr    string
	Kind    string
	Example string
	IsArray bool
}

// Paths flattens v into every expression that addresses a field inside it,
// prefixed with root ("input" at rule scope, the alias inside an iterator).
// Arrays are both reported themselves — an iterator needs the array, not its
// elements — and descended through element 0, so a field the user can see in
// the sample is always pickable.
func Paths(root string, v any) []Path {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	var out []Path
	walkPaths(root, obj, 0, &out)

	sort.Slice(out, func(i, j int) bool { return out[i].Expr < out[j].Expr })

	return out
}

func walkPaths(prefix string, v any, depth int, out *[]Path) {
	if depth >= maxDepth {
		return
	}

	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			expr := childExpr(prefix, k)
			if _, ok := child.(map[string]any); !ok {
				*out = append(*out, describePath(expr, child))
			}
			walkPaths(expr, child, depth+1, out)
		}
	case []any:
		if len(t) > 0 {
			walkPaths(prefix+"[0]", t[0], depth+1, out)
		}
	}
}

// childExpr addresses key on parent. A key that is not a valid expr
// identifier — one with a dash, a space, or anything else outside
// letters/digits/underscore — would silently change the meaning of the
// expression if joined with a dot (a-b reads as subtraction), so it is
// addressed with json_path instead.
func childExpr(parent, key string) string {
	if isValidIdent(key) {
		return parent + "." + key
	}

	return fmt.Sprintf("json_path(%s, %q)", parent, key)
}

func isValidIdent(s string) bool {
	if s == "" {
		return false
	}

	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) {
			continue
		}
		if i > 0 && unicode.IsDigit(r) {
			continue
		}

		return false
	}

	return true
}

func describePath(expr string, v any) Path {
	_, isArray := v.([]any)

	return Path{Expr: expr, Kind: kindOf(v), Example: exampleOf(v), IsArray: isArray}
}

func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64, int, int64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

func exampleOf(v any) string {
	var s string

	switch t := v.(type) {
	case nil:
		s = "null"
	case string:
		s = t
	case bool:
		s = strconv.FormatBool(t)
	case []any:
		s = fmt.Sprintf("%d item(s)", len(t))
	case map[string]any:
		s = fmt.Sprintf("%d field(s)", len(t))
	default:
		s = fmt.Sprintf("%v", t)
	}

	r := []rune(s)
	if len(r) > maxExample {
		return string(r[:maxExample-1]) + "…"
	}

	return s
}

// Lookup resolves a path produced by Paths back to its value in event. The
// leading "input." segment addresses the event root; anything else misses
// because an iterator-scoped path has no event-level meaning.
func Lookup(event map[string]any, expr string) (any, bool) {
	p := &exprParser{s: expr}

	node, ok := p.parseExpr(0)
	if !ok || p.i != len(p.s) {
		return nil, false
	}

	return node.eval(event)
}

// exprNode evaluates one node of a path expression against the event root.
type exprNode interface {
	eval(event map[string]any) (any, bool)
}

// identNode is a bare identifier. The only one Paths ever emits as an atom is
// the root itself ("input"); anything else misses.
type identNode struct{ name string }

func (n identNode) eval(event map[string]any) (any, bool) {
	if n.name != "input" {
		return nil, false
	}

	return event, true
}

// fieldNode looks up a key on whatever base evaluates to, whether that key
// came from dot notation or from a json_path call.
type fieldNode struct {
	base exprNode
	key  string
}

func (n fieldNode) eval(event map[string]any) (any, bool) {
	v, ok := n.base.eval(event)
	if !ok {
		return nil, false
	}

	obj, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}

	child, ok := obj[n.key]

	return child, ok
}

// indexNode resolves a `[n]` applied to whatever base evaluates to.
type indexNode struct {
	base exprNode
	idx  int
}

func (n indexNode) eval(event map[string]any) (any, bool) {
	v, ok := n.base.eval(event)
	if !ok {
		return nil, false
	}

	arr, ok := v.([]any)
	if !ok || n.idx < 0 || n.idx >= len(arr) {
		return nil, false
	}

	return arr[n.idx], true
}

// exprParser is a small recursive-descent parser for the subset of expr
// syntax Paths emits: dotted identifiers, `[n]` indexing, and json_path calls,
// which can themselves nest as the base of another json_path call.
type exprParser struct {
	s string
	i int
}

func (p *exprParser) peek() byte {
	if p.i >= len(p.s) {
		return 0
	}

	return p.s[p.i]
}

func (p *exprParser) skipSpaces() {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
}

func (p *exprParser) parseExpr(depth int) (exprNode, bool) {
	if depth > maxDepth {
		return nil, false
	}

	base, ok := p.parseAtom(depth)
	if !ok {
		return nil, false
	}

	for {
		switch p.peek() {
		case '.':
			p.i++

			name, ok := p.parseIdent()
			if !ok {
				return nil, false
			}

			base = fieldNode{base: base, key: name}
		case '[':
			p.i++

			n, ok := p.parseInt()
			if !ok || p.peek() != ']' {
				return nil, false
			}

			p.i++
			base = indexNode{base: base, idx: n}
		default:
			return base, true
		}
	}
}

func (p *exprParser) parseAtom(depth int) (exprNode, bool) {
	if strings.HasPrefix(p.s[p.i:], "json_path(") {
		p.i += len("json_path(")

		inner, ok := p.parseExpr(depth + 1)
		if !ok {
			return nil, false
		}

		p.skipSpaces()

		if p.peek() != ',' {
			return nil, false
		}

		p.i++
		p.skipSpaces()

		key, ok := p.parseQuotedString()
		if !ok {
			return nil, false
		}

		p.skipSpaces()

		if p.peek() != ')' {
			return nil, false
		}

		p.i++

		return fieldNode{base: inner, key: key}, true
	}

	name, ok := p.parseIdent()
	if !ok {
		return nil, false
	}

	return identNode{name: name}, true
}

func (p *exprParser) parseIdent() (string, bool) {
	start := p.i

	for p.i < len(p.s) {
		r, size := utf8.DecodeRuneInString(p.s[p.i:])
		if r == utf8.RuneError && size <= 1 {
			break
		}
		if r == '_' || unicode.IsLetter(r) || (p.i > start && unicode.IsDigit(r)) {
			p.i += size
			continue
		}

		break
	}

	if p.i == start {
		return "", false
	}

	return p.s[start:p.i], true
}

func (p *exprParser) parseInt() (int, bool) {
	start := p.i

	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
	}

	if p.i == start {
		return 0, false
	}

	n, err := strconv.Atoi(p.s[start:p.i])
	if err != nil {
		return 0, false
	}

	return n, true
}

func (p *exprParser) parseQuotedString() (string, bool) {
	if p.peek() != '"' {
		return "", false
	}

	start := p.i
	p.i++

	for p.i < len(p.s) {
		switch p.s[p.i] {
		case '\\':
			p.i += 2
		case '"':
			p.i++

			s, err := strconv.Unquote(p.s[start:p.i])
			if err != nil {
				return "", false
			}

			return s, true
		default:
			p.i++
		}
	}

	return "", false
}
