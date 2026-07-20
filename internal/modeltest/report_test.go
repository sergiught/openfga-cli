package modeltest

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"strconv"
	"strings"
	"testing"
)

func countAssertions(res *Results) int {
	n := 0
	for _, t := range res.Tests {
		n += len(t.Assertions)
	}
	return n
}

func TestWriteJUnitHasTestcasePerAssertion(t *testing.T) {
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

	var buf bytes.Buffer
	if err := WriteReport("junit", &buf, res); err != nil {
		t.Fatal(err)
	}

	var parsed junitTestSuites
	if err := xml.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid xml: %v", err)
	}

	total := 0
	for _, s := range parsed.Suites {
		total += len(s.Cases)
		if s.Tests != len(s.Cases) {
			t.Errorf("suite %q: want tests=%d (case count), got %d", s.Name, len(s.Cases), s.Tests)
		}
	}
	want := countAssertions(res)
	if total != want {
		t.Fatalf("want %d testcases, got %d", want, total)
	}
	if len(parsed.Suites) == 0 {
		t.Fatal("want at least one testsuite")
	}
	if parsed.Tests != want {
		t.Fatalf("want testsuites tests=%d, got %d", want, parsed.Tests)
	}
}

func TestWriteJUnitFailureElement(t *testing.T) {
	res := &Results{
		Summary: Summary{Total: 1, Failed: 1},
		Tests: []TestResult{
			{
				Name:   "documents/owner-is-viewer",
				Passed: false,
				Assertions: []AssertionResult{
					{Kind: "check", Subject: "check user:anne viewer document:1", Expected: true, Got: false, Passed: false},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteReport("junit", &buf, res); err != nil {
		t.Fatal(err)
	}

	var parsed junitTestSuites
	if err := xml.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid xml: %v", err)
	}

	if len(parsed.Suites) != 1 {
		t.Fatalf("want 1 suite, got %d", len(parsed.Suites))
	}
	if len(parsed.Suites[0].Cases) != 1 {
		t.Fatalf("want 1 case, got %d", len(parsed.Suites[0].Cases))
	}
	failure := parsed.Suites[0].Cases[0].Failure
	if failure == nil {
		t.Fatal("want a <failure> element on the failing assertion")
	}
	if !strings.Contains(failure.Message, "expected") {
		t.Fatalf("want failure message to contain %q, got %q", "expected", failure.Message)
	}
	if parsed.Tests != 1 || parsed.Failures != 1 {
		t.Fatalf("want testsuites tests=1 failures=1, got tests=%d failures=%d", parsed.Tests, parsed.Failures)
	}
	if parsed.Suites[0].Tests != 1 || parsed.Suites[0].Failures != 1 {
		t.Fatalf("want testsuite tests=1 failures=1, got tests=%d failures=%d", parsed.Suites[0].Tests, parsed.Suites[0].Failures)
	}
}

func TestWriteJUnitCountsMixedPassFail(t *testing.T) {
	res := &Results{
		Summary: Summary{Total: 2, Passed: 1, Failed: 1},
		Tests: []TestResult{
			{
				Name:   "documents/owner-is-viewer",
				Passed: false,
				Assertions: []AssertionResult{
					{Kind: "check", Subject: "check user:anne viewer document:1", Expected: true, Got: true, Passed: true},
					{Kind: "check", Subject: "check user:bob viewer document:1", Expected: true, Got: false, Passed: false},
				},
			},
			{
				Name:   "folders/owner-is-viewer",
				Passed: true,
				Assertions: []AssertionResult{
					{Kind: "check", Subject: "check user:anne viewer folder:1", Expected: true, Got: true, Passed: true},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteReport("junit", &buf, res); err != nil {
		t.Fatal(err)
	}

	var parsed junitTestSuites
	if err := xml.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid xml: %v", err)
	}

	if parsed.Tests != 3 || parsed.Failures != 1 {
		t.Fatalf("want testsuites tests=3 failures=1, got tests=%d failures=%d", parsed.Tests, parsed.Failures)
	}
	if len(parsed.Suites) != 2 {
		t.Fatalf("want 2 suites, got %d", len(parsed.Suites))
	}

	docs, folders := parsed.Suites[0], parsed.Suites[1]
	if docs.Tests != 2 || docs.Failures != 1 {
		t.Fatalf("want documents suite tests=2 failures=1, got tests=%d failures=%d", docs.Tests, docs.Failures)
	}
	if folders.Tests != 1 || folders.Failures != 0 {
		t.Fatalf("want folders suite tests=1 failures=0, got tests=%d failures=%d", folders.Tests, folders.Failures)
	}
}

func TestJUnitFailureMessageUsesExplain(t *testing.T) {
	res := &Results{
		Summary: Summary{Total: 1, Failed: 1},
		Tests: []TestResult{
			{
				Name:   "documents/owner-is-viewer",
				Passed: false,
				Assertions: []AssertionResult{
					{
						Kind:     "check",
						Subject:  "check user:anne viewer document:1",
						Expected: true,
						Got:      false,
						Passed:   false,
						Explain: &Explain{
							NearestMiss: "a tuple (user:anne, owner, document:1) would grant it",
						},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteReport("junit", &buf, res); err != nil {
		t.Fatal(err)
	}

	var parsed junitTestSuites
	if err := xml.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid xml: %v", err)
	}

	failure := parsed.Suites[0].Cases[0].Failure
	if failure == nil {
		t.Fatal("want a <failure> element on the failing assertion")
	}
	want := "a tuple (user:anne, owner, document:1) would grant it"
	if !strings.Contains(failure.Message, want) {
		t.Fatalf("want failure message to contain %q, got %q", want, failure.Message)
	}
}

// TestWriteJUnitHasTimeAttributes guards review finding S6 for the JUnit
// surface: CI dashboards key off JUnit's time= attribute, so <testsuites>
// (the whole run) and <testsuite> (per test file) must both carry a valid,
// non-negative seconds value. <testcase> deliberately has none — see the
// junitTestCase doc comment for why a per-assertion split would be
// misleading — so this test does not look for it there.
//
// It also guards the conventional JUnit aggregation invariant that the
// parent's time is >= every child's, and specifically == their sum: under
// the parallel runner, wall-clock time (Results.Summary.DurationMs) can be
// LESS than the sum of per-suite durations, which would make a <testsuite>
// report more time than its parent <testsuites> — CI/JUnit consumers assume
// parent >= sum(children), so the root must use the summed basis instead.
func TestWriteJUnitHasTimeAttributes(t *testing.T) {
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

	var buf bytes.Buffer
	if err := WriteReport("junit", &buf, res); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), `time="`) {
		t.Fatalf("want JUnit XML to contain time= attributes, got: %s", buf.String())
	}

	var parsed junitTestSuites
	if err := xml.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid xml: %v", err)
	}

	if _, err := strconv.ParseFloat(parsed.Time, 64); err != nil {
		t.Fatalf("testsuites time=%q is not a valid float: %v", parsed.Time, err)
	}
	if len(parsed.Suites) == 0 {
		t.Fatal("want at least one testsuite")
	}

	rootTime, err := strconv.ParseFloat(parsed.Time, 64)
	if err != nil {
		t.Fatalf("testsuites time=%q is not a valid float: %v", parsed.Time, err)
	}

	const epsilon = 0.0005 // half the "%.3f" rounding step
	var sum float64
	for _, s := range parsed.Suites {
		v, err := strconv.ParseFloat(s.Time, 64)
		if err != nil {
			t.Fatalf("testsuite %q time=%q is not a valid float: %v", s.Name, s.Time, err)
		}
		if v < 0 {
			t.Fatalf("testsuite %q time=%q is negative", s.Name, s.Time)
		}
		if v > rootTime+epsilon {
			t.Fatalf("testsuite %q time=%v exceeds testsuites time=%v (parent must be >= every child)", s.Name, v, rootTime)
		}
		sum += v
	}
	if diff := rootTime - sum; diff > epsilon || diff < -epsilon {
		t.Fatalf("want testsuites time=%v to equal sum of testsuite times=%v", rootTime, sum)
	}
}

func TestWriteJSONReportStableShape(t *testing.T) {
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

	var buf bytes.Buffer
	if err := WriteReport("json", &buf, res); err != nil {
		t.Fatal(err)
	}

	var got Results
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got.Summary != res.Summary {
		t.Fatalf("want summary %+v, got %+v", res.Summary, got.Summary)
	}
}

func TestWriteReportUnknownFormatErrors(t *testing.T) {
	res := &Results{}
	var buf bytes.Buffer
	if err := WriteReport("bogus", &buf, res); err == nil {
		t.Fatal("want an error for an unknown report format")
	}
}

// TestWriteGitHubReportEmitsErrorAnnotation checks the github report format
// emits one ::error annotation per failing test (and none for passing tests),
// with GitHub's property/data escaping applied.
func TestWriteGitHubReportEmitsErrorAnnotation(t *testing.T) {
	res := &Results{
		Summary: Summary{Total: 2, Passed: 1, Failed: 1},
		Tests: []TestResult{
			{Name: "documents/passes", Passed: true},
			{
				Name:   "documents/owner-is-viewer",
				Passed: false,
				Assertions: []AssertionResult{
					{Kind: "check", Subject: "check user:anne viewer document:1", Expected: true, Got: false, Passed: false},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteReport("github", &buf, res); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if n := strings.Count(out, "::error "); n != 1 {
		t.Fatalf("want exactly 1 ::error annotation (only the failing test), got %d in:\n%s", n, out)
	}
	if !strings.Contains(out, "owner-is-viewer") {
		t.Fatalf("annotation should name the failing test, got:\n%s", out)
	}
	if strings.Contains(out, "documents/passes") {
		t.Fatalf("passing test must not be annotated, got:\n%s", out)
	}
	// The title carries a ':' which must be escaped to %3A so it can't break
	// the workflow command's property parsing.
	if !strings.Contains(out, "model test%3A") {
		t.Fatalf("title ':' should be escaped to %%3A, got:\n%s", out)
	}
}
