package mapping

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/log/v2"
	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/output"
)

// failingMapping expects "admin" where the rule writes "member".
var failingMapping = strings.Replace(validMapping,
	"relation: member\n        object: \"organization:o1\"",
	"relation: admin\n        object: \"organization:o1\"", 1)

const uncoveredMapping = `version: "1"
rules:
  - name: r
    when: input.type == "x"
    tuple_filters:
      - {relation: member, object: "team:2"}
    tuples:
      - {user: "user:1", relation: member, object: "team:1"}
tests:
  - name: uncovered
    input: {type: x}
    assert_writes_covered_by_filter: true
    expect_tuples:
      - {user: "user:1", relation: member, object: "team:1", action: write}
`

func newCLI() *cli.CLI { return cli.New(log.New(io.Discard), config.New(), "test") }

func TestTestCommand(t *testing.T) {
	configtest.Isolate(t)

	t.Run("a passing file exits 0", func(t *testing.T) {
		p := writeFile(t, t.TempDir(), "mapping.yaml", validMapping)
		out, err := run(t, newCLI(), "test", p)
		if err != nil || !strings.Contains(out, "1 passed, 0 failed") || !strings.Contains(out, "✓") || !strings.Contains(out, "added (") {
			t.Fatalf("err=%v out=%s", err, out)
		}
	})

	t.Run("a failing expectation shows why", func(t *testing.T) {
		p := writeFile(t, t.TempDir(), "mapping.yaml", failingMapping)
		out, err := run(t, newCLI(), "test", p)
		if clierr.Code(err) != clierr.CodeTestFailed || !clierr.IsSilent(err) {
			t.Fatalf("err=%v", err)
		}
		for _, want := range []string{"mapping.yaml:19", "missing: user:u1 admin organization:o1", "unexpected: user:u1 member organization:o1", "fired: member-added", "0 passed, 1 failed"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in:\n%s", want, out)
			}
		}
	})

	t.Run("--run matching nothing is an error", func(t *testing.T) {
		p := writeFile(t, t.TempDir(), "mapping.yaml", validMapping)
		_, err := run(t, newCLI(), "test", p, "--run", "nomatch")
		if clierr.Code(err) != clierr.CodeError || err == nil || !strings.Contains(err.Error(), `no test name contains "nomatch"`) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("a file with no tests is an error", func(t *testing.T) {
		p := writeFile(t, t.TempDir(), "mapping.yaml", "version: \"1\"\nrules:\n  - name: r\n    when: input.type == \"x\"\n    tuples:\n      - {user: \"user:1\", relation: member, object: \"t:1\"}\n")
		_, err := run(t, newCLI(), "test", p)
		if clierr.Code(err) != clierr.CodeError || err == nil || !strings.Contains(err.Error(), "no tests in "+p) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("a file that does not compile prints its findings", func(t *testing.T) {
		p := writeFile(t, t.TempDir(), "mapping.yaml", "version: \"1\"\nrules: [\n")
		out, err := run(t, newCLI(), "test", p)
		if clierr.Code(err) != clierr.CodeError || !strings.Contains(out, "mapping.yaml:") {
			t.Fatalf("err=%v out=%s", err, out)
		}
	})

	t.Run("reports", func(t *testing.T) {
		dir := t.TempDir()
		pass := writeFile(t, dir, "mapping.yaml", validMapping)
		out, err := run(t, newCLI(), "test", pass, "--report", "junit")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `<testcase name="tuples" classname="added"`) {
			t.Fatalf("out=%s", out)
		}
		dec := xml.NewDecoder(strings.NewReader(out[strings.Index(out, "<?xml"):]))
		for {
			if _, err := dec.Token(); err != nil {
				if err != io.EOF {
					t.Fatalf("not xml: %v\n%s", err, out)
				}
				break
			}
		}

		fail := writeFile(t, t.TempDir(), "mapping.yaml", failingMapping)
		out, err = run(t, newCLI(), "test", fail, "--report", "github")
		if clierr.Code(err) != clierr.CodeTestFailed {
			t.Fatalf("err=%v", err)
		}
		if !strings.Contains(out, "::error file=") || !strings.Contains(out, "mapping.yaml,line=19") ||
			!strings.Contains(out, "title=mapping test%3A added") || !strings.Contains(out, "user:u1 admin organization:o1") || strings.Contains(out, "map[") {
			t.Fatalf("out=%s", out)
		}

		rf := filepath.Join(dir, "sub", "r.json")
		if _, err := run(t, newCLI(), "test", pass, "--report", "json", "--report-file", rf); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(rf); err != nil || !strings.Contains(string(b), `"passed": 1`) && !strings.Contains(string(b), `"passed":1`) {
			t.Fatalf("report file: %v %s", err, b)
		}

		_, err = run(t, newCLI(), "test", pass, "--report-file", rf)
		if clierr.Code(err) != clierr.CodeUsage {
			t.Fatalf("err=%v", err)
		}
		_, err = run(t, newCLI(), "test", pass, "--report", "xml")
		if clierr.Code(err) != clierr.CodeUsage {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("control sequences in user text are stripped", func(t *testing.T) {
		old := output.Plain
		defer func() { output.Plain = old }()
		output.Plain = false
		src := strings.Replace(failingMapping, "name: added", `name: "ad\u001b[31mded"`, 1)
		src = strings.Replace(src, "user_id: u1", `user_id: "u\u001b[31m1"`, 1)
		p := writeFile(t, t.TempDir(), "mapping.yaml", src)
		c := newCLI()
		c.NoColor = true
		out, _ := run(t, c, "test", p)
		if strings.Contains(out, "\x1b[31m") || !strings.Contains(out, "✗") {
			t.Fatalf("out=%q", out)
		}
	})

	t.Run("an uncovered write is reported", func(t *testing.T) {
		p := writeFile(t, t.TempDir(), "mapping.yaml", uncoveredMapping)
		out, err := run(t, newCLI(), "test", p)
		if clierr.Code(err) != clierr.CodeTestFailed || !strings.Contains(out, "not covered by a filter: user:1 member team:1") {
			t.Fatalf("err=%v out=%s", err, out)
		}
	})

	t.Run("summary counts filtered tests", func(t *testing.T) {
		p := writeFile(t, t.TempDir(), "mapping.yaml", twoTests(failingMapping, validMapping))
		out, err := run(t, newCLI(), "test", p, "--run", "second")
		if err != nil || !strings.Contains(out, "1 passed, 0 failed, 1 filtered") {
			t.Fatalf("err=%v out=%s", err, out)
		}
	})

	t.Run("--fail-fast reports tests not run", func(t *testing.T) {
		p := writeFile(t, t.TempDir(), "mapping.yaml", twoTests(failingMapping, failingMapping))
		out, err := run(t, newCLI(), "test", p, "--fail-fast")
		if clierr.Code(err) != clierr.CodeTestFailed || !strings.Contains(out, "0 passed, 1 failed, 1 not run (--fail-fast)") {
			t.Fatalf("err=%v out=%s", err, out)
		}
	})

	t.Run("junit on stdout is pure XML", func(t *testing.T) {
		p := writeFile(t, t.TempDir(), "mapping.yaml", failingMapping)
		root := New(newCLI()).Command()
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs([]string{"test", p, "--report", "junit"})
		err := root.Execute()
		if clierr.Code(err) != clierr.CodeTestFailed {
			t.Fatalf("err=%v", err)
		}
		var doc struct {
			Failures int `xml:"failures,attr"`
		}
		if xerr := xml.Unmarshal(stdout.Bytes(), &doc); xerr != nil || doc.Failures != 1 {
			t.Fatalf("stdout not XML: %v\n%s", xerr, stdout.String())
		}
		if !strings.Contains(stderr.String(), "0 passed, 1 failed") {
			t.Fatalf("stderr=%s", stderr.String())
		}
	})

	t.Run("an errored test shows its error, not no rule matched", func(t *testing.T) {
		p := writeFile(t, t.TempDir(), "mapping.yaml", erroringMapping)
		out, err := run(t, newCLI(), "test", p)
		if clierr.Code(err) != clierr.CodeTestFailed || !strings.Contains(out, "error:") || strings.Contains(out, "no rule matched") {
			t.Fatalf("err=%v out=%s", err, out)
		}
	})
}

// twoTests returns a mapping with two tests, "first" taken from a's test and
// "second" from b's, both against a's rules.
func twoTests(a, b string) string {
	tests := func(src string) string { return src[strings.Index(src, "tests:\n")+len("tests:\n"):] }
	rename := func(src, name string) string { return strings.Replace(tests(src), "name: added", "name: "+name, 1) }
	return a[:strings.Index(a, "tests:\n")] + "tests:\n" + rename(a, "first") + rename(b, "second")
}

const erroringMapping = `version: "1"
rules:
  - name: r
    when: input.a.b.c == "x"
    tuples:
      - {user: "user:1", relation: member, object: "team:1"}
tests:
  - name: boom
    input: {type: x}
    expect_tuples:
      - {user: "user:1", relation: member, object: "team:1", action: write}
`

func TestTestReport(t *testing.T) {
	t.Setenv("GITHUB_WORKSPACE", "") // paths are then relative to the working directory
	r := &mapping.TestRun{Tests: []mapping.TestOutcome{
		{Name: "ok", Line: 3, Passed: true, Duration: 5 * time.Millisecond},
		{
			Name: "bad", Line: 9, Duration: 7 * time.Millisecond, Fired: []string{"r"},
			Expected:        []language.Tuple{{User: "u:1", Relation: "admin", Object: "o:1"}},
			Actual:          []language.Tuple{{User: "u:1", Relation: "member", Object: "o:1", Condition: "c"}},
			Missing:         []language.Tuple{{User: "u:1", Relation: "admin", Object: "o:1"}},
			Unexpected:      []language.Tuple{{User: "u:1", Relation: "member", Object: "o:1", Condition: "c"}},
			MissingFilters:  []language.TupleFilter{{Relation: "member", Object: "o:1", Action: "patch"}},
			ExpectedFilters: []language.TupleFilter{{Relation: "member", Object: "o:1", Action: "patch"}},
			Uncovered:       []language.Tuple{{User: "u:2", Relation: "member", Object: "o:2"}},
		},
	}}
	res := testReport(r, "m.yaml")
	if res.SchemaVersion != 1 || res.Label != "mapping test" || res.Summary.Total != 2 || res.Summary.Passed != 1 || res.Summary.Failed != 1 || res.Summary.DurationMs != 12 {
		t.Fatalf("summary = %+v", res.Summary)
	}
	if got := res.Tests[0]; len(got.Assertions) != 1 || !got.Assertions[0].Passed {
		t.Fatalf("passing test = %+v", got)
	}
	got := res.Tests[1]
	if got.File != "m.yaml" || got.Line != 9 || got.Passed || len(got.Assertions) != 3 {
		t.Fatalf("test = %+v", got)
	}
	a := got.Assertions[0]
	if a.Kind != "tuples" || a.Explain.SetDiff.Missing[0] != "u:1 admin o:1" || a.Explain.SetDiff.Unexpected[0] != "u:1 member o:1 (c)" {
		t.Fatalf("tuples assertion = %+v", a)
	}
	if f := got.Assertions[1]; f.Kind != "tuple_filters" || f.Explain.SetDiff.Missing[0] != "* member o:1 patch" {
		t.Fatalf("filters assertion = %+v", f)
	}
	if c := got.Assertions[2]; c.Kind != "coverage" || c.Got != "not covered by a filter: u:2 member o:2" {
		t.Fatalf("coverage assertion = %+v", c)
	}
}

func TestTestReportNamesTheFileFromTheRepositoryRoot(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GITHUB_WORKSPACE", repo)
	res := testReport(&mapping.TestRun{Tests: []mapping.TestOutcome{{Name: "bad", Line: 4}}}, filepath.Join(repo, "sync", "auth0.yaml"))
	if got := res.Tests[0].File; got != "sync/auth0.yaml" {
		t.Fatalf("file = %q", got)
	}
}

func TestTestReportsCompileErrorsBeforeMissingTests(t *testing.T) {
	configtest.Isolate(t)
	p := writeFile(t, t.TempDir(), "mapping.yaml", `version: "1"
rules:
  - name: r
    when: input.type ==
    tuples:
      - {user: "user:1", relation: member, object: "team:1"}
`)
	out, err := run(t, newCLI(), "test", p)
	if clierr.Code(err) != clierr.CodeError || !clierr.IsSilent(err) || !strings.Contains(out, p+":4:") || strings.Contains(out, "no tests") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

// quietly runs a mappings subcommand under --quiet, returning stdout and stderr.
func quietly(t *testing.T, c *cli.CLI, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	defer func(q bool) { output.Quiet = q }(output.Quiet)
	output.Quiet = true
	root := New(c).Command()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	root.SilenceUsage, root.SilenceErrors = true, true
	err = root.Execute()
	return out.String(), errb.String(), err
}

func TestQuietKeepsResultsAndDropsSummaries(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	valid := writeFile(t, dir, "valid.yaml", validMapping)
	invalid := writeFile(t, dir, "invalid.yaml", "version: \"1\"\nrules: [\n")

	t.Run("validate", func(t *testing.T) {
		stdout, stderr, err := quietly(t, newCLI(), "validate", valid)
		if err != nil || stdout != "" || stderr != "" {
			t.Fatalf("err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		stdout, _, err = quietly(t, newCLI(), "validate", invalid)
		if clierr.Code(err) != clierr.CodeError || !strings.Contains(stdout, "invalid.yaml:2:8: ") {
			t.Fatalf("err=%v stdout=%q", err, stdout)
		}
	})

	t.Run("test", func(t *testing.T) {
		stdout, stderr, err := quietly(t, newCLI(), "test", valid)
		if err != nil || !strings.Contains(stdout, "added (") || stderr != "" {
			t.Fatalf("err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
	})

	t.Run("apply", func(t *testing.T) {
		fs := newFakeStore(t)
		mp, ep := applyFiles(t, membership, twoEvents)
		stdout, stderr, err := quietly(t, fs.cli(), "apply", mp, "--events", ep, "--force")
		if err != nil || !strings.Contains(stdout, "+ user:u1 member team:t1") || stderr != "" || fs.writeCount() != 1 {
			t.Fatalf("err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
	})
}

// A write and a delete of one tuple are different tuples to the runner, so the
// failure has to say which is which: printed without the action, the missing
// and unexpected lines were identical.
func TestTestFailureNamesADeletesAction(t *testing.T) {
	configtest.Isolate(t)
	const wrongAction = `version: "1"
rules:
  - name: r
    when: input.type == "x"
    tuples:
      - {user: "user:1", relation: member, object: "team:1"}
tests:
  - name: expects a delete
    input: {type: x}
    expect_tuples:
      - {user: "user:1", relation: member, object: "team:1", action: delete}
`
	p := writeFile(t, t.TempDir(), "mapping.yaml", wrongAction)
	out, _ := run(t, newCLI(), "test", p)
	for _, want := range []string{"missing: user:1 member team:1 (delete)", "unexpected: user:1 member team:1\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	report, _ := run(t, newCLI(), "test", p, "--report", "json")
	if !strings.Contains(report, `"user:1 member team:1 (delete)"`) {
		t.Errorf("the JSON report drops the delete:\n%s", report)
	}
}

// A tuple whose condition context differs is another tuple to the runner, so
// the failure shows the context: without it, the missing and unexpected lines
// read the same.
func TestTestFailureShowsAConditionsContext(t *testing.T) {
	configtest.Isolate(t)
	const wrongContext = `version: "1"
rules:
  - name: r
    when: input.type == "x"
    tuples:
      - user: "user:1"
        relation: member
        object: "team:1"
        condition: in_region
        context: {region: us, tier: gold}
tests:
  - name: expects eu
    input: {type: x}
    expect_tuples:
      - user: "user:1"
        relation: member
        object: "team:1"
        condition: in_region
        context: {tier: gold, region: eu}
`
	p := writeFile(t, t.TempDir(), "mapping.yaml", wrongContext)
	out, _ := run(t, newCLI(), "test", p)
	for _, want := range []string{
		`missing: user:1 member team:1 (in_region region="eu", tier="gold")` + "\n",
		`unexpected: user:1 member team:1 (in_region region="us", tier="gold")` + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	report, _ := run(t, newCLI(), "test", p, "--report", "json")
	if !strings.Contains(report, `"user:1 member team:1 (in_region region=\"us\", tier=\"gold\")"`) {
		t.Errorf("the JSON report drops the context:\n%s", report)
	}
	// The context is the file's own text, escaped like every other field.
	hostile := strings.Replace(wrongContext, "{tier: gold, region: eu}", `{tier: gold, region: "e\u001b[31mu"}`, 1)
	out, _ = run(t, newCLI(), "test", writeFile(t, t.TempDir(), "mapping.yaml", hostile))
	if strings.Contains(out, "\x1b[31m") || !strings.Contains(out, `missing: user:1 member team:1 (in_region region="e`) {
		t.Errorf("out = %q", out)
	}
}
