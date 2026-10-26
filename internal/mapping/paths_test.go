package mapping_test

import (
	"context"
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

func TestPathsSummarizesArrayExamples(t *testing.T) {
	// A container's example must stay display text for the picker, not a
	// truncated Go literal of its contents.
	ps := mapping.Paths("input", sampleEvent(t))

	arr, ok := pathByExpr(ps, "input.data.object.identities")
	if !ok {
		t.Fatalf("missing array path, got %+v", ps)
	}
	if arr.Example != "1 item(s)" {
		t.Fatalf("array example = %q, want %q", arr.Example, "1 item(s)")
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
	// The picker exists to choose a field; an entry whose expression is the
	// entire event is noise, so a non-map root yields nothing.
	if ps := mapping.Paths("input", nil); len(ps) != 0 {
		t.Fatalf("nil root produced %d paths, got %+v", len(ps), ps)
	}
	if ps := mapping.Paths("input", "scalar"); len(ps) != 0 {
		t.Fatalf("scalar root produced %d paths, got %+v", len(ps), ps)
	}
}

func TestPathsCapsRecursionDepth(t *testing.T) {
	// Two nested chains straddling the 32-level cap, alongside a shallow
	// sibling. A leaf one level inside the cap must survive; the same shape
	// one level past it must not, so the boundary is pinned in both
	// directions rather than merely asserted vacuously.
	const maxDepth = 32

	nearCap := map[string]any{"leaf": "just inside"}
	for i := 0; i < maxDepth-2; i++ {
		nearCap = map[string]any{"child": nearCap}
	}

	pastCap := map[string]any{"leaf": "just outside"}
	for i := 0; i < maxDepth; i++ {
		pastCap = map[string]any{"child": pastCap}
	}

	root := map[string]any{
		"shallow": "ok",
		"near":    nearCap,
		"past":    pastCap,
	}

	ps := mapping.Paths("input", root)

	if _, ok := pathByExpr(ps, "input.shallow"); !ok {
		t.Fatalf("expected the shallow sibling to survive the cap, got %+v", ps)
	}

	nearExpr := "input.near" + strings.Repeat(".child", maxDepth-2) + ".leaf"
	if p, ok := pathByExpr(ps, nearExpr); !ok || p.Example != "just inside" {
		t.Fatalf("expected the near-cap leaf %q to survive, got %+v", nearExpr, ps)
	}

	pastExpr := "input.past" + strings.Repeat(".child", maxDepth) + ".leaf"
	if _, ok := pathByExpr(ps, pastExpr); ok {
		t.Fatalf("expected the past-cap leaf %q to be cut off, got %+v", pastExpr, ps)
	}

	for _, p := range ps {
		if n := strings.Count(p.Expr, "."); n > maxDepth {
			t.Fatalf("path %q exceeds the depth cap (%d dots): %+v", p.Expr, n, p)
		}
	}
}

func TestPathsAndLookupRoundTripNonASCIIKeys(t *testing.T) {
	// isValidIdent decodes runes properly and accepts any Unicode letter, so
	// Paths renders these in dot notation; the parser must decode the same
	// way or the round trip silently breaks on non-ASCII keys.
	event := map[string]any{"héllo": "value1", "日本語": "value2"}
	want := map[string]any{
		"input.héllo": "value1",
		"input.日本語":   "value2",
	}

	ps := mapping.Paths("input", event)
	if len(ps) != len(want) {
		t.Fatalf("got %d paths, want %d: %+v", len(ps), len(want), ps)
	}

	for _, p := range ps {
		wantVal, ok := want[p.Expr]
		if !ok {
			t.Fatalf("unexpected path %q", p.Expr)
		}

		got, ok := mapping.Lookup(event, p.Expr)
		if !ok || got != wantVal {
			t.Fatalf("Lookup(%q) = %v, %v; want %v, true", p.Expr, got, ok, wantVal)
		}
	}
}

func TestPathsIndexesNonIdentifierKeys(t *testing.T) {
	event := map[string]any{
		"data": map[string]any{
			"x-request-id": "abc-123",
			"a b":          map[string]any{"c": "nested"},
		},
	}

	ps := mapping.Paths("input", event)

	dash, ok := pathByExpr(ps, `input.data["x-request-id"]`)
	if !ok {
		t.Fatalf("missing dashed-key path, got %+v", ps)
	}
	if dash.Kind != "string" || dash.Example != "abc-123" {
		t.Fatalf("dashed-key path = %+v", dash)
	}

	nested, ok := pathByExpr(ps, `input.data["a b"].c`)
	if !ok {
		t.Fatalf("missing nested child under a spaced key, got %+v", ps)
	}
	if nested.Example != "nested" {
		t.Fatalf("nested path = %+v", nested)
	}
}

// TestPathsAgreeWithMapperOnKeysContainingDots pins the one thing a picker row
// promises: that the expression next to the example really produces it. A key
// with a dot in it — an Auth0 namespaced claim is the everyday case — cannot be
// addressed with json_path, whose path argument mapper splits on ".", so the
// example and the evaluated value would come from two different traversals.
func TestPathsAgreeWithMapperOnKeysContainingDots(t *testing.T) {
	event := map[string]any{
		"data": map[string]any{
			"https://myapp.example.com/roles": "admin",
			"a.b":                             "literal-key",
			"a":                               map[string]any{"b": "traversed"},
		},
	}

	ps := mapping.Paths("input", event)

	for _, want := range []string{"admin", "literal-key", "traversed"} {
		p, ok := pathByExample(ps, want)
		if !ok {
			t.Fatalf("no path shows example %q, got %+v", want, ps)
		}

		got, ok := mapping.Lookup(event, p.Expr)
		if !ok || got != want {
			t.Fatalf("Lookup(%q) = %v, %v; want %q, true", p.Expr, got, ok, want)
		}

		if got := evalUserTemplate(t, event, p.Expr); got != "user:"+want {
			t.Fatalf("the picker shows %q for %s, but mapper evaluates it to %q",
				want, p.Expr, got)
		}
	}
}

func pathByExample(ps []mapping.Path, example string) (mapping.Path, bool) {
	for _, p := range ps {
		if p.Example == example {
			return p, true
		}
	}

	return mapping.Path{}, false
}

// evalUserTemplate runs expr through mapper as a tuple user template and
// returns the user it produced, or "" when mapper produced no tuple.
func evalUserTemplate(t *testing.T, event map[string]any, expr string) string {
	t.Helper()

	doc := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r",
		When: "true",
		Tuples: []mapping.Tuple{{
			User:     "user:{{ " + expr + " }}",
			Relation: "member",
			Object:   "organization:acme",
		}},
	}}}

	p := mapping.Evaluate(context.Background(), doc, event)
	if len(p.Tuples) == 0 {
		return ""
	}

	return p.Tuples[0].User
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

func TestLookupResolvesBareRoot(t *testing.T) {
	// Resolving a bare "input" to the whole event is harmless, and a user may
	// legitimately type it even though Paths never emits it as an entry.
	event := sampleEvent(t)

	got, ok := mapping.Lookup(event, "input")
	if !ok {
		t.Fatal("expected \"input\" to resolve")
	}
	if m, ok := got.(map[string]any); !ok || m["type"] != "user.created" {
		t.Fatalf("input = %v, want the event root", got)
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

func TestLookupCapsParserDepth(t *testing.T) {
	// json_path calls can nest as the base of another json_path call, and
	// parseExpr/parseAtom mutually recurse once per level with no bound of
	// their own. A few hundred levels is enough to prove the cap holds
	// without allocating an input large enough to actually blow the stack.
	//
	// The event is built so the lookup would actually succeed if the parser
	// had no cap — every level unwraps a real "k" key down to a leaf — so a
	// miss here is proof the cap fired, not an unrelated key miss.
	const levels = 300

	var value any = "leaf-value"
	for i := 0; i < levels; i++ {
		value = map[string]any{"k": value}
	}
	event := value.(map[string]any)

	expr := strings.Repeat("json_path(", levels) + "input" + strings.Repeat(`, "k")`, levels)

	if _, ok := mapping.Lookup(event, expr); ok {
		t.Fatal("expected a deeply nested json_path expression to miss cleanly")
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
