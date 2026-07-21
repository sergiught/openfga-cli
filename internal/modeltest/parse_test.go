package modeltest

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestLoadWorkspaceDiscoversManifest(t *testing.T) {
	ws, err := LoadWorkspace("testdata/docs")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if ws.Manifest == nil {
		t.Fatal("expected a manifest")
	}
	if len(ws.TestFiles) != 1 {
		t.Fatalf("want 1 test file, got %d", len(ws.TestFiles))
	}
	if got := len(ws.TestFiles[0].Tests); got != 2 {
		t.Fatalf("want 2 tests, got %d", got)
	}
}

func TestLoadWorkspaceDetectsOfficialFile(t *testing.T) {
	dir := t.TempDir()
	official := []byte("name: x\nmodel_file: ./m.fga\ntuples: []\ntests: []\n")
	p := filepath.Join(dir, "store.fga.yaml")
	os.WriteFile(p, official, 0o600)
	_, err := LoadWorkspace(p)
	if err == nil || !strings.Contains(err.Error(), "official openfga CLI store file") {
		t.Fatalf("want official-file detection error, got %v", err)
	}
}

func TestLoadWorkspaceMissingReferencedModelNamesBothPaths(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte("version: 1\nmodel: ./missing.fga\ntests: []\n"), 0o600)
	_, err := LoadWorkspace(dir)
	if err == nil || !strings.Contains(err.Error(), "./missing.fga") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("error must name declared value and resolved path, got %v", err)
	}
}

func TestLoadWorkspaceMissingModelErrorPathIsAbsoluteForRelativeFileArg(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "ofga.yaml")
	os.WriteFile(manifest, []byte("version: 1\nmodel: ./missing.fga\ntests: []\n"), 0o600)

	t.Chdir(dir)

	_, err := LoadWorkspace("ofga.yaml")
	if err == nil {
		t.Fatal("want error for missing model, got nil")
	}
	if strings.Contains(err.Error(), "resolved to ./missing.fga") || strings.Contains(err.Error(), "resolved to missing.fga") {
		t.Fatalf("want absolute resolved path, got relative: %v", err)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Fatalf("want resolved path to contain temp dir %q, got %v", dir, err)
	}
}

func TestLoadWorkspaceSingleFileWalksUpToManifest(t *testing.T) {
	testFile := filepath.Join("testdata", "docs", "tests", "documents.test.yaml")

	ws, err := LoadWorkspace(testFile)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if ws.Manifest == nil {
		t.Fatal("expected LoadWorkspace to walk up and find the manifest")
	}
	if len(ws.TestFiles) != 1 {
		t.Fatalf("want just the given test file, got %d", len(ws.TestFiles))
	}
	abs, err := filepath.Abs(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if ws.TestFiles[0].Path != abs {
		t.Fatalf("want %s, got %s", abs, ws.TestFiles[0].Path)
	}
	if got := len(ws.TestFiles[0].Tests); got != 2 {
		t.Fatalf("want 2 tests, got %d", got)
	}
}

func TestLoadWorkspaceAllowsDuplicateBasenames(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte("version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "model.fga"), []byte("model\n  schema 1.1\n\ntype user\n"), 0o600)

	testYAML := `model: ./model.fga
tests:
  - name: self-check
    check:
      - user: user:anne
        object: document:1
        assertions: {viewer: true}
`
	os.MkdirAll(filepath.Join(dir, "tests", "a"), 0o755)
	os.MkdirAll(filepath.Join(dir, "tests", "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "tests", "a", "foo.test.yaml"), []byte(testYAML), 0o600)
	os.WriteFile(filepath.Join(dir, "tests", "b", "foo.test.yaml"), []byte(testYAML), 0o600)

	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatalf("duplicate basenames in separate directories should load: %v", err)
	}
	got := []string{ws.TestFileID(ws.TestFiles[0]), ws.TestFileID(ws.TestFiles[1])}
	sort.Strings(got)
	if strings.Join(got, ",") != "a/foo,b/foo" {
		t.Fatalf("test file IDs = %v, want [a/foo b/foo]", got)
	}
}

func TestLoadWorkspaceDedupesOverlappingTestGlobs(t *testing.T) {
	dir := t.TempDir()
	// Two overlapping patterns both match tests/a.test.yaml. The file must be
	// loaded once, not flagged as a self-duplicate.
	os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte("version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n  - \"tests/*.test.yaml\"\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "model.fga"), []byte("model\n  schema 1.1\n\ntype user\n"), 0o600)

	testYAML := "tests:\n  - name: t\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {viewer: true}\n"
	os.MkdirAll(filepath.Join(dir, "tests"), 0o755)
	os.WriteFile(filepath.Join(dir, "tests", "a.test.yaml"), []byte(testYAML), 0o600)

	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatalf("overlapping globs must not false-positive as duplicates: %v", err)
	}
	if len(ws.TestFiles) != 1 {
		t.Fatalf("want the file loaded exactly once, got %d", len(ws.TestFiles))
	}
}

func TestLoadWorkspaceSingleFileIgnoresUnrelatedDuplicateStems(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte("version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "model.fga"), []byte("model\n  schema 1.1\n\ntype user\n"), 0o600)

	testYAML := `model: ./model.fga
tests:
  - name: self-check
    check:
      - user: user:anne
        object: document:1
        assertions: {viewer: true}
`
	os.MkdirAll(filepath.Join(dir, "tests", "a"), 0o755)
	os.MkdirAll(filepath.Join(dir, "tests", "u1"), 0o755)
	os.MkdirAll(filepath.Join(dir, "tests", "u2"), 0o755)
	targetFile := filepath.Join(dir, "tests", "a", "target.test.yaml")
	os.WriteFile(targetFile, []byte(testYAML), 0o600)
	os.WriteFile(filepath.Join(dir, "tests", "u1", "dup.test.yaml"), []byte(testYAML), 0o600)
	os.WriteFile(filepath.Join(dir, "tests", "u2", "dup.test.yaml"), []byte(testYAML), 0o600)

	ws, err := LoadWorkspace(targetFile)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(ws.TestFiles) != 1 {
		t.Fatalf("want just the given test file, got %d", len(ws.TestFiles))
	}
	abs, err := filepath.Abs(targetFile)
	if err != nil {
		t.Fatal(err)
	}
	if ws.TestFiles[0].Path != abs {
		t.Fatalf("want %s, got %s", abs, ws.TestFiles[0].Path)
	}
}

func TestLoadWorkspaceAllowsDifferentlyNamedTestFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte("version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "model.fga"), []byte("model\n  schema 1.1\n\ntype user\n"), 0o600)

	testYAML := `model: ./model.fga
tests:
  - name: self-check
    check:
      - user: user:anne
        object: document:1
        assertions: {viewer: true}
`
	os.MkdirAll(filepath.Join(dir, "tests", "a"), 0o755)
	os.MkdirAll(filepath.Join(dir, "tests", "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "tests", "a", "foo.test.yaml"), []byte(testYAML), 0o600)
	os.WriteFile(filepath.Join(dir, "tests", "b", "bar.test.yaml"), []byte(testYAML), 0o600)

	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(ws.TestFiles) != 2 {
		t.Fatalf("want 2 test files, got %d", len(ws.TestFiles))
	}
}

func TestLoadWorkspaceStandaloneTestFileWithNoManifestAboveStaysBare(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "standalone.test.yaml")
	standalone := `model: ./model.fga
tests:
  - name: self-check
    check:
      - user: user:anne
        object: document:1
        assertions: {viewer: true}
`
	os.WriteFile(testFile, []byte(standalone), 0o600)

	ws, err := LoadWorkspace(testFile)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if ws.Manifest != nil {
		t.Fatal("expected no manifest for a standalone file with no ofga.yaml above it")
	}
	if len(ws.TestFiles) != 1 {
		t.Fatalf("want 1 test file, got %d", len(ws.TestFiles))
	}
	if got := len(ws.TestFiles[0].Tests); got != 1 {
		t.Fatalf("want 1 test, got %d", got)
	}
}

func TestExpandFixturesRegistersByNameAcrossNestedDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("fixtures/org.yaml", "- {user: user:a, relation: admin, object: org:1}\n")
	writeFile("fixtures/sub/docs.yaml", "- {user: user:b, relation: viewer, object: doc:1}\n")
	writeFile("fixtures/ignore.txt", "not a fixture\n") // excluded by the *.yaml glob

	reg, err := expandFixtures(dir, []string{"fixtures/**/*.yaml"})
	if err != nil {
		t.Fatalf("expandFixtures: %v", err)
	}
	if len(reg) != 2 {
		t.Fatalf("want 2 registered fixtures, got %d: %v", len(reg), reg)
	}
	if _, ok := reg["org"]; !ok {
		t.Errorf("expected 'org' registered (direct child), got %v", reg)
	}
	if _, ok := reg["sub/docs"]; !ok {
		t.Errorf("expected qualified nested fixture registered, got %v", reg)
	}
	ws := &Workspace{Root: dir, Fixtures: reg}
	tf := &TestFile{Path: filepath.Join(dir, "tests", "test.test.yaml")}
	if got, err := resolveFixtureRef(ws, tf, "docs"); err != nil || got != reg["sub/docs"] {
		t.Errorf("unique bare basename should resolve for compatibility: got %q, err=%v", got, err)
	}
}

func TestExpandFixturesAllowsDuplicateBasenamesWithQualifiedRefs(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"fixtures/a/org.yaml", "fixtures/b/org.yaml"} {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("- {user: user:a, relation: x, object: y:1}\n"), 0o600)
	}
	reg, err := expandFixtures(dir, []string{"fixtures/**/*.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if reg["a/org"] == "" || reg["b/org"] == "" {
		t.Fatalf("qualified fixture refs missing: %v", reg)
	}
	ws := &Workspace{Root: dir, Fixtures: reg}
	tf := &TestFile{Path: filepath.Join(dir, "tests", "test.test.yaml")}
	if _, err := resolveFixtureRef(ws, tf, "org"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("bare duplicate fixture ref should be actionable: %v", err)
	}
}

func TestResolveFixtureRefUnregisteredNamesRegistered(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "fixtures/org.yaml", "- {user: user:a, relation: admin, object: org:1}\n")
	ws := newWorkspace(root)
	tf := &TestFile{Path: filepath.Join(root, "tests/t.test.yaml")}

	if _, err := resolveFixtureRef(ws, tf, "org"); err != nil {
		t.Fatalf("registered fixture should resolve: %v", err)
	}
	_, err := resolveFixtureRef(ws, tf, "missing")
	if err == nil {
		t.Fatal("want error for unregistered fixture, got nil")
	}
	if !strings.Contains(err.Error(), "not registered") || !strings.Contains(err.Error(), "org") {
		t.Errorf("error should say not registered and list known fixtures, got: %v", err)
	}
}

func TestFixturesAndTuplesKeywordsInterchangeable(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("model.fga", "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n")
	// manifest uses `tuples:` instead of `fixtures:` to register fixture globs
	write("ofga.yaml", "version: 1\nmodel: ./model.fga\ntuples:\n  - \"fixtures/**/*.yaml\"\ntests:\n  - \"tests/**/*.test.yaml\"\n")
	write("fixtures/grants.yaml", "- {user: user:anne, relation: viewer, object: doc:1}\n")
	// test file uses top-level `tuples:` instead of `fixtures:` to reference it
	write("tests/t.test.yaml", "tuples: [grants]\ntests:\n  - name: t\n    check:\n      - user: user:anne\n        object: doc:1\n        assertions: {viewer: true}\n")

	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}
	// manifest `tuples:` registered the fixture
	if _, ok := ws.Fixtures["grants"]; !ok {
		t.Fatalf("manifest `tuples` should register fixtures like `fixtures`, got %v", ws.Fixtures)
	}
	// file-level `tuples:` folded into the file's fixture references
	if len(ws.TestFiles) != 1 || len(ws.TestFiles[0].Fixtures) != 1 || ws.TestFiles[0].Fixtures[0] != "grants" {
		t.Fatalf("file-level `tuples` should alias `fixtures`, got %+v", ws.TestFiles)
	}

	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	res, err := Run(context.Background(), ws, Options{Engine: eng})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Passed != 1 || res.Summary.Failed != 0 {
		t.Fatalf("expected the check to pass via `tuples`-registered fixture, got %+v", res.Summary)
	}
}

func TestTestLevelFixturesTuplesAcceptRefsAndInlineObjects(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("model.fga", "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n    define editor: [user]\n")
	write("ofga.yaml", "version: 1\nmodel: ./model.fga\nfixtures:\n  - \"fixtures/**/*.yaml\"\ntests:\n  - \"tests/**/*.test.yaml\"\n")
	write("fixtures/grants.yaml", "- {user: user:anne, relation: viewer, object: doc:1}\n")
	// In this test: `fixtures:` holds an INLINE tuple (object), and `tuples:`
	// holds a REFERENCE (string) — the reverse of the conventional split.
	write("tests/t.test.yaml", `tests:
  - name: mixed
    fixtures:
      - {user: user:bob, relation: editor, object: doc:1}
    tuples:
      - grants
    check:
      - user: user:anne
        object: doc:1
        assertions: {viewer: true}
      - user: user:bob
        object: doc:1
        assertions: {editor: true}
`)

	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	res, err := Run(context.Background(), ws, Options{Engine: eng})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Passed != 1 || res.Summary.Failed != 0 {
		t.Fatalf("inline-in-fixtures and ref-in-tuples should both resolve; got %+v", res.Summary)
	}
}

func TestCompactTupleStringsEverywhere(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("model.fga", "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n    define editor: [user]\n    define owner: [user]\n")
	write("ofga.yaml", "version: 1\nmodel: ./model.fga\nfixtures:\n  - \"fixtures/**/*.yaml\"\ntests:\n  - \"tests/**/*.test.yaml\"\n")
	// compact form in a fixture file
	write("fixtures/grants.yaml", "- user:anne viewer doc:1\n- user:carol owner doc:1\n")
	// compact form in test-level tuples and in contextual_tuples
	write("tests/t.test.yaml", `tests:
  - name: compact
    fixtures: [grants]
    tuples:
      - user:bob editor doc:1
    check:
      - user: user:anne
        object: doc:1
        assertions: {viewer: true}
      - user: user:bob
        object: doc:1
        assertions: {editor: true}
      - user: user:dave
        object: doc:1
        contextual_tuples:
          - user:dave viewer doc:1
        assertions: {viewer: true}
`)

	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	res, err := Run(context.Background(), ws, Options{Engine: eng})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Passed != 1 || res.Summary.Failed != 0 {
		t.Fatalf("compact tuples in fixture/tuples/contextual_tuples should all resolve, got %+v", res.Summary)
	}
}

func TestCompactTupleRejectsWrongFieldCount(t *testing.T) {
	_, err := parseCompactTuple("user:anne viewer")
	if err == nil {
		t.Fatal("two-field compact tuple should error")
	}
	if !strings.Contains(err.Error(), "three") {
		t.Errorf("error should explain the three-field form, got: %v", err)
	}
}

func TestListUsersAssertionAcceptsFlatAndWrappedForms(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("model.fga", "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n")
	write("ofga.yaml", "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n")
	write("tests/t.test.yaml", `tests:
  - name: flat-and-wrapped
    tuples:
      - user:anne viewer doc:1
    list_users:
      - object: doc:1
        user_filter: [{type: user}]
        assertions:
          viewer: [user:anne]              # flat form
      - object: doc:1
        user_filter: [{type: user}]
        assertions:
          viewer: {users: [user:anne]}     # wrapped form
`)

	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	res, err := Run(context.Background(), ws, Options{Engine: eng})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Passed != 1 || res.Summary.Failed != 0 {
		t.Fatalf("both list_users assertion forms should resolve, got %+v", res.Summary)
	}
}

func TestLoadWorkspaceWithFlagsManifestFreeAndOverride(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("model.fga", "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n")
	write("fixtures/grants.yaml", "- user:anne viewer doc:1\n")
	write("tests/a.test.yaml", "tests:\n  - name: a\n    fixtures: [grants]\n    check:\n      - user: user:anne\n        object: doc:1\n        assertions: {viewer: true}\n")
	write("tests/b.test.yaml", "tests:\n  - name: b\n    check:\n      - user: user:x\n        object: doc:1\n        assertions: {viewer: false}\n")

	// Manifest-free: no ofga.yaml exists; everything comes from options.
	ws, err := LoadWorkspaceWith(dir, WorkspaceOptions{
		Model:    "model.fga",
		Fixtures: []string{"fixtures/**/*.yaml"},
		Tests:    []string{"tests/**/*.test.yaml"},
	})
	if err != nil {
		t.Fatalf("manifest-free load: %v", err)
	}
	if len(ws.TestFiles) != 2 {
		t.Fatalf("want 2 test files, got %d", len(ws.TestFiles))
	}
	if _, ok := ws.Fixtures["grants"]; !ok {
		t.Fatalf("fixtures glob should register grants, got %v", ws.Fixtures)
	}

	// Manifest-free requires model + tests.
	if _, err := LoadWorkspaceWith(dir, WorkspaceOptions{Model: "model.fga"}); err == nil {
		t.Fatal("manifest-free without --tests should error")
	}

	// Override: a manifest exists but --tests narrows it to one file.
	write("ofga.yaml", "version: 1\nmodel: ./model.fga\nfixtures:\n  - \"fixtures/**/*.yaml\"\ntests:\n  - \"tests/**/*.test.yaml\"\n")
	ws, err = LoadWorkspaceWith(dir, WorkspaceOptions{Tests: []string{"tests/a.test.yaml"}})
	if err != nil {
		t.Fatalf("override load: %v", err)
	}
	if len(ws.TestFiles) != 1 || FileStem(ws.TestFiles[0].Path) != "a" {
		t.Fatalf("--tests should override the manifest to just a.test.yaml, got %+v", ws.TestFiles)
	}
}

func TestCheckGroupingUsersAndObjects(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("model.fga", "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n")
	write("ofga.yaml", "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n")
	write("tests/t.test.yaml", `tests:
  - name: grouped
    tuples:
      - user:anne viewer doc:1
      - user:anne viewer doc:2
      - user:bob viewer doc:1
      - user:bob viewer doc:2
    check:
      - users: [user:anne, user:bob]
        objects: [doc:1, doc:2]
        assertions: {viewer: true}
`)

	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	res, err := Run(context.Background(), ws, Options{Engine: eng})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 2 users x 2 objects x 1 relation = 4 assertions, all in one test.
	if len(res.Tests) != 1 || len(res.Tests[0].Assertions) != 4 {
		t.Fatalf("grouped check should expand to 4 assertions, got %d", len(res.Tests[0].Assertions))
	}
	if !res.Tests[0].Passed {
		t.Fatalf("grouped check should pass: %+v", res.Tests[0])
	}
}

func TestCheckGroupingRejectsSingularAndPluralTogether(t *testing.T) {
	_, _, err := CheckCase{User: "user:a", Users: []string{"user:b"}, Assertions: map[string]bool{"viewer": true}}.subjects()
	if err == nil || !strings.Contains(err.Error(), "user") {
		t.Fatalf("setting both user and users should error, got %v", err)
	}
	_, _, err = CheckCase{Object: "doc:1", Objects: []string{"doc:2"}}.subjects()
	if err == nil || !strings.Contains(err.Error(), "object") {
		t.Fatalf("setting both object and objects should error, got %v", err)
	}
}

func TestSchemaRejectsCheckCaseWithoutSubject(t *testing.T) {
	// assertions present but neither user/users nor object/objects.
	bad := `{"tests":[{"name":"t","check":[{"assertions":{"viewer":true}}]}]}`
	if err := validate(docTestFile, []byte(bad)); err == nil {
		t.Fatal("check case without a user/object subject must be rejected")
	}
}

func TestTwoFieldScalarTupleYieldsCompactTupleError(t *testing.T) {
	// A two-token scalar is an attempted compact tuple with a dropped field, not
	// a fixture reference; it must fail with the clear three-field error at load,
	// not be silently treated as a Ref (which would later fail with a misleading
	// "fixture not registered").
	raw := []byte("tests:\n  - name: t\n    tuples:\n      - user:anne viewer\n    check:\n      - user: user:anne\n        object: doc:1\n        assertions: {viewer: true}\n")
	_, err := decodeTestFile("mem.test.yaml", raw)
	if err == nil {
		t.Fatal("two-field scalar tuple should error")
	}
	if !strings.Contains(err.Error(), "three") {
		t.Fatalf("want compact-tuple error explaining the three-field form, got: %v", err)
	}
	if strings.Contains(err.Error(), "not registered") {
		t.Fatalf("must not be misreported as a fixture-ref error, got: %v", err)
	}
}

func TestDecodeRejectsCaseWithEmptyAssertions(t *testing.T) {
	// The schema requires an `assertions` key but permits {}; an empty block
	// would run zero assertions and pass vacuously, so it's rejected at load.
	cases := map[string]string{
		"check":        "tests:\n  - name: t\n    check:\n      - user: user:anne\n        object: doc:1\n        assertions: {}\n",
		"list_objects": "tests:\n  - name: t\n    list_objects:\n      - user: user:anne\n        type: doc\n        assertions: {}\n",
		"list_users":   "tests:\n  - name: t\n    list_users:\n      - object: doc:1\n        user_filter: [{type: user}]\n        assertions: {}\n",
	}
	for kind, doc := range cases {
		t.Run(kind, func(t *testing.T) {
			_, err := decodeTestFile("mem.test.yaml", []byte(doc))
			if err == nil {
				t.Fatalf("%s case with empty assertions should be rejected", kind)
			}
			if !strings.Contains(err.Error(), "assertions") {
				t.Fatalf("error should point at the empty assertions block, got: %v", err)
			}
		})
	}
}

func TestLoadWorkspaceWithFlagsRestrictsToSingleTestFileArg(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("model.fga", "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n")
	write("ofga.yaml", "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n")
	write("tests/a.test.yaml", "tests:\n  - name: a\n    check:\n      - user: user:anne\n        object: doc:1\n        assertions: {viewer: false}\n")
	write("tests/b.test.yaml", "tests:\n  - name: b\n    check:\n      - user: user:bob\n        object: doc:1\n        assertions: {viewer: false}\n")

	// A positional .test.yaml path with a flag set must still restrict the run
	// to that one file, not expand the whole manifest.
	ws, err := LoadWorkspaceWith(filepath.Join(dir, "tests", "a.test.yaml"), WorkspaceOptions{Model: "model.fga"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(ws.TestFiles) != 1 || FileStem(ws.TestFiles[0].Path) != "a" {
		t.Fatalf("a positional test-file arg with flags should run only that file, got %+v", ws.TestFiles)
	}
}

func TestVersionlessManifestWithTuplesAliasIsNotOfficialFile(t *testing.T) {
	dir := t.TempDir()
	// `tuples` is a legitimate manifest alias for `fixtures`; a versionless
	// manifest using it should get the accurate "version required" schema error,
	// not be misdiagnosed as an official openfga store file.
	os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte("model: ./m.fga\ntuples:\n  - \"fixtures/**/*.yaml\"\ntests: []\n"), 0o600)
	_, err := LoadWorkspace(dir)
	if err == nil {
		t.Fatal("versionless manifest must error")
	}
	if strings.Contains(err.Error(), "official openfga CLI store file") {
		t.Fatalf("manifest using the tuples alias must not be reported as an official store file, got: %v", err)
	}
	if !strings.Contains(err.Error(), "version") {
		t.Fatalf("want a missing-version error, got: %v", err)
	}
}

func TestCheckGroupingDedupesUsersAndObjects(t *testing.T) {
	users, objects, err := CheckCase{
		Users:      []string{"user:anne", "user:anne", "user:bob"},
		Objects:    []string{"doc:1", "doc:1"},
		Assertions: map[string]bool{"viewer": true},
	}.subjects()
	if err != nil {
		t.Fatalf("subjects: %v", err)
	}
	if len(users) != 2 || users[0] != "user:anne" || users[1] != "user:bob" {
		t.Fatalf("users should be deduped preserving order, got %v", users)
	}
	if len(objects) != 1 || objects[0] != "doc:1" {
		t.Fatalf("objects should be deduped, got %v", objects)
	}
}

func TestCoverageDiffReportsAddedBranches(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Current model has viewer (tested) and editor (NOT tested).
	write("model.fga", "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n    define editor: [user]\n")
	write("ofga.yaml", "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n")
	write("tests/t.test.yaml", "tests:\n  - name: t\n    tuples: [user:anne viewer doc:1]\n    check:\n      - user: user:anne\n        object: doc:1\n        assertions: {viewer: true}\n")

	// Base model only had viewer, so editor's branch is "added".
	base, err := LoadModelBytes([]byte("model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n"))
	if err != nil {
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

	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true, DiffBaseModel: base, DiffBaseName: "base"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Coverage == nil || res.Coverage.Diff == nil {
		t.Fatal("expected a coverage diff")
	}
	d := res.Coverage.Diff
	if len(d.Added) != 1 || d.Added[0].Type != "doc" || d.Added[0].Relation != "editor" {
		t.Fatalf("expected exactly doc.editor added, got %+v", d.Added)
	}
	if d.Uncovered != 1 || d.Added[0].Covered {
		t.Fatalf("added editor branch should be uncovered, got %+v", d)
	}
}
