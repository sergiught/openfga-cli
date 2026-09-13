package mapping_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func sampleEvent(t *testing.T) map[string]any {
	t.Helper()

	const raw = `{
      "type": "user.created",
      "data": {
        "object": {
          "user_id": "auth0|507f",
          "logins_count": 42,
          "blocked": false,
          "nickname": null,
          "identities": [
            {"connection":"Username-Password-Authentication","isSocial":false}
          ]
        },
        "context": {"tenant": {"id": "my-tenant"}}
      }
    }`

	var event map[string]any
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal sample event: %v", err)
	}

	return event
}

func pathByExpr(ps []mapping.Path, expr string) (mapping.Path, bool) {
	for _, p := range ps {
		if p.Expr == expr {
			return p, true
		}
	}

	return mapping.Path{}, false
}

func TestPathsFlattensScalarsWithKindsAndExamples(t *testing.T) {
	ps := mapping.Paths("input", sampleEvent(t))

	cases := []struct {
		expr    string
		kind    string
		example string
	}{
		{"input.type", "string", "user.created"},
		{"input.data.object.user_id", "string", "auth0|507f"},
		{"input.data.object.logins_count", "number", "42"},
		{"input.data.object.blocked", "bool", "false"},
		{"input.data.object.nickname", "null", "null"},
		{"input.data.context.tenant.id", "string", "my-tenant"},
	}

	for _, c := range cases {
		p, ok := pathByExpr(ps, c.expr)
		if !ok {
			t.Fatalf("missing path %q, got %+v", c.expr, ps)
		}
		if p.Kind != c.kind || p.Example != c.example {
			t.Fatalf("path %q = %+v, want kind %q example %q", c.expr, p, c.kind, c.example)
		}
	}
}

func TestPathsMarksArraysAndDescendsFirstElement(t *testing.T) {
	ps := mapping.Paths("input", sampleEvent(t))

	arr, ok := pathByExpr(ps, "input.data.object.identities")
	if !ok {
		t.Fatalf("missing array path, got %+v", ps)
	}
	if !arr.IsArray || arr.Kind != "array" {
		t.Fatalf("identities path = %+v, want an array", arr)
	}

	conn, ok := pathByExpr(ps, "input.data.object.identities[0].connection")
	if !ok {
		t.Fatalf("missing element path, got %+v", ps)
	}
	if conn.Example != "Username-Password-Authentication" {
		t.Fatalf("identities[0].connection = %+v", conn)
	}

	social, ok := pathByExpr(ps, "input.data.object.identities[0].isSocial")
	if !ok {
		t.Fatalf("missing element bool path, got %+v", ps)
	}
	if social.Kind != "bool" || social.Example != "false" {
		t.Fatalf("identities[0].isSocial = %+v", social)
	}
}

func TestPathsUsesTheGivenRoot(t *testing.T) {
	element := map[string]any{"user_id": "auth0|507f"}

	ps := mapping.Paths("identity", element)

	if _, ok := pathByExpr(ps, "identity.user_id"); !ok {
		t.Fatalf("expected identity.user_id, got %+v", ps)
	}
}

func TestPathsAreSortedAndStable(t *testing.T) {
	event := sampleEvent(t)

	a := mapping.Paths("input", event)
	b := mapping.Paths("input", event)

	if len(a) != len(b) {
		t.Fatalf("lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("not stable at %d: %+v vs %+v", i, a[i], b[i])
		}
	}

	for i := 1; i < len(a); i++ {
		if a[i-1].Expr >= a[i].Expr {
			t.Fatalf("not sorted at %d: %q >= %q", i, a[i-1].Expr, a[i].Expr)
		}
	}
}

func TestPathsTruncatesLongExamples(t *testing.T) {
	long := strings.Repeat("x", 200)

	ps := mapping.Paths("input", map[string]any{"blob": long})

	p, ok := pathByExpr(ps, "input.blob")
	if !ok {
		t.Fatalf("missing blob path, got %+v", ps)
	}
	if len(p.Example) >= len(long) {
		t.Fatalf("example not truncated: %d chars", len(p.Example))
	}
	if !strings.HasSuffix(p.Example, "…") {
		t.Fatalf("example missing ellipsis: %q", p.Example)
	}
}

func TestPathsOnNonObjectRoot(t *testing.T) {
	ps := mapping.Paths("input", "just a string")

	if len(ps) != 1 {
		t.Fatalf("expected exactly one path, got %+v", ps)
	}
	if ps[0].Expr != "input" || ps[0].Kind != "string" || ps[0].Example != "just a string" {
		t.Fatalf("path = %+v", ps[0])
	}
}

func TestPathsCapsRecursionDepth(t *testing.T) {
	// A pathologically nested chain, well past the 32-level cap, sitting
	// alongside a shallow sibling. The walk must stop before the leaf at the
	// bottom of the chain, but must not lose the shallow sibling on the way.
	deep := map[string]any{"leaf": "bottom"}
	for i := 0; i < 100; i++ {
		deep = map[string]any{"child": deep}
	}

	root := map[string]any{
		"shallow": "ok",
		"deep":    deep,
	}

	ps := mapping.Paths("input", root)

	if _, ok := pathByExpr(ps, "input.shallow"); !ok {
		t.Fatalf("expected the shallow sibling to survive the cap, got %+v", ps)
	}

	const maxDepth = 32

	for _, p := range ps {
		if n := strings.Count(p.Expr, "."); n > maxDepth {
			t.Fatalf("path %q exceeds the depth cap (%d dots): %+v", p.Expr, n, p)
		}
	}
}

func TestPathsUsesJSONPathForNonIdentifierKeys(t *testing.T) {
	event := map[string]any{
		"data": map[string]any{
			"x-request-id": "abc-123",
			"a b":          map[string]any{"c": "nested"},
		},
	}

	ps := mapping.Paths("input", event)

	dash, ok := pathByExpr(ps, `json_path(input.data, "x-request-id")`)
	if !ok {
		t.Fatalf("missing dashed-key path, got %+v", ps)
	}
	if dash.Kind != "string" || dash.Example != "abc-123" {
		t.Fatalf("dashed-key path = %+v", dash)
	}

	nested, ok := pathByExpr(ps, `json_path(input.data, "a b").c`)
	if !ok {
		t.Fatalf("missing nested child under a spaced key, got %+v", ps)
	}
	if nested.Example != "nested" {
		t.Fatalf("nested path = %+v", nested)
	}
}

func TestPathsTruncatesByRunesNotBytes(t *testing.T) {
	// "é" is two bytes in UTF-8 but one rune. A byte-based truncation at 64
	// bytes would land mid-character here and produce invalid UTF-8; a
	// rune-based one never does.
	long := strings.Repeat("é", 100)

	ps := mapping.Paths("input", map[string]any{"blob": long})

	p, ok := pathByExpr(ps, "input.blob")
	if !ok {
		t.Fatalf("missing blob path, got %+v", ps)
	}
	if !utf8.ValidString(p.Example) {
		t.Fatalf("truncated example is not valid UTF-8: %q", p.Example)
	}
	if r := []rune(p.Example); len(r) > 64 {
		t.Fatalf("example not truncated: %d runes", len(r))
	}
}

func TestLookupResolvesDottedAndIndexedPaths(t *testing.T) {
	event := sampleEvent(t)

	cases := []struct {
		expr string
		want any
	}{
		{"input.type", "user.created"},
		{"input.data.object.user_id", "auth0|507f"},
		{"input.data.object.logins_count", float64(42)},
		{"input.data.object.blocked", false},
		{"input.data.object.nickname", nil},
		{"input.data.context.tenant.id", "my-tenant"},
		{"input.data.object.identities[0].connection", "Username-Password-Authentication"},
		{"input.data.object.identities[0].isSocial", false},
	}

	for _, c := range cases {
		got, ok := mapping.Lookup(event, c.expr)
		if !ok {
			t.Fatalf("%s: not found", c.expr)
		}
		if got != c.want {
			t.Fatalf("%s = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestLookupResolvesJSONPathExpressions(t *testing.T) {
	event := map[string]any{
		"data": map[string]any{
			"x-request-id": "abc-123",
			"a b":          map[string]any{"c": "nested"},
		},
	}

	cases := []struct {
		expr string
		want any
	}{
		{`json_path(input.data, "x-request-id")`, "abc-123"},
		{`json_path(input.data, "a b").c`, "nested"},
	}

	for _, c := range cases {
		got, ok := mapping.Lookup(event, c.expr)
		if !ok || got != c.want {
			t.Fatalf("%s = %v, %v; want %v, true", c.expr, got, ok, c.want)
		}
	}
}

func TestLookupMissesCleanly(t *testing.T) {
	event := sampleEvent(t)

	misses := []string{
		"",
		"input.nope",
		"notinput.type",
		"input.data.object.identities[9].connection",
		"input.data.object.user_id.deeper",
	}

	for _, expr := range misses {
		if _, ok := mapping.Lookup(event, expr); ok {
			t.Fatalf("%q: expected a miss", expr)
		}
	}
}
