package modeltest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixtureFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func newWorkspace(root string) *Workspace {
	// Register every fixture under fixtures/ by name, mirroring a manifest whose
	// `fixtures` glob is fixtures/**/*.yaml. Callers write their fixture files
	// before calling this.
	reg, err := expandFixtures(root, []string{"fixtures/**/*.yaml"})
	if err != nil {
		panic(err)
	}
	return &Workspace{
		Root: root,
		Manifest: &Manifest{
			Fixtures: []string{"fixtures/**/*.yaml"},
			path:     filepath.Join(root, "ofga.yaml"),
		},
		Fixtures: reg,
	}
}

// inlineTuples wraps inline tuples as TupleItem entries for a test's Tuples list.
func inlineTuples(tks ...TupleKey) []TupleItem {
	items := make([]TupleItem, len(tks))
	for i := range tks {
		tk := tks[i]
		items[i] = TupleItem{Tuple: &tk}
	}
	return items
}

func TestFixtureLoadersRejectUnknownAndEmptyFields(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		file    string
		content string
		wantErr string
	}{
		{
			name:    "yaml misspelled key",
			file:    "typo.yaml",
			content: "- user: user:anne\n  relaton: viewer\n  object: document:1\n",
			wantErr: "relaton",
		},
		{
			name:    "yaml empty relation",
			file:    "empty.yaml",
			content: "- user: user:anne\n  relation: \"\"\n  object: document:1\n",
			wantErr: "missing required field(s): relation",
		},
		{
			name:    "jsonl unknown field",
			file:    "typo.jsonl",
			content: `{"user":"user:anne","relaton":"viewer","object":"document:1"}` + "\n",
			wantErr: "relaton",
		},
		{
			name:    "jsonl missing object",
			file:    "missing.jsonl",
			content: `{"user":"user:anne","relation":"viewer"}` + "\n",
			wantErr: "missing required field(s): object",
		},
		{
			name:    "csv empty user",
			file:    "empty.csv",
			content: "user,relation,object\n,viewer,document:1\n",
			wantErr: "missing required field(s): user",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFixtureFile(t, dir, tc.file, tc.content)
			_, err := loadFixtureFile(path)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestFixtureConditionRequiresName(t *testing.T) {
	// A referenced fixture bypasses JSON-schema validation; a condition with a
	// context but no name must still be rejected at load, not deferred to an
	// opaque engine error.
	dir := t.TempDir()
	path := writeFixtureFile(t, dir, "cond.yaml", "- user: user:anne\n  relation: viewer\n  object: doc:1\n  condition:\n    context: {region: us}\n")
	_, err := loadFixtureFile(path)
	if err == nil {
		t.Fatal("fixture condition without a name should be rejected")
	}
	if !strings.Contains(err.Error(), "condition") {
		t.Fatalf("error should mention the condition, got: %v", err)
	}
}

func TestFixtureLoadsMultipleYAMLDocuments(t *testing.T) {
	// A `---`-separated multi-document fixture file must load every document's
	// tuples, not silently stop after the first.
	dir := t.TempDir()
	content := "- {user: user:anne, relation: viewer, object: doc:1}\n---\n- {user: user:bob, relation: viewer, object: doc:2}\n"
	path := writeFixtureFile(t, dir, "multi.yaml", content)
	tuples, err := loadFixtureFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(tuples) != 2 {
		t.Fatalf("want 2 tuples across both documents, got %d: %+v", len(tuples), tuples)
	}
	if tuples[0].User != "user:anne" || tuples[1].User != "user:bob" {
		t.Fatalf("want both documents' tuples in order, got %+v", tuples)
	}
}

func TestFixtureUnknownFieldErrorPhrasing(t *testing.T) {
	// The mapping-form unknown-key error should describe an unknown field.
	dir := t.TempDir()
	path := writeFixtureFile(t, dir, "typo.yaml", "- user: user:anne\n  relaton: viewer\n  object: doc:1\n")
	_, err := loadFixtureFile(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("want an 'unknown field' error, got: %v", err)
	}
}

func TestResolveMergesFileAndInlineTuples(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "fixtures/core-users.yaml", `
- user: user:anne
  relation: owner
  object: document:1
`)

	ws := newWorkspace(root)
	tf := &TestFile{Path: filepath.Join(root, "tests/documents.test.yaml"), Fixtures: []string{"core-users"}}
	tt := Test{
		Name:   "owner-is-viewer",
		Tuples: inlineTuples(TupleKey{User: "user:bob", Relation: "viewer", Object: "document:2"}),
	}

	got, err := resolveFixtures(ws, tf, tt, false, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 tuples, got %d: %+v", len(got), got)
	}
	if got[0].User != "user:anne" || got[0].Object != "document:1" {
		t.Fatalf("want fixture tuple first, got %+v", got[0])
	}
	if got[1].User != "user:bob" || got[1].Object != "document:2" {
		t.Fatalf("want inline tuple second, got %+v", got[1])
	}
}

func TestResolveExactDuplicateIsHardError(t *testing.T) {
	root := t.TempDir()
	fixturePath := writeFixtureFile(t, root, "fixtures/core-users.yaml", `
- user: user:anne
  relation: owner
  object: document:1
`)

	ws := newWorkspace(root)
	tf := &TestFile{Path: filepath.Join(root, "tests/documents.test.yaml"), Fixtures: []string{"core-users"}}
	tt := Test{
		Name:   "dup-test",
		Tuples: inlineTuples(TupleKey{User: "user:anne", Relation: "owner", Object: "document:1"}),
	}

	_, err := resolveFixtures(ws, tf, tt, false, nil)
	if err == nil {
		t.Fatal("want error for exact duplicate, got nil")
	}
	if !strings.Contains(err.Error(), fixturePath) {
		t.Fatalf("want error to name fixture source %q, got %v", fixturePath, err)
	}
	if !strings.Contains(err.Error(), "dup-test inline") {
		t.Fatalf("want error to name inline source, got %v", err)
	}
}

func TestResolveDedupeFlagCollapses(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "fixtures/core-users.yaml", `
- user: user:anne
  relation: owner
  object: document:1
`)

	ws := newWorkspace(root)
	tf := &TestFile{Path: filepath.Join(root, "tests/documents.test.yaml"), Fixtures: []string{"core-users"}}
	tt := Test{
		Name:   "dup-test",
		Tuples: inlineTuples(TupleKey{User: "user:anne", Relation: "owner", Object: "document:1"}),
	}

	got, err := resolveFixtures(ws, tf, tt, true, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 tuple after dedupe, got %d: %+v", len(got), got)
	}
}

func TestResolveSameTupleDifferentConditionErrors(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "fixtures/core-users.yaml", `
- user: user:anne
  relation: owner
  object: document:1
  condition:
    name: in_region
    context:
      region: us
`)

	ws := newWorkspace(root)
	tf := &TestFile{Path: filepath.Join(root, "tests/documents.test.yaml"), Fixtures: []string{"core-users"}}
	tt := Test{
		Name: "conflict-test",
		Tuples: inlineTuples(TupleKey{
			User: "user:anne", Relation: "owner", Object: "document:1",
			Condition: &TupleCond{Name: "in_region", Context: map[string]any{"region": "eu"}},
		}),
	}

	// Same error even with dedupe: true, since it's a condition conflict, not
	// an exact duplicate.
	_, err := resolveFixtures(ws, tf, tt, true, nil)
	if err == nil {
		t.Fatal("want error for conflicting condition, got nil")
	}
	if !strings.Contains(err.Error(), "conflict-test inline") {
		t.Fatalf("want error to name inline source, got %v", err)
	}
}

// TestResolveCSVAndJSONLFixturesLoadRealTuples covers the untested CSV and
// JSONL fixture loaders end to end (LoadWorkspace + Run against the embedded
// engine), not just parsing: a workspace's test references both a .csv and a
// .jsonl fixture, and every check below only passes if the loader actually
// produced the tuple it depends on.
//
//   - core.csv has a header row ("user,relation,object") that must be
//     skipped, a plain 3-column data row (anne/owner), and a 4-column row
//     using the optional condition_name column with no condition_context
//     (carol/viewer, condition in_business_hours) — the check context, not
//     the tuple, supplies the condition parameter.
//   - core.jsonl has one TupleKey JSON object per line: a plain grant
//     (bob/owner) and a grant carrying an inline condition name+context
//     (dave/viewer).
func TestResolveCSVAndJSONLFixturesLoadRealTuples(t *testing.T) {
	dir := t.TempDir()

	modelDSL := `model
  schema 1.1

type user

type document
  relations
    define owner: [user]
    define viewer: [user with in_business_hours] or owner

condition in_business_hours(current_hour: int) {
  current_hour >= 9 && current_hour <= 17
}
`
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(modelDSL), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := "version: 1\nmodel: ./model.fga\nfixtures:\n  - \"fixtures/**/*\"\ntests:\n  - \"tests/**/*.test.yaml\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	csvContent := " User,RELATION,Object \n" +
		"user:anne,owner,document:1\n" +
		"user:carol,viewer,document:3,in_business_hours\n"
	writeFixtureFile(t, dir, "fixtures/core-csv.csv", csvContent)

	jsonlContent := `{"user":"user:bob","relation":"owner","object":"document:2"}` + "\n" +
		`{"user":"user:dave","relation":"viewer","object":"document:4","condition":{"name":"in_business_hours","context":{"current_hour":10}}}` + "\n"
	writeFixtureFile(t, dir, "fixtures/core-jsonl.jsonl", jsonlContent)

	testFile := "fixtures: [core-csv, core-jsonl]\n" +
		"tests:\n" +
		"  - name: csv-and-jsonl-fixtures\n" +
		"    check:\n" +
		"      - user: user:anne\n" +
		"        object: document:1\n" +
		"        assertions: {owner: true, viewer: true}\n" +
		"      - user: user:bob\n" +
		"        object: document:2\n" +
		"        assertions: {owner: true, viewer: true}\n" +
		"      - user: user:carol\n" +
		"        object: document:3\n" +
		"        context: {current_hour: 10}\n" +
		"        assertions: {viewer: true}\n" +
		"      - user: user:dave\n" +
		"        object: document:4\n" +
		"        assertions: {viewer: true}\n"
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "docs.test.yaml"), []byte(testFile), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Failed != 0 {
		t.Fatalf("want 0 failed, got %d: %+v", res.Summary.Failed, res.Tests)
	}
	if res.Summary.MatchedTests != 1 {
		t.Fatalf("want 1 matched test, got %d", res.Summary.MatchedTests)
	}
	for _, tr := range res.Tests {
		for _, ar := range tr.Assertions {
			if !ar.Passed {
				t.Errorf("assertion %s: got %v, want %v (csv/jsonl fixture tuple missing or mis-parsed)", ar.Subject, ar.Got, ar.Expected)
			}
		}
	}
}
