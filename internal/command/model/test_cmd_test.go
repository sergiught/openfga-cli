package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
)

const passingWorkspace = "../../modeltest/testdata/docs/ofga.yaml"

func runTestCmd(t *testing.T, args []string, mutate func(*cli.CLI)) (string, string, error) {
	t.Helper()
	cfg := config.New()
	a := cli.New(log.New(io.Discard), cfg, "test")
	if mutate != nil {
		mutate(a)
	}
	c := New(a)
	cmd := c.testCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestModelTestPassesExitsZero(t *testing.T) {
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace}, nil)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
}

func TestRejectInertConnectionFlags(t *testing.T) {
	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{}
		for _, n := range []string{"profile", "api-url", "store-id", "model-id", "auth-token-file"} {
			cmd.Flags().String(n, "", "")
		}
		return cmd
	}

	// Unset connection flags are fine.
	if err := rejectInertConnectionFlags(newCmd()); err != nil {
		t.Fatalf("unset flags should pass, got %v", err)
	}

	// Each connection flag, when explicitly set, is rejected as a usage error.
	for _, name := range []string{"store-id", "api-url", "profile", "auth-token-file"} {
		cmd := newCmd()
		if err := cmd.Flags().Set(name, "x"); err != nil {
			t.Fatal(err)
		}
		err := rejectInertConnectionFlags(cmd)
		if err == nil {
			t.Errorf("--%s set: expected error, got nil", name)
			continue
		}
		if code := clierr.Code(err); code != clierr.CodeUsage {
			t.Errorf("--%s: want CodeUsage exit code, got %d", name, code)
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("--%s: error should name the flag, got %q", name, err)
		}
	}
}

// writeFailingWorkspace writes a workspace (model.fga + ofga.yaml + a test
// file) whose single test intentionally fails an assertion, and returns the
// path to its ofga.yaml manifest.
func writeFailingWorkspace(t *testing.T) string {
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

	testFile := "tests:\n  - name: wrong-assertion\n    tuples:\n      - user: user:anne\n        relation: viewer\n        object: document:1\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {viewer: false}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "docs.test.yaml"), []byte(testFile), 0o600); err != nil {
		t.Fatal(err)
	}

	return filepath.Join(dir, "ofga.yaml")
}

// TestModelTestHumanSummaryShowsDuration guards review finding S6 for the
// human surface: the summary line must show a duration (formatted like `go
// test`'s "0.02s"), so this only asserts a parenthesized "…s)" is present —
// never an exact value, since real wall-clock timing is inherently flaky to
// pin down.
func TestModelTestHumanSummaryShowsDuration(t *testing.T) {
	out, _, err := runTestCmd(t, []string{"--file", passingWorkspace}, nil)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	stripped := ansi.Strip(out)
	if !regexp.MustCompile(`\(\d+\.\d+s\)`).MatchString(stripped) {
		t.Fatalf("want summary line to contain a parenthesized duration like (0.02s), got: %q", stripped)
	}
}

// TestModelTestHumanSummaryShowsDenominatorOnPass guards that a passing run's
// summary line carries the same "N/Total" shape as a failing run's
// ("1/14 test(s) failed"), rather than a bare count ("13 test(s) passed").
func TestModelTestHumanSummaryShowsDenominatorOnPass(t *testing.T) {
	out, _, err := runTestCmd(t, []string{"--file", passingWorkspace}, nil)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	stripped := ansi.Strip(out)
	m := regexp.MustCompile(`(\d+)/(\d+) test\(s\) passed`).FindStringSubmatch(stripped)
	if m == nil {
		t.Fatalf("want summary line to match \"N/Total test(s) passed\", got: %q", stripped)
	}
	if m[1] != m[2] {
		t.Fatalf("want passed count to equal total on an all-passing run, got %s/%s", m[1], m[2])
	}
}

func TestModelTestFailuresExitCodeThree(t *testing.T) {
	manifestPath := writeFailingWorkspace(t)

	_, _, err := runTestCmd(t, []string{"--file", manifestPath}, nil)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if got := clierr.Code(err); got != clierr.CodeTestFailed {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeTestFailed, err)
	}
}

// TestModelTestFailuresPrintSingleSummary guards the duplicate-summary fix:
// the command prints exactly one authoritative "N/Total test(s) failed" line
// (summaryLine) and returns a Silent coded error, so main honors exit 3
// without re-printing a second summary of its own.
func TestModelTestFailuresPrintSingleSummary(t *testing.T) {
	manifestPath := writeFailingWorkspace(t)

	out, errOut, err := runTestCmd(t, []string{"--file", manifestPath}, nil)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if !clierr.IsSilent(err) {
		t.Fatalf("failing run error should be Silent so main prints no second summary; err=%v", err)
	}
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("clierr.Code(err) = %d, want %d", clierr.Code(err), clierr.CodeTestFailed)
	}
	combined := ansi.Strip(out + errOut)
	if n := strings.Count(combined, "test(s) failed"); n != 1 {
		t.Fatalf("want exactly one summary line, got %d\nstdout=%q\nstderr=%q", n, out, errOut)
	}
	if !strings.Contains(ansi.Strip(out), "1/1 test(s) failed") {
		t.Fatalf("want the richer N/Total summary line, got stdout=%q", out)
	}
}

// writeDenialWorkspace writes a workspace whose single test asserts
// user:anne can viewer document:1 without ever granting owner (so the check
// is a denial, expected true / got false) — a nearest-miss suggestion
// exists, unlike writeFailingWorkspace's unexpected-true case.
func writeDenialWorkspace(t *testing.T) string {
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

	return filepath.Join(dir, "ofga.yaml")
}

func TestModelTestHumanOutputShowsWhy(t *testing.T) {
	manifestPath := writeDenialWorkspace(t)

	out, _, err := runTestCmd(t, []string{"--file", manifestPath}, nil)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", clierr.Code(err), clierr.CodeTestFailed, err)
	}
	if !strings.Contains(out, "nearest miss:") {
		t.Fatalf("expected human output to contain a nearest-miss explanation; got %q", out)
	}
}

func TestModelTestJSONStdoutDoesNotContainRenderedTree(t *testing.T) {
	manifestPath := writeDenialWorkspace(t)

	out, _, err := runTestCmd(t, []string{"--file", manifestPath}, func(a *cli.CLI) { a.JSON = true })
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", clierr.Code(err), clierr.CodeTestFailed, err)
	}
	if strings.Contains(out, "├─") || strings.Contains(out, "└─") || strings.Contains(out, "nearest miss:") {
		t.Fatalf("stdout in --json mode should not contain rendered explanation text: %s", out)
	}
	var res map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &res); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", jsonErr, out)
	}
}

func TestModelTestJSONStdoutOnlyMachine(t *testing.T) {
	out, errOut, err := runTestCmd(t, []string{"--file", passingWorkspace}, func(a *cli.CLI) { a.JSON = true })
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	var res map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &res); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", jsonErr, out)
	}
	if _, ok := res["summary"]; !ok {
		t.Fatalf("stdout JSON missing summary key: %s", out)
	}

	if bytes.Contains([]byte(out), []byte("test(s) passed")) {
		t.Fatalf("stdout appears to contain human-rendered summary text: %s", out)
	}
	_ = errOut
}

func TestModelTestJSONShapeIdenticalPassFail(t *testing.T) {
	passOut, _, err := runTestCmd(t, []string{"--file", passingWorkspace}, func(a *cli.CLI) { a.JSON = true })
	if err != nil {
		t.Fatalf("passing run Execute() = %v, want nil", err)
	}

	manifestPath := writeFailingWorkspace(t)

	failOut, _, err := runTestCmd(t, []string{"--file", manifestPath}, func(a *cli.CLI) { a.JSON = true })
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("failing run clierr.Code(err) = %d, want %d (err=%v)", clierr.Code(err), clierr.CodeTestFailed, err)
	}

	var passRes, failRes map[string]any
	if err := json.Unmarshal([]byte(passOut), &passRes); err != nil {
		t.Fatalf("pass stdout not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(failOut), &failRes); err != nil {
		t.Fatalf("fail stdout not JSON: %v", err)
	}

	passSummary, ok := passRes["summary"].(map[string]any)
	if !ok {
		t.Fatalf("pass summary is not an object: %v", passRes["summary"])
	}
	failSummary, ok := failRes["summary"].(map[string]any)
	if !ok {
		t.Fatalf("fail summary is not an object: %v", failRes["summary"])
	}

	passKeys := keySet(passSummary)
	failKeys := keySet(failSummary)
	if len(passKeys) != len(failKeys) {
		t.Fatalf("summary key sets differ: pass=%v fail=%v", passKeys, failKeys)
	}
	for k := range passKeys {
		if !failKeys[k] {
			t.Fatalf("summary key sets differ: pass=%v fail=%v", passKeys, failKeys)
		}
	}
}

// TestModelTestDiscoversManifestByWalkUp verifies that a bare `ofga model
// test` (no --file, no positional path) walks up from the current directory
// to find ofga.yaml in a parent, mirroring go.mod discovery.
func TestModelTestDiscoversManifestByWalkUp(t *testing.T) {
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

	testFile := "tests:\n  - name: anne-is-viewer\n    tuples:\n      - user: user:anne\n        relation: viewer\n        object: document:1\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {viewer: true}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "docs.test.yaml"), []byte(testFile), 0o600); err != nil {
		t.Fatal(err)
	}

	subdir := filepath.Join(dir, "nested", "deeper")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subdir)

	_, _, err := runTestCmd(t, nil, nil)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil (should discover the parent ofga.yaml by walking up)", err)
	}
}

func TestModelTestZeroMatchIsUsageOrError(t *testing.T) {
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--run", "nope/nope"}, nil)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "no tests matched") {
		t.Fatalf("err = %v, want message mentioning %q", err, "no tests matched")
	}
	// A --run pattern that selects nothing is a bad invocation, not a runtime
	// failure: it must exit CodeUsage (2), not the generic CodeError (1).
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeUsage, err)
	}
}

// TestModelTestBadReportValueUsage guards that an invalid --report value is
// rejected up front (CodeUsage), before the suite runs at all.
func TestModelTestBadReportValueUsage(t *testing.T) {
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--report", "bogus"}, nil)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeUsage, err)
	}
}

func TestModelTestReportFileWithoutReportUsage(t *testing.T) {
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--report-file", filepath.Join(t.TempDir(), "out.xml")}, nil)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeUsage, err)
	}
}

func TestModelTestReportJUnitWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.xml")
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--report", "junit", "--report-file", path}, nil)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("report file not written: %v", readErr)
	}
	if !strings.Contains(string(data), "<testsuites") {
		t.Fatalf("report file should contain JUnit XML, got: %q", data)
	}
}

func TestModelTestReportJSONWithoutFilePrintsToStdout(t *testing.T) {
	out, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--report", "json"}, nil)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	// The human summary is rendered first, so the JSON report is a trailing
	// object appended to stdout rather than the whole of stdout.
	i := strings.Index(out, "{")
	if i < 0 {
		t.Fatalf("stdout should contain a JSON report object, got: %q", out)
	}
	var res map[string]any
	if jsonErr := json.Unmarshal([]byte(out[i:]), &res); jsonErr != nil {
		t.Fatalf("trailing stdout is not the JSON report: %v\nstdout=%s", jsonErr, out)
	}
	if _, ok := res["summary"]; !ok {
		t.Fatalf("stdout JSON report missing summary key: %s", out)
	}
}

// TestModelTestReportToStdoutInMachineModeIsUsage guards the fix for
// machine-mode stdout corruption: --report without --report-file
// prints to the terminal, which would put a second document on stdout
// alongside the JSON/YAML results in -o json/yaml mode. It must be rejected
// up front (CodeUsage) instead. With --report-file set, the same
// combination is fine: the report goes to the file and stdout stays a single
// JSON document.
func TestModelTestReportToStdoutInMachineModeIsUsage(t *testing.T) {
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--report", "json"}, func(a *cli.CLI) { a.JSON = true })
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeUsage, err)
	}

	path := filepath.Join(t.TempDir(), "report.json")
	out, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--report", "json", "--report-file", path}, func(a *cli.CLI) { a.JSON = true })
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	var res map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &res); jsonErr != nil {
		t.Fatalf("stdout is not a single JSON document: %v\nstdout=%s", jsonErr, out)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("report file not written: %v", readErr)
	}
	if !strings.Contains(string(data), "summary") {
		t.Fatalf("report file should contain the JSON report, got: %q", data)
	}
}

// TestModelTestSeedFlagRemoved asserts the --seed flag no longer exists: it was
// replaced by --playground as the sole TUI entry point, so passing it must fail
// as an unknown flag (a bad invocation → CodeUsage).
func TestModelTestSeedFlagRemoved(t *testing.T) {
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--seed", "docs/anne-is-viewer"}, nil)
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeUsage, err)
	}
}

// TestModelTestCoverageModeFlagRemoved asserts --coverage-mode no longer
// exists: coverage is always branch-grained now, so passing it must fail as
// an unknown flag (a bad invocation → CodeUsage).
func TestModelTestCoverageModeFlagRemoved(t *testing.T) {
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--coverage", "--coverage-mode", "relation"}, nil)
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeUsage, err)
	}
}

func keySet(m map[string]any) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func TestModelTestCoverageTablePrinted(t *testing.T) {
	out, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--coverage"}, nil)
	if err != nil && clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("Execute() = %v, want nil or CodeTestFailed", err)
	}
	if !strings.Contains(out, "document") {
		t.Fatalf("stdout should mention the \"document\" type, got: %q", out)
	}
	if !strings.Contains(out, "%") {
		t.Fatalf("stdout should contain a coverage percent, got: %q", out)
	}
	if !strings.Contains(out, "coverage:") {
		t.Fatalf("stdout should contain the coverage caption, got: %q", out)
	}
	if !strings.Contains(out, "grant-based") {
		t.Fatalf("stdout should contain the coverage-meaning disclosure note, got: %q", out)
	}
}

func TestModelTestCoveragePlainHasNoBoxDrawing(t *testing.T) {
	output.Plain = true
	t.Cleanup(func() { output.Plain = false })
	out, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--coverage"}, nil)
	if err != nil && clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("Execute() = %v, want nil or CodeTestFailed", err)
	}
	if !strings.Contains(out, "coverage:") {
		t.Fatalf("stdout should contain the plain coverage caption, got: %q", out)
	}
	if strings.ContainsRune(out, '─') {
		t.Fatalf("plain output should not contain box-drawing runes, got: %q", out)
	}
	output.Plain = false
	humanOut, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--coverage"}, nil)
	if err != nil && clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("Execute() = %v, want nil or CodeTestFailed", err)
	}
	if !strings.Contains(humanOut, "\x1b[") {
		t.Fatalf("non-plain output should still render the styled coverage caption, got: %q", humanOut)
	}
}

func TestModelTestCoverageDetailShowsBranches(t *testing.T) {
	out, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--coverage", "--coverage-detail"}, nil)
	if err != nil && clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("Execute() = %v, want nil or CodeTestFailed", err)
	}
	if !strings.Contains(out, style.IconCheck) && !strings.Contains(out, style.IconCircle) {
		t.Fatalf("stdout should contain per-branch covered markers, got: %q", out)
	}
}

func TestModelTestCoverageMinGateFails(t *testing.T) {
	out, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--coverage", "--coverage-min", "100"}, nil)
	if got := clierr.Code(err); got != clierr.CodeTestFailed {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeTestFailed, err)
	}
	if !strings.Contains(out, "document") || !strings.Contains(out, "%") {
		t.Fatalf("coverage table should still be rendered before the gate error, got: %q", out)
	}
}

func TestModelTestCoverageMinWithoutBranchIsUsage(t *testing.T) {
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--coverage-min", "50"}, nil)
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeUsage, err)
	}
}

// writeMultiModelWorkspace writes a workspace whose two test files override
// `model:` to two distinct models, so --coverage has no single model to
// enumerate against. Both tests pass, so the run itself is green.
func writeMultiModelWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	modelA := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model-a.fga"), []byte(modelA), 0o600); err != nil {
		t.Fatal(err)
	}
	modelB := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define editor: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model-b.fga"), []byte(modelB), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := "version: 1\nmodel: ./model-a.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"
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

	return filepath.Join(dir, "ofga.yaml")
}

// TestModelTestCoverageMultiModelIsUsage guards that the CLI preserves its
// historical exit 2 for --coverage on a workspace with no single model, even
// though modeltest.Run now treats that as non-fatal (the tests run, Coverage
// is nil, CoverageError is set). The passing results must still be rendered
// before the coded error.
func TestModelTestCoverageMultiModelIsUsage(t *testing.T) {
	manifestPath := writeMultiModelWorkspace(t)

	out, _, err := runTestCmd(t, []string{"--file", manifestPath, "--coverage"}, nil)
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeUsage, err)
	}
	if !strings.Contains(ansi.Strip(out), "test(s) passed") {
		t.Fatalf("passing results should be rendered before the coverage usage error, got: %q", out)
	}
}

// TestModelTestMultiModelCoverageStillWritesReport guards that --report is
// independent of --coverage: even though --coverage on a multi-model
// workspace still exits CodeUsage (2), the report is generated from the
// results, which are already known good, so it must be written regardless.
func TestModelTestMultiModelCoverageStillWritesReport(t *testing.T) {
	manifestPath := writeMultiModelWorkspace(t)
	reportPath := filepath.Join(t.TempDir(), "out.json")

	_, _, err := runTestCmd(t, []string{"--file", manifestPath, "--coverage", "--report", "json", "--report-file", reportPath}, nil)
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeUsage, err)
	}

	if _, statErr := os.Stat(reportPath); statErr != nil {
		t.Fatalf("report file not written: %v", statErr)
	}
}

// TestModelTestMultiModelWithoutCoverageExitsZero confirms the same workspace
// runs fine (exit 0) when --coverage is not requested: the coverage failure
// only matters when coverage was asked for.
func TestModelTestMultiModelWithoutCoverageExitsZero(t *testing.T) {
	manifestPath := writeMultiModelWorkspace(t)

	if _, _, err := runTestCmd(t, []string{"--file", manifestPath}, nil); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
}

func TestModelTestInvalidExplainValueIsUsage(t *testing.T) {
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--explain", "bogus"}, nil)
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("clierr.Code(err) = %d, want %d (err=%v)", got, clierr.CodeUsage, err)
	}
}

func TestModelTestCoverageJSONMachineShape(t *testing.T) {
	out, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--coverage"}, func(a *cli.CLI) { a.JSON = true })
	if err != nil && clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("Execute() = %v, want nil or CodeTestFailed", err)
	}

	var res map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &res); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", jsonErr, out)
	}
	cov, ok := res["coverage"].(map[string]any)
	if !ok {
		t.Fatalf("stdout JSON missing coverage object: %s", out)
	}
	for _, key := range []string{"total", "covered", "percent", "complete"} {
		if _, ok := cov[key]; !ok {
			t.Fatalf("coverage object missing %q key: %s", key, out)
		}
	}
	if _, ok := cov["mode"]; ok {
		t.Fatalf("coverage object should no longer have a %q key: %s", "mode", out)
	}

	types, ok := cov["types"].([]any)
	if !ok || len(types) == 0 {
		t.Fatalf("coverage.types missing or empty: %s", out)
	}
	firstType, ok := types[0].(map[string]any)
	if !ok {
		t.Fatalf("coverage.types[0] is not an object: %s", out)
	}
	relations, ok := firstType["relations"].([]any)
	if !ok || len(relations) == 0 {
		t.Fatalf("coverage.types[0].relations missing or empty: %s", out)
	}
	firstRelation, ok := relations[0].(map[string]any)
	if !ok {
		t.Fatalf("coverage.types[0].relations[0] is not an object: %s", out)
	}
	if _, ok := firstRelation["branches"]; !ok {
		t.Fatalf("coverage.types[0].relations[0] missing branches key: %s", out)
	}

	if strings.Contains(out, "relation-grained") {
		t.Fatalf("stdout should not contain the rendered human coverage table in machine mode, got: %q", out)
	}
}

func TestWatchRejectsIncompatibleFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--file", passingWorkspace, "--watch", "--report", "junit"},
		{"--file", passingWorkspace, "--watch", "--coverage-diff", "main"},
	} {
		_, _, err := runTestCmd(t, args, nil)
		if err == nil {
			t.Fatalf("%v: expected an error", args)
		}
		if code := clierr.Code(err); code != clierr.CodeUsage {
			t.Errorf("%v: want CodeUsage, got %d", args, code)
		}
	}
}

func TestEngineSelectionRejectsIncompatibleFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--file", passingWorkspace, "--openfga-image", "x", "--server-addr", "y"},
		{"--file", passingWorkspace, "--openfga-image", "x", "--playground"},
		{"--file", passingWorkspace, "--openfga-image", "x", "--watch"},
	} {
		_, _, err := runTestCmd(t, args, nil)
		if err == nil {
			t.Fatalf("%v: expected an error", args)
		}
		if code := clierr.Code(err); code != clierr.CodeUsage {
			t.Errorf("%v: want CodeUsage, got %d", args, code)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeGitWorkspace writes a passing single-model workspace inside a fresh git
// repo and commits it, so --coverage-diff has a base ref (HEAD) to diff against.
func writeGitWorkspace(t *testing.T) string {
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
	testFile := "tests:\n  - name: anne-is-viewer\n    tuples:\n      - user: user:anne\n        relation: viewer\n        object: document:1\n    check:\n      - user: user:anne\n        object: document:1\n        assertions: {viewer: true}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "docs.test.yaml"), []byte(testFile), 0o600); err != nil {
		t.Fatal(err)
	}

	runGit(t, dir, "init")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-m", "init")
	return filepath.Join(dir, "ofga.yaml")
}

// TestModelTestCoverageDiffWithCoverageMinNotUsage guards the ordering fix:
// --coverage-diff implies --coverage, so combining it with --coverage-min (or
// --coverage-detail) must NOT trip the "requires --coverage" usage guard.
func TestModelTestCoverageDiffWithCoverageMinNotUsage(t *testing.T) {
	manifestPath := writeGitWorkspace(t)

	_, _, err := runTestCmd(t, []string{"--file", manifestPath, "--coverage-diff", "HEAD", "--coverage-min", "80"}, nil)
	if code := clierr.Code(err); code == clierr.CodeUsage {
		t.Fatalf("--coverage-diff HEAD --coverage-min 80 should not be a usage error (--coverage-diff implies --coverage), got CodeUsage: %v", err)
	}
}

// writeFailingMultiModelWorkspace writes a multi-model workspace (so --coverage
// has no single model to enumerate against) in which one test genuinely fails.
func writeFailingMultiModelWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	modelA := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model-a.fga"), []byte(modelA), 0o600); err != nil {
		t.Fatal(err)
	}
	modelB := "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define editor: [user]\n"
	if err := os.WriteFile(filepath.Join(dir, "model-b.fga"), []byte(modelB), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := "version: 1\nmodel: ./model-a.fga\ntests:\n  - \"tests/**/*.test.yaml\"\n"
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
	// b-test grants editor but asserts editor:false, so it fails.
	testB := "model: ../model-b.fga\ntests:\n  - name: b-test\n    tuples:\n      - user: user:bob\n        relation: editor\n        object: document:1\n    check:\n      - user: user:bob\n        object: document:1\n        assertions: {editor: false}\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "b.test.yaml"), []byte(testB), 0o600); err != nil {
		t.Fatal(err)
	}

	return filepath.Join(dir, "ofga.yaml")
}

// TestModelTestMultiModelCoverageWithFailuresExitsThree guards the exit-code
// precedence fix: a real test failure on a multi-model workspace under
// --coverage must exit CodeTestFailed (3), not be masked by the coverage-nil
// usage error (2) that CI keys on differently.
func TestModelTestMultiModelCoverageWithFailuresExitsThree(t *testing.T) {
	manifestPath := writeFailingMultiModelWorkspace(t)

	_, _, err := runTestCmd(t, []string{"--file", manifestPath, "--coverage"}, nil)
	if got := clierr.Code(err); got != clierr.CodeTestFailed {
		t.Fatalf("clierr.Code(err) = %d, want %d (CodeTestFailed) — the test failure must win over the coverage usage error (err=%v)", got, clierr.CodeTestFailed, err)
	}
}

// TestWatchAllowsHumanOutputRejectsMachine guards that --watch accepts human
// output formats (-o table) while still rejecting machine output (--json).
func TestWatchAllowsHumanOutputRejectsMachine(t *testing.T) {
	// --json is machine output: rejected up front as a usage error.
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--watch"}, func(a *cli.CLI) { a.JSON = true })
	if code := clierr.Code(err); code != clierr.CodeUsage {
		t.Fatalf("--watch --json: want CodeUsage, got %d (err=%v)", code, err)
	}

	// -o table is a human format: allowed. Run with an already-cancelled
	// context so the watch loop does its single initial run and returns
	// immediately instead of blocking on file events.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := config.New()
	a := cli.New(log.New(io.Discard), cfg, "test")
	a.Output = "table"
	c := New(a)
	cmd := c.testCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--file", passingWorkspace, "--watch"})
	if err := cmd.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("--watch -o table cancellation = %v, want context.Canceled", err)
	}
}

func TestModelTestRejectsInvalidNumericFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--parallel", "-1"},
		{"--slowest", "-1"},
		{"--timeout", "-1s"},
		{"--coverage", "--coverage-min", "-1"},
		{"--coverage", "--coverage-min", "101"},
		{"--coverage", "--coverage-min", "NaN"},
		{"--coverage-min", "0"},
	} {
		_, _, err := runTestCmd(t, args, nil)
		if clierr.Code(err) != clierr.CodeUsage {
			t.Errorf("%v: code = %d, want usage (err=%v)", args, clierr.Code(err), err)
		}
	}
}

func TestModelTestRejectsInvalidRunGlob(t *testing.T) {
	_, _, err := runTestCmd(t, []string{"--file", passingWorkspace, "--run", "["}, nil)
	if clierr.Code(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "invalid --run glob") {
		t.Fatalf("error = %v (code %d), want actionable usage error", err, clierr.Code(err))
	}
}

func TestMalformedManifestDoesNotSuggestInit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte("version: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := runTestCmd(t, []string{"--file", dir}, nil)
	if err == nil {
		t.Fatal("malformed manifest should fail")
	}
	if strings.Contains(err.Error(), "test init") {
		t.Fatalf("malformed existing manifest must not suggest scaffolding over it: %v", err)
	}
}

// TestModelTestNoWorkspaceHintsInit guards review finding M2: when no
// ofga.yaml/workspace is found, the error points the user at `test init`
// instead of a bare load failure.
func TestModelTestNoWorkspaceHintsInit(t *testing.T) {
	t.Chdir(t.TempDir())

	_, _, err := runTestCmd(t, nil, nil)
	if err == nil {
		t.Fatal("Execute() = nil, want an error for a missing workspace")
	}
	if !strings.Contains(err.Error(), "test init") {
		t.Fatalf("missing-workspace error should hint at `test init`, got: %v", err)
	}
}

// TestModelTestFlagCompletions guards review finding S1: --report and
// --explain complete their fixed enum values.
func TestModelTestFlagCompletions(t *testing.T) {
	a := cli.New(log.New(io.Discard), config.New(), "test")
	cmd := New(a).testCmd()

	for _, tc := range []struct {
		flag string
		want []string
	}{
		{"report", []string{"junit", "json", "github"}},
		{"explain", []string{"auto", "full"}},
	} {
		f, ok := cmd.GetFlagCompletionFunc(tc.flag)
		if !ok {
			t.Fatalf("no completion registered for --%s", tc.flag)
		}
		got, _ := f(cmd, nil, "")
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("--%s completion = %v, want %v", tc.flag, got, tc.want)
		}
	}
}

// TestModelTestReportJSONHumanSummaryOnStderr guards review finding S4: when
// a junit/json report is printed to stdout (no --report-file) in human mode,
// stdout carries only the report and the human summary goes to stderr, so the
// two documents are never interleaved.
func TestModelTestReportJSONHumanSummaryOnStderr(t *testing.T) {
	out, errOut, err := runTestCmd(t, []string{"--file", passingWorkspace, "--report", "json"}, nil)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if strings.Contains(ansi.Strip(out), "test(s) passed") {
		t.Fatalf("human summary leaked onto stdout alongside the report: %q", out)
	}
	var res map[string]any
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); jsonErr != nil {
		t.Fatalf("stdout should be a single JSON report: %v\nstdout=%s", jsonErr, out)
	}
	if !strings.Contains(ansi.Strip(errOut), "test(s) passed") {
		t.Fatalf("human summary should be on stderr, got stderr=%q", errOut)
	}
}
