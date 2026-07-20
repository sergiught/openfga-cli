package modeltest

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// writeModel writes an inline DSL model to a temp file and returns its path,
// for tests that need a small bespoke model rather than testdata/docs.
func writeModel(t *testing.T, dsl string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.fga")
	if err := os.WriteFile(path, []byte(dsl), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNearestMissSuggestsGrantingTuple(t *testing.T) {
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	lm, err := loadModel(filepath.Join("testdata", "docs", "model.fga"))
	if err != nil {
		t.Fatal(err)
	}

	// No tuples seeded, so viewer document:1 for user:anne is currently false.
	sc, err := setupScope(t, eng, lm.Proto, nil)
	if err != nil {
		t.Fatal(err)
	}

	req := CheckReq{User: "user:anne", Relation: "viewer", Object: "document:1"}

	verdict, err := eng.Check(t.Context(), sc, req)
	if err != nil {
		t.Fatal(err)
	}
	if verdict {
		t.Fatal("expected viewer document:1 to be false with no tuples seeded")
	}

	msg, err := nearestMiss(t.Context(), lm, eng, sc, req)
	if err != nil {
		t.Fatal(err)
	}
	if msg == "" {
		t.Fatal("expected a non-empty nearest-miss suggestion")
	}
	if !strings.Contains(msg, "document:1") || !strings.Contains(msg, "user:anne") {
		t.Fatalf("expected suggestion to reference document:1 and user:anne; got %q", msg)
	}
	if !strings.Contains(msg, "owner") && !strings.Contains(msg, "viewer") {
		t.Fatalf("expected suggestion to name a plausible granting relation (owner/viewer); got %q", msg)
	}
}

// TestNearestMissWildcardGrant covers a relation whose only direct edge is a
// wildcard ("[user:*]"): the concrete-user probe tuple OpenFGA used to be
// asked to write is invalid (wildcard-only relations reject concrete users),
// which used to make eng.Check error and nearestMiss propagate that error
// instead of returning a suggestion. It should now probe with the wildcard
// user form and succeed.
func TestNearestMissWildcardGrant(t *testing.T) {
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	path := writeModel(t, `model
  schema 1.1

type user

type document
  relations
    define viewer: [user:*]
`)
	lm, err := loadModel(path)
	if err != nil {
		t.Fatal(err)
	}

	// No tuples seeded, so viewer document:1 for user:anne is currently false.
	sc, err := setupScope(t, eng, lm.Proto, nil)
	if err != nil {
		t.Fatal(err)
	}

	req := CheckReq{User: "user:anne", Relation: "viewer", Object: "document:1"}

	verdict, err := eng.Check(t.Context(), sc, req)
	if err != nil {
		t.Fatal(err)
	}
	if verdict {
		t.Fatal("expected viewer document:1 to be false with no tuples seeded")
	}

	msg, err := nearestMiss(t.Context(), lm, eng, sc, req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if msg == "" {
		t.Fatal("expected a non-empty nearest-miss suggestion for the wildcard grant")
	}
	if !strings.Contains(msg, "user:*") {
		t.Fatalf("expected suggestion to reference the wildcard user:*; got %q", msg)
	}
}

// TestNearestMissMultiParentTTU covers a tupleset relation with more than one
// directly-assignable parent type, where only a later parent type's target
// relation actually grants. An unconditional break used to abandon the
// search after the first parent type failed to match, even when a later one
// would have succeeded.
func TestNearestMissMultiParentTTU(t *testing.T) {
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	path := writeModel(t, `model
  schema 1.1

type user

type employee

type folderA
  relations
    define viewer: [employee]

type folderB
  relations
    define viewer: [user]

type document
  relations
    define parent: [folderA, folderB]
    define viewer: viewer from parent
`)
	lm, err := loadModel(path)
	if err != nil {
		t.Fatal(err)
	}

	// No tuples seeded, so viewer document:1 for user:anne is currently false.
	sc, err := setupScope(t, eng, lm.Proto, nil)
	if err != nil {
		t.Fatal(err)
	}

	req := CheckReq{User: "user:anne", Relation: "viewer", Object: "document:1"}

	verdict, err := eng.Check(t.Context(), sc, req)
	if err != nil {
		t.Fatal(err)
	}
	if verdict {
		t.Fatal("expected viewer document:1 to be false with no tuples seeded")
	}

	msg, err := nearestMiss(t.Context(), lm, eng, sc, req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if msg == "" {
		t.Fatal("expected a non-empty nearest-miss suggestion via the second (folderB) parent type")
	}
	if !strings.Contains(msg, "folderB") {
		t.Fatalf("expected suggestion to route through folderB; got %q", msg)
	}
}

func TestRenderExplainUnexpectedFalseShowsBranchesAndMiss(t *testing.T) {
	ar := AssertionResult{
		Kind:     "check",
		Subject:  "user:anne can viewer document:1",
		Expected: true,
		Got:      false,
		Passed:   false,
		Explain: &Explain{
			Verdict: false,
			Tree: &ExplainNode{
				Label:  "document:1#viewer",
				Result: false,
				Children: []*ExplainNode{
					{Label: "document:1#owner", Result: false, Reason: "no tuple grants user:anne owner on document:1"},
					{Label: "document:1#editor", Result: false, Reason: "no tuple grants user:anne editor on document:1"},
					{Label: "viewer from parent", Result: false, Reason: "document:1 has no parent tuple"},
				},
			},
			NearestMiss: "a tuple (user:anne, owner, document:1) would grant it",
		},
	}

	var buf bytes.Buffer
	RenderExplain(&buf, ar)
	out := ansi.Strip(buf.String())

	if !strings.Contains(out, "expected: true") || !strings.Contains(out, "got: false") {
		t.Fatalf("expected an expected/got line; got %q", out)
	}
	for _, want := range []string{"document:1#owner", "document:1#editor", "viewer from parent"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain branch label %q; got %q", want, out)
		}
	}
	for _, want := range []string{
		"no tuple grants user:anne owner on document:1",
		"no tuple grants user:anne editor on document:1",
		"document:1 has no parent tuple",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain dead-end reason %q; got %q", want, out)
		}
	}
	if !strings.Contains(out, "nearest miss: a tuple (user:anne, owner, document:1) would grant it") {
		t.Fatalf("expected a nearest-miss line; got %q", out)
	}
}

func TestRenderExplainUnexpectedTrueShowsGrantPath(t *testing.T) {
	ar := AssertionResult{
		Kind:     "check",
		Subject:  "user:anne can viewer document:1",
		Expected: false,
		Got:      true,
		Passed:   false,
		Explain: &Explain{
			Verdict: true,
			Tree: &ExplainNode{
				Label:  "document:1#viewer",
				Result: true,
				Children: []*ExplainNode{
					{Label: "document:1#owner", Result: true, Children: []*ExplainNode{
						{Label: "user:anne", Result: true, Reason: "direct tuple grants user:anne owner on document:1"},
					}},
					{Label: "document:1#editor", Result: false, Reason: "no tuple grants user:anne editor on document:1"},
				},
			},
		},
	}

	var buf bytes.Buffer
	RenderExplain(&buf, ar)
	out := ansi.Strip(buf.String())

	if !strings.Contains(out, "expected: false") || !strings.Contains(out, "got: true") {
		t.Fatalf("expected an expected/got line; got %q", out)
	}
	for _, want := range []string{"document:1#viewer", "document:1#owner", "user:anne", "direct tuple grants user:anne owner on document:1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain granting-path element %q; got %q", want, out)
		}
	}
	if strings.Contains(out, "document:1#editor") {
		t.Fatalf("expected non-granting sibling branch to be omitted; got %q", out)
	}
}

// TestRenderExplainUnexpectedTrueIntersectionKeepsAllArms guards the fix that
// grantingPathNode keeps ALL true children of an intersection node (which grants
// only when every arm is true), instead of collapsing to the first like a union.
// Dropping the other required arms would mis-narrate the "expected false, got
// true" path.
func TestRenderExplainUnexpectedTrueIntersectionKeepsAllArms(t *testing.T) {
	ar := AssertionResult{
		Kind:     "check",
		Subject:  "user:anne can viewer document:1",
		Expected: false,
		Got:      true,
		Passed:   false,
		Explain: &Explain{
			Verdict: true,
			Tree: &ExplainNode{
				Label:  "document:1#viewer",
				Result: true,
				Reason: "intersection",
				Children: []*ExplainNode{
					{Label: "document:1#editor", Result: true, Reason: "direct tuple grants user:anne editor on document:1"},
					{Label: "document:1#member", Result: true, Reason: "direct tuple grants user:anne member on document:1"},
				},
			},
		},
	}

	var buf bytes.Buffer
	RenderExplain(&buf, ar)
	out := ansi.Strip(buf.String())

	for _, want := range []string{"document:1#editor", "document:1#member"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected both intersection arms in output; missing %q; got %q", want, out)
		}
	}
}

func TestRenderExplainListDiff(t *testing.T) {
	ar := AssertionResult{
		Kind:     "list_objects",
		Subject:  "user:anne can viewer",
		Expected: []string{"document:1"},
		Got:      []string{"document:2"},
		Passed:   false,
		Explain: &Explain{
			SetDiff: &SetDiff{
				Unexpected: []string{"document:2"},
				Missing:    []string{"document:1"},
			},
		},
	}

	var buf bytes.Buffer
	RenderExplain(&buf, ar)
	out := ansi.Strip(buf.String())

	if !strings.Contains(out, "+unexpected") || !strings.Contains(out, "document:2") {
		t.Fatalf("expected unexpected entries in output; got %q", out)
	}
	if !strings.Contains(out, "-missing") || !strings.Contains(out, "document:1") {
		t.Fatalf("expected missing entries in output; got %q", out)
	}
}

// TestRenderExplainListDiffExpectedGotCommaSeparated guards that the
// expected/got line for a list_objects/list_users assertion joins the
// []string values with ", " (e.g. "[document:ghost, document:memo]"),
// matching the comma style already used by the +unexpected/-missing lines,
// instead of Go's space-separated %v slice formatting.
func TestRenderExplainListDiffExpectedGotCommaSeparated(t *testing.T) {
	ar := AssertionResult{
		Kind:     "list_objects",
		Subject:  "user:anne can viewer",
		Expected: []string{"document:ghost", "document:memo"},
		Got:      []string{"document:memo"},
		Passed:   false,
		Explain:  &Explain{SetDiff: &SetDiff{Missing: []string{"document:ghost"}}},
	}

	var buf bytes.Buffer
	RenderExplain(&buf, ar)
	out := ansi.Strip(buf.String())

	if !strings.Contains(out, "expected: [document:ghost, document:memo]") {
		t.Fatalf("want comma-separated expected list, got %q", out)
	}
	if !strings.Contains(out, "got: [document:memo]") {
		t.Fatalf("want got list rendered, got %q", out)
	}
}
