package model

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/command/playground"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
)

func (c *Command) testCmd() *cobra.Command {
	var (
		file           string
		modelFlag      string
		fixturesFlag   []string
		testsFlag      []string
		run            string
		parallel       int
		dedupe         bool
		report         string
		reportFile     string
		explain        string
		coverage       bool
		coverageMin    float64
		coverageDetail bool
		coverageDiff   string
		playground     bool
		noTUI          bool
		watch          bool
		openfgaImage   string
		serverAddr     string
		timeout        time.Duration
		failFast       bool
		slowest        int
	)
	cmd := &cobra.Command{
		Use:   "test [path]",
		Short: "Run authorization model tests against an embedded (or a real/versioned) OpenFGA server",
		Example: `  ofga model test
  ofga model test --file ofga.yaml
  ofga model test --model model.fga --tests 'tests/**/*.test.yaml'   # no manifest
  ofga model test --run "documents/*"
  ofga model test --watch
  ofga model test --coverage --coverage-min 80
  ofga model test --coverage-diff main
  ofga model test --report junit --report-file results.xml
  ofga model test --report json
  ofga model test --playground
  ofga model test --openfga-image openfga/openfga:v1.5.0
  ofga model test --server-addr localhost:8081`,
		Long: "Run the tests declared by an ofga workspace (an ofga.yaml manifest and its *.test.yaml files). By default they run against an in-process, embedded OpenFGA server with no external dependency, real store, or profile involved; --openfga-image runs them against a specific OpenFGA version in Docker and --server-addr against an already-running server.\n\n" +
			"Workspace: ofga.yaml is discovered by walking up from the current directory (like go.mod) unless a positional path or --file overrides it. Each test runs against its own fresh store.\n\n" +
			"Explain: a failure prints expected/got values, a resolution tree, and a nearest-miss suggestion. --explain full shows this for every assertion, pass or fail.\n\n" +
			"Coverage: --coverage reports per-type rewrite-branch coverage against the model, --coverage-detail adds full per-branch detail to the human report, and --coverage-min gates the run on it (exit 3 if unmet). Coverage is grant-based: a rewrite branch (a direct/wildcard type, a computed or tuple-to-userset arm, a 'but not' exclusion, or an ABAC condition outcome) counts covered only when a check assertion showed that specific arm granting — so an arm you never exercise stays uncovered even if its relation is otherwise tested. list_objects/list_users assertions have no per-arm tree, so they credit at relation granularity; use check assertions for precise per-arm coverage. --coverage-diff compares against a git ref and fails (exit 3) on newly-added branches no test covers.\n\n" +
			"Reports: --report writes a CI-friendly report to --report-file (or the terminal): junit (XML), json (the same result shape as -o json, for writing to a file), or github (GitHub Actions ::error annotations so failures surface in the Actions log).\n\n" +
			"Playground: --playground, after the run, boots the embedded server over HTTP and opens the interactive playground against a failing test's seeded world (on a TTY) so you can explore and drill into every result; the seeded data is shown under a clearly-labeled ephemeral profile and is never written to your real config.\n\n" +
			"Exit codes: 0 success · 1 error · 2 usage · 3 test failure or coverage gate · 4 network · 130 canceled.",
		Args: cobra.MaximumNArgs(1),
		// This command reports the test-failure/usage error itself (via the
		// exit code and rendered output), so cobra's default "Error: ..." +
		// usage dump would duplicate that and — worse — pollute a clean JSON
		// stdout. Silence both here rather than relying on the root command's
		// settings, since this command is also exercised standalone in tests.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectInertConnectionFlags(cmd); err != nil {
				return err
			}
			if explain != "" && explain != "auto" && explain != "full" {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("invalid --explain %q (want \"auto\" or \"full\")", explain))
			}
			if coverageDiff != "" {
				coverage = true // --coverage-diff implies --coverage, so enable it before the guards below
			}
			if !coverage && coverageDetail {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--coverage-detail requires --coverage"))
			}
			if !coverage && coverageMin > 0 {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--coverage-min requires --coverage"))
			}
			if openfgaImage != "" && serverAddr != "" {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--openfga-image and --server-addr can't be combined"))
			}
			remoteEngine := openfgaImage != "" || serverAddr != ""
			if remoteEngine && playground {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--playground drills into an embedded seeded world and can't be combined with --openfga-image/--server-addr"))
			}
			if report != "" && report != "junit" && report != "json" && report != "github" {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("invalid --report %q (want \"junit\", \"json\", or \"github\")", report))
			}
			if report == "" && reportFile != "" {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--report-file requires --report"))
			}
			if report != "" && reportFile == "" && (c.cli.JSON || c.cli.YAML) {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--report without --report-file prints to stdout, which conflicts with -o json/yaml; pass --report-file"))
			}
			path := file
			if len(args) == 1 {
				if cmd.Flags().Changed("file") {
					output.Notef(cmd.ErrOrStderr(), "a positional path argument was given, so --file is ignored")
				}
				path = args[0]
			}

			wsOpts := modeltest.WorkspaceOptions{Model: modelFlag, Fixtures: fixturesFlag, Tests: testsFlag}

			if watch {
				// -o json/yaml already set c.cli.JSON/YAML in resolveOutput, so
				// checking those covers the machine formats; -o table/plain are
				// human formats and stay allowed (matching bare --plain).
				if c.cli.JSON || c.cli.YAML {
					return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--watch is interactive and can't be combined with machine output (--json/--yaml)"))
				}
				if report != "" || playground || coverageDiff != "" {
					return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--watch can't be combined with --report, --playground, or --coverage-diff"))
				}
				if remoteEngine {
					return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--watch can't be combined with --openfga-image/--server-addr (a container per change is too slow)"))
				}
				return runWatch(cmd, c.cli, path, wsOpts, watchConfig{
					run: run, parallel: parallel, dedupe: dedupe, explain: explain,
					coverage: coverage, coverageDetail: coverageDetail,
				})
			}

			ws, err := modeltest.LoadWorkspaceWith(path, wsOpts)
			if err != nil {
				if isWorkspaceNotFound(err) {
					return fmt.Errorf("%w\n  run `ofga model test init` to scaffold a starter workspace here", err)
				}
				return fmt.Errorf("load workspace: %w", err)
			}

			var serverOpts map[string]any
			if ws.Manifest != nil {
				serverOpts = ws.Manifest.Server
			}
			if remoteEngine && len(serverOpts) > 0 {
				output.Notef(cmd.ErrOrStderr(), "the manifest's server: options are ignored for non-embedded engines (--openfga-image/--server-addr)")
			}

			var diffBase *modeltest.LoadedModel
			if coverageDiff != "" {
				diffBase, err = loadDiffBaseModel(ws, coverageDiff)
				if err != nil {
					return clierr.WithCode(clierr.CodeError, fmt.Errorf("coverage diff base: %w", err))
				}
			}

			machineOut := c.cli.JSON || c.cli.YAML
			var eng modeltest.Engine
			switch {
			case openfgaImage != "":
				// A cold Docker pull can hang for minutes; note it so the wait
				// isn't silent. Kept off stdout and out of machine-output runs.
				if !machineOut {
					output.Progressf(cmd.ErrOrStderr(), "starting OpenFGA %s (pulling image if needed)…", openfgaImage)
				}
				eng, err = modeltest.NewContainerEngine(cmd.Context(), openfgaImage)
			case serverAddr != "":
				// The remote engine dials plaintext gRPC. That's fine for a local
				// or CI-network server, but warn if it looks remote so tuples and
				// results aren't sent in the clear unknowingly.
				if !isLocalAddr(serverAddr) {
					fmt.Fprintln(cmd.ErrOrStderr(), style.Warn.Render("warning: --server-addr uses plaintext gRPC (no TLS); avoid sending real data to a non-local server this way"))
				}
				if !machineOut {
					output.Progressf(cmd.ErrOrStderr(), "connecting to %s…", serverAddr)
				}
				eng, err = modeltest.NewRemoteEngine(serverAddr)
			default:
				eng, err = modeltest.NewEmbeddedEngine(serverOpts)
			}
			if err != nil {
				if openfgaImage != "" && isDockerUnreachable(err) {
					return clierr.WithCode(clierr.CodeUsage, errors.New("Docker isn't reachable — start Docker, or drop --openfga-image to use the embedded server"))
				}
				return fmt.Errorf("start engine: %w", err)
			}
			defer eng.Close()

			res, err := modeltest.Run(cmd.Context(), ws, modeltest.Options{
				Run:           run,
				Parallel:      parallel,
				Dedupe:        dedupe,
				Engine:        eng,
				Explain:       explain,
				Coverage:      coverage,
				Timeout:       timeout,
				FailFast:      failFast,
				DiffBaseModel: diffBase,
				DiffBaseName:  coverageDiff,
			})
			if err != nil {
				return fmt.Errorf("run tests: %w", err)
			}

			// junit/json reports printed to the terminal (no --report-file) own
			// stdout as the machine payload; keep the human summary/failures off
			// stdout so it isn't interleaved into the report. github annotations
			// stay inline. -o json/yaml already routes the human summary to stderr.
			reportToStdout := (report == "junit" || report == "json") && reportFile == "" && !machineOut
			if err := renderTestResults(cmd, c.cli, res, explain, coverageDetail, reportToStdout); err != nil {
				return err
			}

			// --slowest is a human aid; machine output stays the pure result.
			if slowest > 0 && !machineOut {
				slowestW := cmd.OutOrStdout()
				if reportToStdout {
					slowestW = cmd.ErrOrStderr()
				}
				renderSlowest(slowestW, res, slowest)
			}

			// A truncated trace means coverage is partial (under-reported), so warn
			// before any --coverage-min/--coverage-diff gate acts on it.
			if res.Coverage != nil && res.Coverage.Bounded {
				output.Warnf(cmd.ErrOrStderr(), "coverage may be under-reported — a model is deep/wide enough that the resolution trace was truncated; some branches weren't evaluated.")
			}
			// --fail-fast can stop before every test ran, so coverage is partial.
			if res.Coverage != nil && res.Summary.Incomplete {
				output.Warnf(cmd.ErrOrStderr(), "coverage is partial — --fail-fast stopped the run before every matched test ran.")
			}

			if report != "" {
				w := cmd.OutOrStdout()
				if reportFile != "" {
					f, err := createReportFile(reportFile)
					if err != nil {
						return err
					}
					defer f.Close()
					w = f
				}
				if err := modeltest.WriteReport(report, w, res); err != nil {
					return err
				}
			}

			if playground {
				if err := c.runPlayground(cmd, ws, res, dedupe, noTUI); err != nil {
					return err
				}
			}

			if res.Summary.Failed > 0 {
				// summaryLine already printed the authoritative "N/Total test(s)
				// failed" line; return a silent coded error so the run exits 3
				// without main re-printing a redundant second summary. This runs
				// before the coverage-nil usage guard below so a real test
				// failure keeps its CodeTestFailed (exit 3) and isn't
				// misclassified as a coverage usage error (exit 2) that CI keys
				// on differently.
				return clierr.Silent(clierr.CodeTestFailed)
			}

			// A coverage-build failure is no longer fatal to the run itself
			// (modeltest.Run returns the results with Coverage nil and the
			// reason in CoverageError), but --coverage on a workspace with no
			// single model to enumerate against is still a bad invocation from
			// the CLI's point of view: preserve the historical exit 2. Results,
			// any coverage that WAS produced, and any --report/--playground
			// output are already handled above, so this only sets the exit
			// code. This also stands in for the --coverage-min gate: with no
			// coverage available, min is moot and the same usage error applies
			// (the gate below guards nil Coverage).
			if coverage && res.Coverage == nil && res.CoverageError != "" {
				return clierr.WithCode(clierr.CodeUsage, errors.New(res.CoverageError))
			}
			if coverageMin > 0 && res.Coverage != nil && res.Coverage.Percent < coverageMin {
				return clierr.WithCode(clierr.CodeTestFailed, fmt.Errorf("coverage %.1f%% is below the required %.1f%%", res.Coverage.Percent, coverageMin))
			}
			if res.Coverage != nil && res.Coverage.Diff != nil && res.Coverage.Diff.Uncovered > 0 {
				return clierr.WithCode(clierr.CodeTestFailed, fmt.Errorf("%d rewrite branch(es) added since %s have no test coverage", res.Coverage.Diff.Uncovered, coverageDiff))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", ".", "path to the workspace manifest, directory, or a single test file (default: current directory, searched upward for ofga.yaml)")
	cmd.Flags().StringVar(&modelFlag, "model", "", "model file to test (overrides the manifest, or runs manifest-free with --tests)")
	cmd.Flags().StringArrayVar(&fixturesFlag, "fixtures", nil, "fixture-file glob(s) to register (repeatable; overrides the manifest's fixtures)")
	cmd.Flags().StringArrayVar(&testsFlag, "tests", nil, "test-file glob(s) to run (repeatable; overrides the manifest's tests, or runs manifest-free with --model)")
	cmd.Flags().StringVar(&run, "run", "", "glob to select tests by \"<file-stem>/<test-name>\" or by name alone")
	cmd.Flags().IntVar(&parallel, "parallel", 0, "max concurrent tests (0 = number of CPUs)")
	cmd.Flags().BoolVar(&failFast, "fail-fast", false, "stop after the first failing test instead of running the whole suite")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "per-test timeout bounding each test's engine work (e.g. 30s; 0 = no timeout)")
	cmd.Flags().IntVar(&slowest, "slowest", 0, "after the run, list the N slowest tests (0 = don't)")
	cmd.Flags().BoolVar(&dedupe, "dedupe-fixtures", false, "drop duplicate tuples when resolving fixtures")
	cmd.Flags().StringVar(&report, "report", "", "write a report in this format (\"junit\", \"json\", or \"github\"); to --report-file if set, otherwise printed to the terminal")
	cmd.Flags().StringVar(&reportFile, "report-file", "", "path to write the --report file to (requires --report)")
	cmd.Flags().StringVar(&explain, "explain", "auto", "explanation detail: \"auto\" (failures only) or \"full\" (every assertion)")
	cmd.Flags().BoolVar(&coverage, "coverage", false, "enable a branch coverage report against the model")
	cmd.Flags().BoolVar(&coverageDetail, "coverage-detail", false, "print full per-branch detail in the human coverage report; requires --coverage")
	cmd.Flags().Float64Var(&coverageMin, "coverage-min", 0, "fail (exit 3) if coverage percent is below this threshold; requires --coverage")
	cmd.Flags().StringVar(&coverageDiff, "coverage-diff", "", "compare the model against a git ref (e.g. main) and fail (exit 3) on rewrite branches added since that ref that no test covers (grant-based); implies --coverage")
	cmd.Flags().BoolVar(&watch, "watch", false, "re-run the tests whenever a workspace file changes (interactive; Ctrl-C to stop)")
	cmd.Flags().StringVar(&openfgaImage, "openfga-image", "", "run tests against a specific OpenFGA server version in Docker (e.g. openfga/openfga:v1.5.0) instead of the embedded server; requires Docker")
	cmd.Flags().StringVar(&serverAddr, "server-addr", "", "run tests against an already-running OpenFGA server at this gRPC host:port instead of the embedded server")
	cmd.Flags().BoolVar(&playground, "playground", false, "after the run, open the playground on a failing test's seeded world to explore and drill into every result (interactive TTY, human output only)")
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "with --playground, print a note instead of launching the playground TUI")

	// Shell completion: enum flags complete their choices; path-like flags and
	// the positional workspace path complete files/dirs.
	_ = cmd.RegisterFlagCompletionFunc("report", cobra.FixedCompletions([]string{"junit", "json", "github"}, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("explain", cobra.FixedCompletions([]string{"auto", "full"}, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.MarkFlagFilename("file")
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveDefault // complete the workspace path (files/dirs)
	}

	cmd.AddCommand(c.testInitCmd())
	cmd.AddCommand(c.testSchemaCmd())
	return cmd
}

// loadDiffBaseModel fetches the workspace's model as it was at git ref and
// loads it, to diff coverage against. If the model file didn't exist at that
// ref (a brand-new model), the base is treated as empty so every branch counts
// as added.
func loadDiffBaseModel(ws *modeltest.Workspace, ref string) (*modeltest.LoadedModel, error) {
	modelPath, err := ws.ModelPath()
	if err != nil {
		return nil, err
	}
	root, err := gitToplevel(filepath.Dir(modelPath))
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, modelPath)
	if err != nil {
		return nil, fmt.Errorf("locate model within repo: %w", err)
	}

	data, err := gitShow(root, ref, filepath.ToSlash(rel))
	if err != nil {
		if strings.Contains(err.Error(), "does not exist in") || strings.Contains(err.Error(), "exists on disk, but not in") {
			return modeltest.LoadModelBytes([]byte("model\n  schema 1.1\n"))
		}
		return nil, err
	}
	return modeltest.LoadModelBytes(data)
}

func gitToplevel(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("--coverage-diff needs a git repository (none found at %s)", dir)
	}
	return strings.TrimSpace(string(out)), nil
}

func gitShow(root, ref, relSlash string) ([]byte, error) {
	spec := ref + ":" + relSlash
	cmd := exec.Command("git", "-C", root, "show", spec)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git show %s: %s", spec, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// rejectInertConnectionFlags errors if the user explicitly set a connection or
// auth flag on `model test`. The command runs against a hermetic embedded
// server — no store, profile, or network — so these inherited global flags do
// nothing, and silently ignoring them is a footgun (a user pointing --api-url
// at a server and seeing tests "pass" against nothing). We check .Changed, so
// only a flag set on the command line trips this, not an env/config default.
func rejectInertConnectionFlags(cmd *cobra.Command) error {
	for _, name := range []string{
		"profile", "api-url", "store-id", "model-id",
		"auth-token-file", "auth-client-secret-file", "auth-private-key-file",
	} {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			return clierr.WithCode(clierr.CodeUsage, fmt.Errorf(
				"--%s has no effect on `model test`: it runs against a hermetic embedded server, not a real store, profile, or network", name))
		}
	}
	return nil
}

// createReportFile creates path's parent directories if needed and opens it
// for writing, truncating any existing content.
func createReportFile(path string) (*os.File, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create report dir: %w", err)
		}
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create report file: %w", err)
	}
	return f, nil
}

// runPlayground opens the playground on a failing test's seeded world (or the
// first test's, when all passed) after a normal `model test` run, listing every
// result in the Test Results section. It is a no-op — with a one-line stderr
// note so the ignore isn't silent — in machine-output (-o json/yaml), non-TTY,
// or --no-tui runs, so it never disturbs the scripted/CI contract. The
// interactive session is the terminal action; the caller still returns exit 3
// on failures once the TUI closes, keeping CI semantics for scripted callers.
func (c *Command) runPlayground(cmd *cobra.Command, ws *modeltest.Workspace, res *modeltest.Results, dedupe, noTUI bool) error {
	if len(res.Tests) == 0 {
		return nil
	}
	if noTUI || c.cli.JSON || c.cli.YAML || !term.IsTerminal(os.Stdout.Fd()) {
		output.Notef(cmd.ErrOrStderr(), "--playground needs an interactive terminal and human output; ignoring it")
		return nil
	}

	// Seed the first failing test's world (falling back to the first test) so the
	// live Query/Model sections back the most actionable failure; the Test Results
	// section still lists every test, and RunSeeded highlights the first failure.
	seedSel := res.Tests[0].Name
	for _, t := range res.Tests {
		if !t.Passed {
			seedSel = t.Name
			break
		}
	}

	endpoint, storeID, modelID, stop, err := modeltest.Seed(cmd.Context(), ws, seedSel, modeltest.Options{Dedupe: dedupe})
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	defer stop()

	cl, err := openfga.NewClient(endpoint, openfga.WithStoreID(storeID), openfga.WithAuthorizationModelID(modelID))
	if err != nil {
		return err
	}
	return playground.RunSeeded(cmd.Context(), c.cli, playground.SeedOptions{
		Client:    cl,
		StoreID:   storeID,
		ModelID:   modelID,
		Endpoint:  endpoint,
		Results:   res.Tests,
		Workspace: ws,
	})
}

// renderTestResults emits res per the output contract: JSON/YAML modes write
// only the machine-readable result to stdout, with the human summary on
// stderr; human/table/plain modes write the summary and any failure detail
// to stdout. When humanToStderr is set (a junit/json report is claiming stdout),
// the human summary/failures/coverage go to stderr so stdout carries only the
// report.
func renderTestResults(cmd *cobra.Command, a *cli.CLI, res *modeltest.Results, explain string, coverageDetail, humanToStderr bool) error {
	if a.JSON || a.YAML {
		if err := output.Emit(cmd.OutOrStdout(), a.YAML, res); err != nil {
			return err
		}
		summaryLine(cmd.ErrOrStderr(), res)
		return nil
	}

	w := cmd.OutOrStdout()
	if humanToStderr {
		w = cmd.ErrOrStderr()
	}
	summaryLine(w, res)
	if err := renderFailures(w, res, explain); err != nil {
		return err
	}
	if res.Coverage != nil {
		return renderCoverage(w, res.Coverage, coverageDetail)
	}
	return nil
}

// renderSlowest prints the n slowest tests (by wall-clock) after a run, so a
// suite that's getting slow shows where the time goes. Per-test durations are
// already measured; this just surfaces them.
func renderSlowest(w io.Writer, res *modeltest.Results, n int) {
	tests := append([]modeltest.TestResult(nil), res.Tests...)
	if len(tests) == 0 {
		return
	}
	sort.SliceStable(tests, func(i, j int) bool { return tests[i].DurationMs > tests[j].DurationMs })
	if n > len(tests) {
		n = len(tests)
	}
	fmt.Fprintf(w, "\n%s\n", style.Faint.Render(fmt.Sprintf("%d slowest test(s):", n)))
	for _, t := range tests[:n] {
		fmt.Fprintf(w, "  %s  %s\n", formatDuration(t.DurationMs), t.Name)
	}
}

// isLocalAddr reports whether a host:port gRPC target points at the local
// machine, where dialing plaintext gRPC is unremarkable.
func isLocalAddr(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	switch strings.ToLower(host) {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// isWorkspaceNotFound reports whether a LoadWorkspaceWith error is the
// "no ofga.yaml / workspace here" case, worth an actionable `test init` hint
// rather than a bare load error.
func isWorkspaceNotFound(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "ofga.yaml") || strings.Contains(msg, "no workspace") ||
		strings.Contains(msg, "workspace found")
}

// isDockerUnreachable reports whether a container-engine start error is the
// Docker-daemon-unreachable case, so it can be turned into an actionable
// usage error instead of a raw wrapped socket error.
func isDockerUnreachable(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"docker daemon", "cannot connect", "is the docker daemon running", "/var/run/docker.sock", "docker.sock"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func summaryLine(w io.Writer, res *modeltest.Results) {
	s := res.Summary
	dur := formatDuration(s.DurationMs)
	if s.Failed > 0 {
		// Built manually (rather than via output.Errorf) so the failed count
		// can be styled: Errorf sanitizes its formatted message for untrusted
		// error text, which strips the ANSI it would otherwise carry.
		dot := lipgloss.NewStyle().Foreground(style.Red).Render(style.IconDot)
		fmt.Fprintf(w, "%s %s/%d test(s) failed (%s)\n", dot, style.Failure.Render(fmt.Sprintf("%d", s.Failed)), s.Total, dur)
		return
	}
	output.Successf(w, "%s/%d test(s) passed (%s)", style.Success.Render(fmt.Sprintf("%d", s.Passed)), s.Total, dur)
}

// formatDuration renders a millisecond duration like go test's summary line
// (e.g. "0.04s").
func formatDuration(ms int64) string {
	return fmt.Sprintf("%.2fs", float64(ms)/1000)
}

// renderFailures prints, per test, the failed assertions (and, when
// explain == "full", every assertion) with their explanation.
func renderFailures(w io.Writer, res *modeltest.Results, explain string) error {
	full := explain == "full"
	// writeHeader prints the test name and, when present, its `description:` —
	// authored docs that would otherwise never be shown.
	writeHeader := func(t modeltest.TestResult) error {
		if _, err := fmt.Fprintf(w, "\n%s\n", style.Heading.Render(t.Name)); err != nil {
			return err
		}
		if t.Description != "" {
			if _, err := fmt.Fprintf(w, "  %s\n", style.Faint.Render(t.Description)); err != nil {
				return err
			}
		}
		return nil
	}
	for _, t := range res.Tests {
		if t.Passed && !full {
			continue
		}
		printedHeader := false
		// A test that could not execute at all carries no assertions, only an
		// Error — surface it under the same header so it is never silent.
		if t.Error != "" {
			if err := writeHeader(t); err != nil {
				return err
			}
			printedHeader = true
			if _, err := fmt.Fprintf(w, "  %s %s\n", style.Failure.Render(style.IconCross), style.Failure.Render("error: "+t.Error)); err != nil {
				return err
			}
		}
		for _, a := range t.Assertions {
			if a.Passed && !full {
				continue
			}
			if !printedHeader {
				if err := writeHeader(t); err != nil {
					return err
				}
				printedHeader = true
			}
			marker := style.Failure.Render(style.IconCross)
			if a.Passed {
				marker = style.Success.Render(style.IconCheck)
			}
			if _, err := fmt.Fprintf(w, "  %s %s\n", marker, a.Subject); err != nil {
				return err
			}
			if a.Explain != nil {
				modeltest.RenderExplain(w, a)
				continue
			}
			if _, err := fmt.Fprintf(w, "  expected %s, got %s\n", style.Success.Render(fmt.Sprintf("%v", a.Expected)), style.Failure.Render(fmt.Sprintf("%v", a.Got))); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderCoverage prints the coverage report for a human-mode run: a per-type
// table (plus a total row), a line for every relation showing covered/total
// (with a MISSED detail for any with misses), any unreachable branches
// (reported separately — they never count against the score), and a short
// disclosure of what "covered" means so the percentage is never a silent
// black box. With detail set, output also lists every branch under each
// relation.
func renderCoverage(w io.Writer, cov *modeltest.Coverage, detail bool) error {
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if output.Plain {
		if _, err := fmt.Fprintln(w, "coverage:"); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(w, style.Heading.Render("coverage:")); err != nil {
		return err
	}

	headers := []string{"TYPE", "COVERED", "TOTAL", "PERCENT"}
	rows := make([][]string, 0, len(cov.Types)+1)
	for _, tc := range cov.Types {
		pct := modeltest.Percent(tc.Covered, tc.Total)
		rows = append(rows, []string{tc.Type, fmt.Sprintf("%d", tc.Covered), fmt.Sprintf("%d", tc.Total), style.PercentColor(pct).Render(modeltest.FormatPercent(pct))})
	}
	rows = append(rows, []string{
		style.Bold.Render("total"),
		style.Bold.Render(fmt.Sprintf("%d", cov.Covered)),
		style.Bold.Render(fmt.Sprintf("%d", cov.Total)),
		style.Bold.Render(modeltest.FormatPercent(cov.Percent)),
	})
	if err := output.Table(w, headers, rows); err != nil {
		return err
	}

	if err := output.HumanBlankLine(w); err != nil {
		return err
	}

	for _, tc := range cov.Types {
		for _, rc := range tc.Relations {
			fractionStyle := style.Success
			if rc.Covered < rc.Total {
				fractionStyle = style.Failure
			}
			line := fmt.Sprintf("  %s.%s  %s", tc.Type, rc.Relation, fractionStyle.Render(fmt.Sprintf("%d/%d", rc.Covered, rc.Total)))
			if len(rc.Missed) > 0 {
				line += "   " + style.Warn.Render("MISSED: "+strings.Join(rc.Missed, ", "))
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
			if detail {
				if err := renderCoverageDetail(w, rc); err != nil {
					return err
				}
			}
		}
	}

	if len(cov.Unreachable) > 0 {
		if _, err := fmt.Fprintln(w, style.Faint.Render(fmt.Sprintf("unreachable: %s", strings.Join(cov.Unreachable, ", ")))); err != nil {
			return err
		}
	}

	disclosure := "coverage is grant-based (a rewrite branch counts covered only when a check assertion showed that specific arm granting; each ABAC condition counts its true and false outcomes separately; list_objects/list_users credit at relation granularity) over the manifest model."
	if _, err := fmt.Fprintln(w, style.Faint.Render(disclosure)); err != nil {
		return err
	}

	if cov.Diff != nil {
		return renderCoverageDiff(w, cov.Diff)
	}
	return nil
}

// renderCoverageDiff prints the branches a change added versus a base ref and
// whether each is covered — the actionable "did your PR add an untested branch"
// section.
func renderCoverageDiff(w io.Writer, d *modeltest.CoverageDiff) error {
	if _, err := fmt.Fprintf(w, "\ncoverage diff vs %s:\n", d.Base); err != nil {
		return err
	}
	if len(d.Added) == 0 {
		_, err := fmt.Fprintln(w, style.Faint.Render("  no rewrite branches added"))
		return err
	}
	for _, b := range d.Added {
		mark := style.Success.Render(style.IconCheck)
		if !b.Covered {
			mark = style.Warn.Render(style.IconCircle)
		}
		if _, err := fmt.Fprintf(w, "  %s %s.%s  %s\n", mark, b.Type, b.Relation, b.Label); err != nil {
			return err
		}
	}
	summary := fmt.Sprintf("  %d new branch(es), %d uncovered", len(d.Added), d.Uncovered)
	summaryStyle := style.Success
	if d.Uncovered > 0 {
		summaryStyle = style.Failure
	}
	_, err := fmt.Fprintln(w, summaryStyle.Render(summary))
	return err
}

// renderCoverageDetail prints, for a single relation, one line per branch
// with a covered marker and its kind.
func renderCoverageDetail(w io.Writer, rc modeltest.RelCov) error {
	for _, b := range rc.Branches {
		mark := style.Faint.Render(style.IconCircle)
		if b.Covered {
			mark = style.Success.Render(style.IconCheck)
		}
		if _, err := fmt.Fprintf(w, "    %s %s\n", mark, b.Label); err != nil {
			return err
		}
	}
	return nil
}
