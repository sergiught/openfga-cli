package mapping

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/openfga/mapper/language"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/atomicfile"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
)

func (c *Command) newTestCmd() *cobra.Command {
	var (
		filter, report, reportFile string
		failFast                   bool
	)
	cmd := &cobra.Command{
		Use:   "test [file]",
		Short: "Run the tests embedded in a mapping file",
		Long: "Run a mapping file's tests: block with openfga/mapper's runner. Each test evaluates its input " +
			"and compares the tuples (and tuple filters) produced with the ones it expects. " +
			"With no file argument it uses ./mapping.yaml.\n\n" +
			"Exit codes: 0 all passed · 3 a test failed · 1 the file does not compile, has no tests, or --run matched none · 2 usage.",
		Example: `ofga mappings test
ofga mappings test sync/auth0.yaml --run member
ofga mappings test --report junit --report-file mapping-tests.xml`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runTest(cmd, targetPath(args), filter, failFast, report, reportFile)
		},
	}
	cmd.Flags().StringVar(&filter, "run", "", "run only tests whose name contains this text")
	cmd.Flags().BoolVar(&failFast, "fail-fast", false, "stop at the first failing test")
	cmd.Flags().StringVar(&report, "report", "", `write a report in this format ("junit", "json", or "github"); to --report-file if set, otherwise printed to the terminal`)
	cmd.Flags().StringVar(&reportFile, "report-file", "", "path to write the --report file to (requires --report)")
	_ = cmd.RegisterFlagCompletionFunc("report", cobra.FixedCompletions([]string{"junit", "json", "github"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func (c *Command) runTest(cmd *cobra.Command, path, filter string, failFast bool, report, reportFile string) error {
	// Same rules and messages as `model test`.
	if report != "" && report != "junit" && report != "json" && report != "github" {
		return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("invalid --report %q (want \"junit\", \"json\", or \"github\")", report))
	}
	if report == "" && reportFile != "" {
		return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--report-file requires --report"))
	}
	machineOut := c.cli.JSON || c.cli.YAML
	if report != "" && reportFile == "" && machineOut {
		return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--report with -o json/yaml needs --report-file (stdout can hold only one document)"))
	}

	out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
	// Problem lines are not the machine payload: keep stdout clean for jq.
	problemsOut := out
	if machineOut {
		problemsOut = errw
	}
	f, problems, err := loadMappingFile(path)
	if err != nil {
		return err
	}
	if f == nil {
		return failToLoad(problemsOut, machineOut, path, problems)
	}
	if len(f.Doc.Tests) == 0 {
		// A file that does not compile says so before it says it has no tests.
		if _, diags, err := mapping.CompileSource(f.Src); err != nil {
			return failToLoad(problemsOut, machineOut, path, mapping.FindingsFrom(diags))
		}
		return fmt.Errorf("no tests in %s: add a tests: block, or open it in 'ofga mappings %s' to save one per rule with a sample event", path, shellQuote(path))
	}
	res, err := mapping.RunTests(cmd.Context(), f, filter, failFast)
	var le *mapping.LoadError
	if errors.As(err, &le) {
		return failToLoad(problemsOut, machineOut, path, mapping.FindingsFrom(le.Diagnostics))
	}
	if err != nil {
		return err
	}
	if len(res.Tests) == 0 && filter != "" {
		return fmt.Errorf("no test name contains %q", filter)
	}

	results := testReport(res, path)
	// A junit/json report on stdout is a machine payload: keep the human
	// lines off it, as `model test` does.
	reportToStdout := (report == "junit" || report == "json") && reportFile == ""
	switch {
	case machineOut:
		if err := output.Emit(out, c.cli.YAML, results); err != nil {
			return err
		}
	case reportToStdout:
		printTestOutcomes(errw, path, res)
	default:
		printTestOutcomes(out, path, res)
	}
	// Under machine output the error envelope is the only thing on stderr for
	// a failing run; it carries the same summary.
	if !machineOut || res.Failed() == 0 {
		printTestSummary(errw, res)
	}

	if report != "" {
		if err := writeTestReport(cmd, report, reportFile, results); err != nil {
			return err
		}
	}
	if res.Failed() > 0 {
		return clierr.Silent(clierr.CodeTestFailed, testSummaryLine(res))
	}
	return nil
}

// failToLoad prints a file's findings the way validate does and exits 1. Under
// machine output the findings travel in the error envelope's details instead,
// since the envelope is then the only thing on stderr.
func failToLoad(w io.Writer, machine bool, path string, problems []mapping.Finding) error {
	var details []map[string]any
	if machine {
		for _, p := range problems {
			details = append(details, map[string]any{"file": path, "line": p.Line, "column": p.Column, "field": p.Field, "message": p.Message})
		}
	} else if err := printProblems(w, path, problems); err != nil {
		return err
	}
	return clierr.WithDetails(clierr.WithKind(clierr.KindValidation, clierr.Silent(clierr.CodeError, problemsSummary(path, len(problems)))), details...)
}

// problemsSummary is the one-line "file: N problem(s)" summary validate prints.
func problemsSummary(path string, n int) string {
	return fmt.Sprintf("%s: %s", path, mapping.Plural(n, "problem"))
}

// writeTestReport writes the report to reportFile atomically, or to stdout.
func writeTestReport(cmd *cobra.Command, format, reportFile string, res *modeltest.Results) error {
	if reportFile == "" {
		return modeltest.WriteReport(format, cmd.OutOrStdout(), res)
	}
	if dir := filepath.Dir(reportFile); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create report dir: %w", err)
		}
	}
	f, err := atomicfile.Create(reportFile, 0o644)
	if err != nil {
		return fmt.Errorf("create report file: %w", err)
	}
	// Discarded unless the report is written and flushed in full.
	defer f.Abort()
	if err := modeltest.WriteReport(format, f, res); err != nil {
		return err
	}
	return f.Commit()
}

// printTestOutcomes writes one line per test and, under each failure, why it failed.
func printTestOutcomes(w io.Writer, path string, run *mapping.TestRun) {
	for _, o := range run.Tests {
		name := output.PlainField(o.Name)
		if o.Passed {
			_, _ = fmt.Fprintf(w, "%s %s (%s)\n", testMark(style.IconCheck, true), name, formatTestDuration(o.Duration))
			continue
		}
		_, _ = fmt.Fprintf(w, "%s %s — %s:%d\n", testMark(style.IconCross, false), name, output.PlainField(path), o.Line)
		detail(w, "missing", o.Missing, tupleText)
		detail(w, "unexpected", o.Unexpected, tupleText)
		detail(w, "missing filter", o.MissingFilters, filterText)
		detail(w, "unexpected filter", o.UnexpectedFilters, filterText)
		detail(w, "not covered by a filter", o.Uncovered, tupleText)
		if len(o.Fired) == 0 && o.Err == nil {
			_, _ = fmt.Fprintln(w, "    no rule matched")
		} else if len(o.Fired) > 0 {
			fired := make([]string, len(o.Fired))
			for i, n := range o.Fired {
				fired[i] = output.PlainField(n)
			}
			_, _ = fmt.Fprintf(w, "    fired: %s\n", strings.Join(fired, ", "))
		}
		if o.Err != nil {
			_, _ = fmt.Fprintf(w, "    error: %s\n", output.PlainField(o.Err.Error()))
		}
	}
}

func detail[T any](w io.Writer, label string, items []T, text func(T) string) {
	for _, it := range items {
		_, _ = fmt.Fprintf(w, "    %s: %s\n", label, text(it))
	}
}

// tupleText is mapping.TupleText with each field made safe for a terminal: the
// values are the mapping file's and its events' own.
func tupleText(t language.Tuple) string { return mapping.TupleText(t, output.PlainField) }

// filterText prints only the fields a filter sets, plus its action.
func filterText(f language.TupleFilter) string {
	var parts []string
	for _, s := range []string{f.User, f.Relation, f.Object, string(f.Action)} {
		if s != "" {
			parts = append(parts, output.PlainField(s))
		}
	}
	return strings.Join(parts, " ")
}

// testMark styles a pass/fail mark unless output is plain or colorless.
func testMark(icon string, ok bool) string {
	if output.Plain {
		return icon
	}
	col := style.Red
	if ok {
		col = style.Green
	}
	return lipgloss.NewStyle().Foreground(col).Render(icon)
}

func formatTestDuration(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	return d.Round(time.Millisecond).String()
}

func printTestSummary(w io.Writer, run *mapping.TestRun) {
	failed := run.Failed()
	line := testSummaryLine(run)
	if failed > 0 {
		output.Errorf(w, "%s", line)
		return
	}
	output.Successf(w, "%s", line)
}

// testSummaryLine is the "N passed, M failed" line printTestSummary prints.
func testSummaryLine(run *mapping.TestRun) string {
	failed := run.Failed()
	line := fmt.Sprintf("%d passed, %d failed", len(run.Tests)-failed, failed)
	if run.Filtered > 0 {
		line += fmt.Sprintf(", %d filtered", run.Filtered)
	}
	if run.Skipped > 0 {
		line += fmt.Sprintf(", %d not run (--fail-fast)", run.Skipped)
	}
	return line
}

// testReport converts a run to the report model shared with `model test`.
func testReport(run *mapping.TestRun, path string) *modeltest.Results {
	// 1 matches modeltest's unexported resultsSchemaVersion (internal/modeltest/result.go).
	res := &modeltest.Results{SchemaVersion: 1, Label: "mapping test"}
	// Named from the repository root, as model test names its files, so CI
	// annotations and JUnit consumers find the file.
	file := modeltest.RepoPath(path)
	for _, o := range run.Tests {
		tr := modeltest.TestResult{Name: o.Name, File: file, Line: o.Line, Passed: o.Passed, DurationMs: o.Duration.Milliseconds()}
		if o.Err != nil {
			tr.Error = o.Err.Error()
		}
		tr.Assertions = testAssertions(o)
		res.Tests = append(res.Tests, tr)
		res.Summary.Total++
		if o.Passed {
			res.Summary.Passed++
		} else {
			res.Summary.Failed++
		}
		res.Summary.DurationMs += o.Duration.Milliseconds()
	}
	res.Summary.MatchedFiles, res.Summary.MatchedTests = 1, len(run.Tests)
	return res
}

// testAssertions turns an outcome into one assertion per failing category,
// or a single passing tuples assertion.
func testAssertions(o mapping.TestOutcome) []modeltest.AssertionResult {
	var out []modeltest.AssertionResult
	if len(o.Missing) > 0 || len(o.Unexpected) > 0 {
		out = append(out, modeltest.AssertionResult{
			Kind: "tuples", Subject: "tuples",
			Expected: sortedStrings(o.Expected, reportTuple), Got: sortedStrings(o.Actual, reportTuple),
			Explain: &modeltest.Explain{SetDiff: &modeltest.SetDiff{
				Missing: sortedStrings(o.Missing, reportTuple), Unexpected: sortedStrings(o.Unexpected, reportTuple),
			}},
		})
	}
	if len(o.MissingFilters) > 0 || len(o.UnexpectedFilters) > 0 {
		out = append(out, modeltest.AssertionResult{
			Kind: "tuple_filters", Subject: "tuple filters",
			Expected: sortedStrings(o.ExpectedFilters, reportFilter), Got: sortedStrings(o.ActualFilters, reportFilter),
			Explain: &modeltest.Explain{SetDiff: &modeltest.SetDiff{
				Missing: sortedStrings(o.MissingFilters, reportFilter), Unexpected: sortedStrings(o.UnexpectedFilters, reportFilter),
			}},
		})
	}
	if len(o.Uncovered) > 0 {
		out = append(out, modeltest.AssertionResult{
			Kind: "coverage", Subject: "writes covered by a tuple filter",
			Expected: "every write covered",
			Got:      "not covered by a filter: " + strings.Join(sortedStrings(o.Uncovered, reportTuple), ", "),
		})
	}
	if len(out) == 0 {
		out = append(out, modeltest.AssertionResult{
			Kind: "tuples", Subject: "tuples", Passed: o.Passed,
			Expected: sortedStrings(o.Expected, reportTuple), Got: sortedStrings(o.Actual, reportTuple),
		})
	}
	return out
}

func sortedStrings[T any](items []T, text func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = text(it)
	}
	sort.Strings(out)
	return out
}

// reportTuple is mapping.TupleText with the values verbatim: a report is data,
// and each format encodes it its own way.
func reportTuple(t language.Tuple) string { return mapping.TupleText(t, nil) }

// reportFilter shows unset fields as *.
func reportFilter(f language.TupleFilter) string {
	star := func(s string) string {
		if s == "" {
			return "*"
		}
		return s
	}
	return star(f.User) + " " + star(f.Relation) + " " + star(f.Object) + " " + string(f.Action)
}
