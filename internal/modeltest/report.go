package modeltest

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sergiught/openfga-cli/internal/output"
)

// WriteReport writes res in the given format ("junit", "json", or "github") to w.
func WriteReport(format string, w io.Writer, res *Results) error {
	switch format {
	case "junit":
		return writeJUnitReport(w, res)
	case "json":
		return output.JSON(w, res)
	case "github":
		return writeGitHubReport(w, res)
	default:
		return fmt.Errorf("unknown report format %q (want %q, %q, or %q)", format, "junit", "json", "github")
	}
}

// writeGitHubReport emits a GitHub Actions workflow annotation per failing test
// (https://docs.github.com/actions/using-workflows/workflow-commands-for-github-actions),
// so failures surface as errors in the Actions log and job summary. Annotations
func writeGitHubReport(w io.Writer, res *Results) error {
	for _, t := range res.Tests {
		if t.Passed && t.Error == "" {
			continue
		}
		msg := t.Error
		if msg == "" {
			var parts []string
			for _, a := range t.Assertions {
				if a.Passed {
					continue
				}
				parts = append(parts, fmt.Sprintf("%s (expected %v, got %v)", a.Subject, a.Expected, a.Got))
			}
			msg = "failed: " + strings.Join(parts, "; ")
		}
		var props []string
		if t.File != "" {
			props = append(props, "file="+ghEscapeProp(githubAnnotationFile(t)))
		}
		if t.Line > 0 {
			props = append(props, fmt.Sprintf("line=%d", t.Line))
		}
		if t.Column > 0 {
			props = append(props, fmt.Sprintf("col=%d", t.Column))
		}
		props = append(props, "title="+ghEscapeProp("model test: "+t.Name))
		if _, err := fmt.Fprintf(w, "::error %s::%s\n", strings.Join(props, ","), ghEscapeData(msg)); err != nil {
			return err
		}
	}
	return nil
}

func githubAnnotationFile(t TestResult) string {
	if t.sourcePath == "" {
		return t.File
	}
	root := os.Getenv("GITHUB_WORKSPACE")
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return t.File
		}
	}
	rel, err := filepath.Rel(root, t.sourcePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return t.File
	}
	return filepath.ToSlash(rel)
}

// ghEscapeData / ghEscapeProp apply GitHub's workflow-command escaping so a
// message or title containing %, newlines, ':' or ',' can't break the command.
func ghEscapeData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

func ghEscapeProp(s string) string {
	s = ghEscapeData(s)
	s = strings.ReplaceAll(s, ":", "%3A")
	s = strings.ReplaceAll(s, ",", "%2C")
	return s
}

// junitTestSuites is the root element of a minimal JUnit XML report.
type junitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Errors   int              `xml:"errors,attr"`
	Time     string           `xml:"time,attr"` // seconds, "%.3f"; sum of child testsuite times
	Suites   []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Errors   int             `xml:"errors,attr"`
	Time     string          `xml:"time,attr"` // seconds, "%.3f"; sum of this suite's tests' DurationMs
	Cases    []junitTestCase `xml:"testcase"`
}

// junitTestCase deliberately carries no time attribute: a testcase here is
// one check/list_objects/list_users assertion, but duration is measured per
// TEST (Setup + all of a test's assertions together), which has no faithful
// per-assertion split. Distributing the test's duration across N testcases
// (or dumping it all on the first) would misrepresent per-assertion cost to
// CI tooling, so timing is reported at the two granularities it's actually
// measured at: <testsuite> (per test file) and <testsuites> (the whole run).
type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Error     *junitError   `xml:"error,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
}

// junitError marks a testcase the runner could not execute (as opposed to a
// failed assertion), matching JUnit's <error> vs <failure> distinction.
type junitError struct {
	Message string `xml:"message,attr"`
}

func writeJUnitReport(w io.Writer, res *Results) error {
	junitSuites, totalMs := buildJUnitSuites(res)
	suites := junitTestSuites{Suites: junitSuites, Time: formatSeconds(totalMs)}
	for _, s := range suites.Suites {
		suites.Tests += s.Tests
		suites.Failures += s.Failures
		suites.Errors += s.Errors
	}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}

	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	return enc.Encode(suites)
}

// buildJUnitSuites groups res.Tests' assertions into one testsuite per test
// file, keyed by its workspace-relative source identity, preserving res.Tests
// order. It also returns the sum of every suite's duration in milliseconds,
// for the caller to use as the <testsuites> root time.
//
// The root time is the SUM of the child <testsuite> times (matching how
// tests/failures are already rolled up as sums), not Results.Summary.
// DurationMs: under the parallel runner, summed suite time can exceed the
// true wall-clock, and JUnit consumers (Jenkins, GitLab, Azure) assume the
// parent's time is >= the sum of its children's. The human/JSON surfaces
// still report Summary.DurationMs, the true wall-clock of the run.
func buildJUnitSuites(res *Results) ([]junitTestSuite, int64) {
	var suites []junitTestSuite
	index := map[string]int{}
	var suiteDurationMs []int64

	for _, t := range res.Tests {
		stem := junitSuiteName(t)

		i, ok := index[stem]
		if !ok {
			i = len(suites)
			index[stem] = i
			suites = append(suites, junitTestSuite{Name: stem})
			suiteDurationMs = append(suiteDurationMs, 0)
		}
		suiteDurationMs[i] += t.DurationMs

		// A test that could not execute has no assertions — emit one <error>
		// testcase so it is counted and visible rather than silently absent.
		if t.Error != "" {
			suites[i].Cases = append(suites[i].Cases, junitTestCase{
				Name:      t.Name,
				ClassName: t.Name,
				Error:     &junitError{Message: t.Error},
			})
			suites[i].Tests++
			suites[i].Errors++
			continue
		}

		for _, a := range t.Assertions {
			tc := junitTestCase{Name: a.Subject, ClassName: t.Name}
			if !a.Passed {
				tc.Failure = &junitFailure{Message: failureMessage(a)}
				suites[i].Failures++
			}
			suites[i].Tests++
			suites[i].Cases = append(suites[i].Cases, tc)
		}
	}

	var totalMs int64
	for i := range suites {
		suites[i].Time = formatSeconds(suiteDurationMs[i])
		totalMs += suiteDurationMs[i]
	}

	return suites, totalMs
}

func junitSuiteName(t TestResult) string {
	if t.File != "" {
		name := strings.TrimPrefix(filepath.ToSlash(t.File), "tests/")
		for _, suffix := range []string{".test.yaml", ".test.yml"} {
			if strings.HasSuffix(name, suffix) {
				return strings.TrimSuffix(name, suffix)
			}
		}
	}
	if i := strings.LastIndex(t.Name, "/"); i >= 0 {
		return t.Name[:i]
	}
	return t.Name
}

// formatSeconds renders a millisecond duration as JUnit's expected seconds
// attribute, "%.3f".
func formatSeconds(ms int64) string {
	return fmt.Sprintf("%.3f", float64(ms)/1000)
}

// failureMessage describes why a failing assertion failed. It prefers the
// assertion's explanation: a check's nearest miss, or a list_objects/
// list_users set diff, and otherwise falls back to an expected/got summary.
func failureMessage(ar AssertionResult) string {
	switch {
	case ar.Explain != nil && ar.Explain.NearestMiss != "":
		return fmt.Sprintf("expected %v, got %v; nearest miss: %s", ar.Expected, ar.Got, ar.Explain.NearestMiss)
	case ar.Explain != nil && ar.Explain.SetDiff != nil && (len(ar.Explain.SetDiff.Unexpected) > 0 || len(ar.Explain.SetDiff.Missing) > 0):
		sd := ar.Explain.SetDiff

		var parts []string
		if len(sd.Unexpected) > 0 {
			parts = append(parts, fmt.Sprintf("unexpected: %v", sd.Unexpected))
		}
		if len(sd.Missing) > 0 {
			parts = append(parts, fmt.Sprintf("missing: %v", sd.Missing))
		}

		return fmt.Sprintf("expected %v, got %v; %s", ar.Expected, ar.Got, strings.Join(parts, ", "))
	default:
		return fmt.Sprintf("expected %v, got %v", ar.Expected, ar.Got)
	}
}
