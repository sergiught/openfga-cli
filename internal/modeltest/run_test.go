package modeltest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeExplainWorkspace writes a workspace whose single test asserts
// user:anne can viewer document:1 without ever granting owner (so the check
// is a denial, expected true / got false) — good material for both a
// resolution tree and a nearest-miss suggestion.
func writeExplainWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	model := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define owner: [user]\n    define viewer: [user] or owner\n"
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}

	testFile := "tests:\n  - name: anne-is-not-viewer\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {viewer: true}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "docs.test.yaml"), []byte(testFile), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

// writeListUsersContextWorkspace writes a workspace whose viewer relation is
// conditioned on business hours, with two list_users assertions against the
// same tuple: one whose context satisfies the condition (anne is a viewer)
// and one whose context does not (anne is not a viewer). Both only pass if
// ListUsersCase.Context actually reaches the engine.
func writeListUsersContextWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	model := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user with in_business_hours]\n\ncondition in_business_hours(current_hour: int) {\n  current_hour >= 9 && current_hour <= 17\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}

	testFile := "" +
		"tests:\n" +
		"  - name: within-business-hours\n" +
		"    tuples:\n" +
		"      - user: user:anne\n" +
		"        relation: viewer\n" +
		"        object: document:1\n" +
		"        condition: {name: in_business_hours}\n" +
		"    list_users:\n" +
		"      - object: document:1\n" +
		"        user_filter:\n" +
		"          - type: user\n" +
		"        context: {current_hour: 10}\n" +
		"        assertions:\n" +
		"          viewer: {users: [user:anne]}\n" +
		"  - name: outside-business-hours\n" +
		"    tuples:\n" +
		"      - user: user:anne\n" +
		"        relation: viewer\n" +
		"        object: document:1\n" +
		"        condition: {name: in_business_hours}\n" +
		"    list_users:\n" +
		"      - object: document:1\n" +
		"        user_filter:\n" +
		"          - type: user\n" +
		"        context: {current_hour: 20}\n" +
		"        assertions:\n" +
		"          viewer: {users: []}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "docs.test.yaml"), []byte(testFile), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestRunListUsersWithContext(t *testing.T) {
	dir := writeListUsersContextWorkspace(t)
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
	if res.Summary.MatchedTests != 2 {
		t.Fatalf("want 2 matched, got %d", res.Summary.MatchedTests)
	}
}

func TestRunPopulatesExplainOnFailure(t *testing.T) {
	dir := writeExplainWorkspace(t)
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
	if res.Summary.Failed != 1 {
		t.Fatalf("want 1 failed, got %d", res.Summary.Failed)
	}

	ar := res.Tests[0].Assertions[0]
	if ar.Passed {
		t.Fatal("expected assertion to fail")
	}
	if ar.Explain == nil {
		t.Fatal("want Explain populated on a failed assertion")
	}
	if ar.Explain.Tree == nil {
		t.Fatal("want a non-nil Explain.Tree")
	}
	if ar.Explain.NearestMiss == "" {
		t.Fatal("want a non-empty Explain.NearestMiss for a denial")
	}
}

func TestRunDoesNotPopulateExplainOnPassByDefault(t *testing.T) {
	ws, err := LoadWorkspace("testdata/docs")
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
	for _, tr := range res.Tests {
		for _, ar := range tr.Assertions {
			if !ar.Passed {
				t.Fatalf("expected all assertions to pass in testdata/docs, got failure: %+v", ar)
			}
			if ar.Explain != nil {
				t.Fatalf("want Explain nil on a passing assertion by default, got %+v", ar.Explain)
			}
		}
	}
}

func TestRunPopulatesExplainOnPassWhenExplainFull(t *testing.T) {
	ws, err := LoadWorkspace("testdata/docs")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Explain: "full"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tr := range res.Tests {
		for _, ar := range tr.Assertions {
			if !ar.Passed {
				t.Fatalf("expected all assertions to pass in testdata/docs, got failure: %+v", ar)
			}
			if ar.Explain == nil {
				t.Fatalf("want Explain populated on a passing assertion when Explain==\"full\", got nil for %+v", ar)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected at least one assertion")
	}
}

func TestRunAllPassesDocsWorkspace(t *testing.T) {
	ws, err := LoadWorkspace("testdata/docs")
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
		t.Fatalf("want 0 failed, got %d", res.Summary.Failed)
	}
	if res.Summary.MatchedTests != 2 {
		t.Fatalf("want 2 matched, got %d", res.Summary.MatchedTests)
	}
}

// TestRunPopulatesDurations guards review finding S6: a run must report real
// per-test and total durations, not the zero value. Exact values would be
// flaky (wall-clock, and per-test durations aren't summable since tests run
// concurrently — see Run's DurationMs comment), so this only asserts the
// fields are populated (non-negative) and serialize under the documented
// duration_ms key.
func TestRunPopulatesDurations(t *testing.T) {
	ws, err := LoadWorkspace("testdata/docs")
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
	if res.Summary.DurationMs < 0 {
		t.Fatalf("want Summary.DurationMs >= 0, got %d", res.Summary.DurationMs)
	}
	if len(res.Tests) == 0 {
		t.Fatal("want at least one test result")
	}
	for _, tr := range res.Tests {
		if tr.DurationMs < 0 {
			t.Fatalf("test %s: want DurationMs >= 0, got %d", tr.Name, tr.DurationMs)
		}
	}

	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"duration_ms"`) {
		t.Fatalf("want serialized result to contain duration_ms, got %s", b)
	}
}

// TestRunPopulatesDurationsWithCoverage guards the fix that
// Summary.DurationMs is stamped after buildCoverage, so a --coverage run's
// "whole run" total includes coverage aggregation rather than under-counting
// it. Exact values would be flaky, so this only asserts the field is
// populated (non-negative).
func TestRunPopulatesDurationsWithCoverage(t *testing.T) {
	ws, err := LoadWorkspace("testdata/docs")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.DurationMs < 0 {
		t.Fatalf("want Summary.DurationMs >= 0, got %d", res.Summary.DurationMs)
	}
}

// TestMatchRun is a direct table-driven unit test of matchRun's selection
// logic: exact "<stem>/<name>", a "<stem>/*" glob, a bare (no "/")
// wildcard falling back to name-only matching, and a bare exact name
// matching regardless of stem.
func TestMatchRun(t *testing.T) {
	cases := []struct {
		name               string
		pattern            string
		fileStem, testName string
		want               bool
	}{
		{"empty pattern matches everything", "", "documents", "owner-is-viewer", true},
		{"exact stem/name matches", "documents/owner-is-viewer", "documents", "owner-is-viewer", true},
		{"exact stem/name wrong name", "documents/owner-is-viewer", "documents", "stranger-denied", false},
		{"exact stem/name wrong stem", "documents/owner-is-viewer", "other", "owner-is-viewer", false},
		{"stem/* glob matches any name in that stem", "documents/*", "documents", "owner-is-viewer", true},
		{"stem/* glob matches a different name in that stem", "documents/*", "documents", "stranger-denied", true},
		{"stem/* glob does not match a different stem", "documents/*", "other", "owner-is-viewer", false},
		{"bare wildcard falls back to name-only match", "*", "documents", "owner-is-viewer", true},
		{"bare exact name matches regardless of stem", "owner-is-viewer", "documents", "owner-is-viewer", true},
		{"bare exact name matches under a different stem too", "owner-is-viewer", "other-file", "owner-is-viewer", true},
		{"bare exact name does not match a different name", "owner-is-viewer", "documents", "stranger-denied", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchRun(tc.pattern, tc.fileStem, tc.testName); got != tc.want {
				t.Errorf("matchRun(%q, %q, %q) = %v, want %v", tc.pattern, tc.fileStem, tc.testName, got, tc.want)
			}
		})
	}
}

// writeTwoFileWorkspace writes a workspace with two test files, each stem
// distinct, but each containing one test named "common" plus one test with a
// name unique to that file — material for both the name-only-across-files
// case and the stem/* glob case.
// TestRunIsolatesExecutionErrors verifies that a single test which cannot
// execute (here: a check against a relation the model doesn't define) is
// reported as its own errored/failed result without discarding the results of
// the other tests in the suite.
func TestRunIsolatesExecutionErrors(t *testing.T) {
	dir := t.TempDir()
	model := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\nmodel: ./model.fga\ntests:\n  - \"*.test.yaml\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	good := "tests:\n" +
		"  - name: ok\n" +
		"    check:\n      - user: user:x\n        object: document:1\n        assertions: {viewer: false}\n"
	if err := os.WriteFile(filepath.Join(dir, "a_good.test.yaml"), []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := "tests:\n" +
		"  - name: typo\n" +
		"    check:\n      - user: user:x\n        object: document:1\n        assertions: {viewerr: false}\n"
	if err := os.WriteFile(filepath.Join(dir, "b_bad.test.yaml"), []byte(bad), 0o600); err != nil {
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
		t.Fatalf("Run must not abort the whole suite on one test's error: %v", err)
	}
	if len(res.Tests) != 2 {
		t.Fatalf("expected both tests reported, got %d", len(res.Tests))
	}
	if res.Summary.Total != 2 || res.Summary.Passed != 1 || res.Summary.Failed != 1 {
		t.Errorf("summary = %+v, want total 2 / passed 1 / failed 1", res.Summary)
	}
	byName := map[string]TestResult{}
	for _, tr := range res.Tests {
		byName[tr.Name] = tr
	}
	if good := byName["a_good/ok"]; !good.Passed || good.Error != "" {
		t.Errorf("good test = %+v, want passed and no error", good)
	}
	errored := byName["b_bad/typo"]
	if errored.Passed || errored.Error == "" {
		t.Errorf("errored test = %+v, want failed with an Error", errored)
	}
	if strings.HasPrefix(errored.Error, "test b_bad/typo:") {
		t.Errorf("stored Error should not repeat the test-name prefix: %q", errored.Error)
	}
}

func writeTwoFileWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	model := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}

	fileA := "tests:\n" +
		"  - name: common\n" +
		"    tuples:\n" +
		"      - user: user:anne\n        relation: viewer\n        object: document:1\n" +
		"    check:\n      - user: user:anne\n        object: document:1\n        assertions: {viewer: true}\n" +
		"  - name: only-in-a\n" +
		"    check:\n      - user: user:bob\n        object: document:1\n        assertions: {viewer: false}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "a.test.yaml"), []byte(fileA), 0o600); err != nil {
		t.Fatal(err)
	}

	fileB := "tests:\n" +
		"  - name: common\n" +
		"    tuples:\n" +
		"      - user: user:carol\n        relation: viewer\n        object: document:2\n" +
		"    check:\n      - user: user:carol\n        object: document:2\n        assertions: {viewer: true}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "b.test.yaml"), []byte(fileB), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

// TestRunNameOnlyPatternMatchesAcrossFiles covers the name-only (no "/")
// half of matchRun through Run itself: a pattern of just "common" — a test
// name that exists in both a.test.yaml and b.test.yaml — must select both,
// from both files, not just the first one found.
func TestRunNameOnlyPatternMatchesAcrossFiles(t *testing.T) {
	dir := writeTwoFileWorkspace(t)
	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Run: "common"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.MatchedTests != 2 {
		t.Fatalf("want 2 matched tests (common in both files), got %d: %+v", res.Summary.MatchedTests, res.Tests)
	}
	if res.Summary.MatchedFiles != 2 {
		t.Fatalf("want 2 matched files, got %d", res.Summary.MatchedFiles)
	}
	if res.Summary.Failed != 0 {
		t.Fatalf("want 0 failed, got %d: %+v", res.Summary.Failed, res.Tests)
	}
}

// TestRunGlobStarPatternMatchesMultipleTestsInOneFile covers the "<stem>/*"
// glob half of matchRun through Run: it must select every test in the named
// stem and none from the other file.
func TestRunGlobStarPatternMatchesMultipleTestsInOneFile(t *testing.T) {
	dir := writeTwoFileWorkspace(t)
	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Run: "a/*"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.MatchedTests != 2 {
		t.Fatalf("want 2 matched tests (both tests in a.test.yaml), got %d: %+v", res.Summary.MatchedTests, res.Tests)
	}
	if res.Summary.MatchedFiles != 1 {
		t.Fatalf("want 1 matched file, got %d", res.Summary.MatchedFiles)
	}
	for _, tr := range res.Tests {
		if !strings.HasPrefix(tr.Name, "a/") {
			t.Errorf("want only tests from stem \"a\", got %q", tr.Name)
		}
	}
}

func TestRunFilterSelectsSingleTest(t *testing.T) {
	ws, err := LoadWorkspace("testdata/docs")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Run: "documents/owner-is-viewer"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.MatchedTests != 1 {
		t.Fatalf("want 1, got %d", res.Summary.MatchedTests)
	}
}

func TestRunZeroMatchesIsError(t *testing.T) {
	ws, err := LoadWorkspace("testdata/docs")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	_, err = Run(context.Background(), ws, Options{Engine: eng, Run: "nope/nope"})
	if err == nil {
		t.Fatal("zero matched tests must be an error")
	}
	if got, want := err.Error(), `no tests matched --run "nope/nope"`; got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestRunNoMatchWithoutRunFilterNamesTheEmptyWorkspaceNotABlankPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte("model\n  schema 1.1\n\ntype user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte("version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Run(context.Background(), ws, Options{})
	if err == nil {
		t.Fatal("empty workspace must be an error")
	}
	if got, want := err.Error(), `no test files found in workspace (manifest patterns: tests/**/*.test.yaml)`; got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

// TestRunNoMatchOnSingleRestrictedFileNamesTheFile covers loading a single
// *.test.yaml file directly (the -f/--file <file> case) whose manifest is
// found by walking up: ws.Manifest is non-nil AND ws.TestFiles is
// restricted to just that one (empty) file. The error must name the file,
// not fall back to the manifest's generic tests: glob patterns.
func TestRunNoMatchOnSingleRestrictedFileNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte("model\n  schema 1.1\n\ntype user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte("version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	emptyFile := filepath.Join(dir, "tests", "empty.test.yaml")
	if err := os.WriteFile(emptyFile, []byte("tests: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorkspace(emptyFile)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Manifest == nil {
		t.Fatal("want the manifest found by walking up to be attached to the workspace")
	}
	if len(ws.TestFiles) != 1 {
		t.Fatalf("want the workspace restricted to the single requested file, got %d files", len(ws.TestFiles))
	}

	_, err = Run(context.Background(), ws, Options{})
	if err == nil {
		t.Fatal("empty file must be an error")
	}
	if got, want := err.Error(), emptyFile+" has no tests"; got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestRunParallelDeterministic(t *testing.T) {
	ws, err := LoadWorkspace("testdata/docs")
	if err != nil {
		t.Fatal(err)
	}

	run := func() []byte {
		eng, err := NewEmbeddedEngine(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer eng.Close()

		res, err := Run(context.Background(), ws, Options{Engine: eng, Parallel: 4})
		if err != nil {
			t.Fatal(err)
		}

		// Durations are real wall-clock and legitimately vary run-to-run; this
		// test is about result content/ordering determinism, not timing, so
		// zero them out before comparing.
		res.Summary.DurationMs = 0
		for i := range res.Tests {
			res.Tests[i].DurationMs = 0
		}

		b, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	first := run()
	second := run()

	if string(first) != string(second) {
		t.Fatalf("results not deterministic:\nfirst:  %s\nsecond: %s", first, second)
	}
}

// writeMultiModelWorkspace writes a manifest whose default model (model.fga)
// is never actually used: two test files each override `model:` to their own
// distinct model (model-a.fga, model-b.fga), so the workspace's tests run
// against two different models.
func writeMultiModelWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	manifestModel := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define manifestonly: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(manifestModel), 0o600); err != nil {
		t.Fatal(err)
	}

	modelA := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model-a.fga"), []byte(modelA), 0o600); err != nil {
		t.Fatal(err)
	}

	modelB := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define editor: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model-b.fga"), []byte(modelB), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}

	testA := "model: ../model-a.fga\ntests:\n  - name: a-test\n    tuples:\n      - user: user:anne\n        relation: viewer\n        object: document:1\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {viewer: true}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "a.test.yaml"), []byte(testA), 0o600); err != nil {
		t.Fatal(err)
	}

	testB := "model: ../model-b.fga\ntests:\n  - name: b-test\n    tuples:\n      - user: user:bob\n        relation: editor\n        object: document:1\n    check:\n      - user: user:bob\n        object: document:1\n        assertions: {editor: true}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "b.test.yaml"), []byte(testB), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

// TestRunCoverageMultiModelIsNonFatal covers the regression fix: a workspace
// where different test files override `model:` to different models has no
// single model for --coverage branch to honestly enumerate against, but that
// is NOT a run failure — the tests all ran. Run must return the results (with
// the passed counts intact), leave Coverage nil, and record the reason in
// CoverageError, so a green multi-model run is not lost just because coverage
// couldn't be built. (Previously Run returned a fatal CodeUsage error here,
// failing the whole run even though every test passed; the CLI now re-derives
// that exit code from CoverageError itself — see the model command tests.)
func TestRunCoverageMultiModelIsNonFatal(t *testing.T) {
	dir := writeMultiModelWorkspace(t)
	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true})
	if err != nil {
		t.Fatalf("Run returned a fatal error for multi-model coverage, want non-fatal: %v", err)
	}
	if res == nil {
		t.Fatal("want non-nil results")
	}
	if res.Summary.Passed != 2 || res.Summary.Total != 2 {
		t.Fatalf("summary Passed/Total = %d/%d, want 2/2 (tests should have run)", res.Summary.Passed, res.Summary.Total)
	}
	if res.Coverage != nil {
		t.Fatalf("want nil Coverage for a multi-model workspace, got %+v", res.Coverage)
	}
	if res.CoverageError == "" {
		t.Fatal("want a non-empty CoverageError explaining why coverage was unavailable")
	}
}

// TestRunCoverageUsesTestFileOverrideModel covers the single-override half
// of finding #2: when exactly one model is resolved across the tests that
// ran (here, every test file overrides to the same model, distinct from the
// manifest's), coverage must enumerate branches from that resolved model,
// not blindly from the manifest's.
func TestRunCoverageUsesTestFileOverrideModel(t *testing.T) {
	dir := t.TempDir()

	manifestModel := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define manifestonly: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(manifestModel), 0o600); err != nil {
		t.Fatal(err)
	}

	overrideModel := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define overrideonly: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model-override.fga"), []byte(overrideModel), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}

	testFile := "model: ../model-override.fga\ntests:\n  - name: override-test\n    tuples:\n      - user: user:anne\n        relation: overrideonly\n        object: document:1\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {overrideonly: true}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "override.test.yaml"), []byte(testFile), 0o600); err != nil {
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

	res, err := Run(context.Background(), ws, Options{Engine: eng, Coverage: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Coverage == nil {
		t.Fatal("want non-nil Coverage")
	}

	if rc := findRelCov(res.Coverage, "document", "overrideonly"); rc == nil {
		t.Errorf("expected document#overrideonly (the override model's relation) in coverage, got %+v", res.Coverage.Types)
	}
	if rc := findRelCov(res.Coverage, "document", "manifestonly"); rc != nil {
		t.Errorf("expected document#manifestonly (the manifest model's relation) NOT in coverage since the test file overrides model:, got %+v", rc)
	}
}

// TestRunNearestMissOnlyOnFailedCheck covers finding #4: nearestMiss must
// only be computed for a check assertion that actually failed, not merely
// because got == false. A passing expected-deny (bob) has got == false but
// Passed == true — running nearestMiss there wastes probe Checks and would
// print a confusing "how to grant it" hint on an assertion that intended a
// denial. A failing check (anne) still gets a non-empty nearestMiss.
func TestRunNearestMissOnlyOnFailedCheck(t *testing.T) {
	dir := t.TempDir()

	model := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define owner: [user]\n    define viewer: [user] or owner\n"
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}

	testFile := "tests:\n" +
		"  - name: mixed-checks\n" +
		"    check:\n" +
		"      - user: user:anne\n" +
		"        object: document:1\n" +
		"        assertions: {viewer: true}\n" +
		"      - user: user:bob\n" +
		"        object: document:1\n" +
		"        assertions: {viewer: false}\n"
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

	res, err := Run(context.Background(), ws, Options{Engine: eng, Explain: "full"})
	if err != nil {
		t.Fatal(err)
	}

	assertions := res.Tests[0].Assertions
	if len(assertions) != 2 {
		t.Fatalf("want 2 assertions, got %d: %+v", len(assertions), assertions)
	}

	var failed, passed *AssertionResult
	for i := range assertions {
		if assertions[i].Passed {
			passed = &assertions[i]
		} else {
			failed = &assertions[i]
		}
	}
	if failed == nil {
		t.Fatal("want one failing assertion (anne, expected viewer: true)")
	}
	if passed == nil {
		t.Fatal("want one passing assertion (bob, expected viewer: false)")
	}

	if failed.Explain == nil || failed.Explain.NearestMiss == "" {
		t.Errorf("want a non-empty nearestMiss on the failing check, got %+v", failed.Explain)
	}
	if passed.Explain == nil {
		t.Fatal("want Explain populated on the passing check under Explain==\"full\"")
	}
	if passed.Explain.NearestMiss != "" {
		t.Errorf("want an empty nearestMiss on a passing expected-deny check, got %q", passed.Explain.NearestMiss)
	}
}

// TestRunListObjectsExpectedDuplicatePasses guards the set-vs-list comparison
// fix: a list_objects assertion whose `expected` repeats an element must still
// pass when the engine returns that element once. Before the fix, the positional
// comparison saw a length mismatch and failed the assertion while the set-based
// diff reported nothing missing or extra — a self-contradicting failure.
func TestRunListObjectsExpectedDuplicatePasses(t *testing.T) {
	dir := t.TempDir()

	model := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}

	testFile := "tests:\n" +
		"  - name: anne-sees-document\n" +
		"    tuples:\n" +
		"      - user:anne viewer document:1\n" +
		"    list_objects:\n" +
		"      - user: user:anne\n" +
		"        type: document\n" +
		"        assertions:\n" +
		"          viewer: [document:1, document:1]\n"
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
		t.Fatalf("want 0 failed (duplicate in expected must not fail), got %d: %+v", res.Summary.Failed, res.Tests)
	}
}

// writeMiniWorkspace writes a one-model workspace whose tests all target
// document#viewer, so callers only specify per-test check assertions. It returns
// the workspace root.
func writeMiniWorkspace(t *testing.T, testFileBody string) string {
	t.Helper()
	dir := t.TempDir()
	model := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\nmodel: ./model.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "t.test.yaml"), []byte(testFileBody), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunFailFastStopsAfterFirstFailure verifies --fail-fast stops dispatching
// once a test fails (with Parallel: 1 the first, failing test is the only one
// that runs), and that the un-run tests aren't miscounted as failures.
func TestRunFailFastStopsAfterFirstFailure(t *testing.T) {
	// First test fails (anne is not a viewer of document:1 but the assertion
	// expects true); the second would pass. Fail-fast must stop after the first.
	body := "tests:\n" +
		"  - name: a-fails\n" +
		"    check:\n" +
		"      - user: user:anne\n        object: document:1\n        assertions:\n          viewer: true\n" +
		"  - name: b-would-pass\n" +
		"    check:\n" +
		"      - user: user:anne\n        object: document:1\n        assertions:\n          viewer: false\n"
	ws, err := LoadWorkspace(writeMiniWorkspace(t, body))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := Run(context.Background(), ws, Options{Engine: eng, FailFast: true, Parallel: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Total != 1 || res.Summary.Failed != 1 {
		t.Fatalf("fail-fast: want 1 total / 1 failed (second test not run), got total=%d failed=%d passed=%d",
			res.Summary.Total, res.Summary.Failed, res.Summary.Passed)
	}
}

// TestRunSurfacesTestDescription verifies a test's `description:` is carried
// onto its TestResult (it was previously parsed but never propagated/shown).
func TestRunSurfacesTestDescription(t *testing.T) {
	body := "tests:\n" +
		"  - name: anne-denied\n" +
		"    description: anne has no grant here\n" +
		"    check:\n" +
		"      - user: user:anne\n        object: document:1\n        assertions:\n          viewer: false\n"
	ws, err := LoadWorkspace(writeMiniWorkspace(t, body))
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
	if len(res.Tests) != 1 || res.Tests[0].Description != "anne has no grant here" {
		t.Fatalf("want the test description surfaced on the result, got %+v", res.Tests)
	}
}
