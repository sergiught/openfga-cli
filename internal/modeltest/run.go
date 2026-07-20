package modeltest

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// Options controls Run.
type Options struct {
	Run      string
	Parallel int
	Explain  string
	Coverage bool
	Dedupe   bool
	Engine   Engine
	// Timeout bounds the engine work of a single test (0 = no timeout). It guards
	// against a pathological/cyclic model or a hung server wedging the run.
	Timeout time.Duration
	// FailFast stops the run after the first failing test rather than executing
	// every matched test.
	FailFast bool
	// DiffBaseModel, when set, enables coverage diffing: branches present in the
	// workspace model but not in this base model are reported as "added", with
	// their coverage status. DiffBaseName labels it in output (e.g. a git ref).
	DiffBaseModel *LoadedModel
	DiffBaseName  string
}

// runTask is a single matched (file, test) pair to execute.
type runTask struct {
	tf   *TestFile
	test Test
	name string // "<file-stem>/<test-name>"
}

// modelCache compiles each distinct model file at most once per run. Compiling
// a model (DSL→JSON transform in loadModel) is the expensive part of per-test
// setup; without this cache a workspace of N tests sharing one model would
// compile it N times. Access is mutex-guarded because tests run concurrently.
type modelCache struct {
	mu    sync.Mutex
	byKey map[string]*LoadedModel
	errs  map[string]error
}

func newModelCache() *modelCache {
	return &modelCache{byKey: map[string]*LoadedModel{}, errs: map[string]error{}}
}

// load returns the compiled model at path, compiling (and memoizing) it on the
// first request. A prior failure is memoized too, so a bad model isn't
// re-compiled once per test. The returned *LoadedModel is read-only and safe to
// share across the concurrent tests that reference the same model.
func (c *modelCache) load(path string) (*LoadedModel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lm, ok := c.byKey[path]; ok {
		return lm, nil
	}
	if err, ok := c.errs[path]; ok {
		return nil, err
	}
	lm, err := loadModel(path)
	if err != nil {
		c.errs[path] = err
		return nil, err
	}
	c.byKey[path] = lm
	return lm, nil
}

// Run executes every test in ws that matches opts.Run against a fresh store
// per test, using opts.Engine. It does not create or close the engine.
func Run(ctx context.Context, ws *Workspace, opts Options) (*Results, error) {
	start := time.Now()

	tasks, matchedFiles := matchTasks(ws, opts.Run)
	if len(tasks) == 0 {
		// A --run pattern that selects nothing is a bad invocation, not a runtime
		// failure: exit CodeUsage (2), consistent with other "2 = bad invocation"
		// cases, so scripts can tell a mistyped filter from a real error. When
		// there's no --run at all, the miss isn't a filter problem but an empty
		// workspace/file, so the message says that instead of the nonsensical
		// `no tests matched ""`.
		return nil, clierr.WithCode(clierr.CodeUsage, noMatchError(ws, opts.Run))
	}

	parallel := opts.Parallel
	if parallel <= 0 {
		parallel = runtime.NumCPU()
	}

	results := make([]TestResult, len(tasks))
	errs := make([]error, len(tasks))

	// Compile each distinct model once, and parse each fixture file once, rather
	// than repeating both per test.
	models := newModelCache()
	fixtures := newFixtureCache()

	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	var sawFailure atomic.Bool
	dispatched := 0
	for i, tk := range tasks {
		// Stop dispatching new tests once the run is cancelled/timed-out rather
		// than blocking on a full semaphore while in-flight tests drain.
		if ctx.Err() != nil {
			break
		}
		// --fail-fast stops after the first failure. Already in-flight tests are
		// allowed to finish (cancelling them would corrupt their results); we
		// just stop launching new ones.
		if opts.FailFast && sawFailure.Load() {
			break
		}
		sem <- struct{}{}
		// Re-check after acquiring the slot: with low --parallel the in-flight
		// test can fail (or the context cancel) while we waited for the slot, so
		// the checks above may have been stale.
		if ctx.Err() != nil || (opts.FailFast && sawFailure.Load()) {
			<-sem
			break
		}
		dispatched = i + 1
		wg.Add(1)
		go func(i int, tk runTask) {
			defer wg.Done()
			defer func() { <-sem }()
			// A panic parsing user-authored YAML/DSL/models must fail only its own
			// test, not crash the whole runner (and every other test's result).
			defer func() {
				if r := recover(); r != nil {
					errs[i] = fmt.Errorf("test %s: panic: %v", tk.name, r)
				}
			}()
			results[i], errs[i] = runTest(ctx, ws, tk, opts, models, fixtures)
			if errs[i] != nil || !results[i].Passed {
				sawFailure.Store(true)
			}
		}(i, tk)
	}
	wg.Wait()

	// A cancelled/timed-out context aborts the whole run: in-flight results are
	// unreliable and dispatch may have stopped early, leaving tasks unrun.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// --fail-fast (or a full semaphore hit at cancel time) can leave trailing
	// tasks never dispatched; drop their zero-value slots so they aren't counted
	// as failures. In a normal full run dispatched == len(tasks), so this is a
	// no-op.
	incomplete := dispatched < len(tasks) // fail-fast stopped before every matched test ran
	results = results[:dispatched]
	errs = errs[:dispatched]
	tasks = tasks[:dispatched]

	for i, err := range errs {
		if err == nil {
			continue
		}
		// Isolate the failure to its own test — like go test / pytest, one test
		// that can't execute (bad relation, fixture, setup) fails itself without
		// discarding every other test's result. The task name is already on the
		// result via runTest's wrapper; strip that redundant prefix so the stored
		// message doesn't repeat the name the result already carries.
		msg := strings.TrimPrefix(err.Error(), "test "+tasks[i].name+": ")
		results[i] = TestResult{Name: tasks[i].name, Passed: false, Error: msg}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })

	summary := Summary{Total: len(results), MatchedFiles: matchedFiles, MatchedTests: len(results), Incomplete: incomplete}
	for _, r := range results {
		if r.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
	}
	res := &Results{SchemaVersion: resultsSchemaVersion, Summary: summary, Tests: results}
	if opts.Coverage {
		cov, err := buildCoverage(ws, tasks, results, opts, models)
		if err != nil {
			// A coverage-aggregation failure (e.g. a multi-model workspace has
			// no single model to enumerate against) is NOT a run failure: the
			// tests all ran. Record the reason and return the results with
			// Coverage left nil, so callers can decide what a missing coverage
			// report means for them (the CLI still exits CodeUsage; the TUI
			// surfaces it as a hint) rather than losing an otherwise-green run.
			res.CoverageError = err.Error()
		} else {
			res.Coverage = cov
		}
	}

	// Wall-clock of the whole run, not the sum of per-test durations: tests
	// run concurrently (see the sem-gated goroutines above), so summing would
	// overstate the time an operator or CI dashboard actually waited. Stamped
	// last (after coverage aggregation) so it covers the whole run, not just
	// the test-execution phase.
	res.Summary.DurationMs = time.Since(start).Milliseconds()

	return res, nil
}

// ModelPath returns the single model file the workspace's tests resolve to,
// erroring when test files override to more than one model. The CLI uses it to
// fetch the base model for a coverage diff.
func (ws *Workspace) ModelPath() (string, error) {
	seen := map[string]bool{}
	var paths []string
	for _, tf := range ws.TestFiles {
		p, err := resolveModelPath(ws, tf)
		if err != nil {
			return "", err
		}
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	switch len(paths) {
	case 0:
		return "", fmt.Errorf("no model found in workspace")
	case 1:
		return paths[0], nil
	default:
		return "", fmt.Errorf("workspace uses %d models (test files override model:); coverage diffing needs a single model", len(paths))
	}
}

// noMatchError builds the error for a zero-match run. With an explicit
// --run pattern, it names the pattern that excluded everything. With no
// pattern, the miss means the workspace/file simply has no tests to run, so
// it reports that instead — naming the single restricted file when one was
// loaded directly (e.g. via --file), or the manifest's tests: glob patterns
// otherwise.
func noMatchError(ws *Workspace, pattern string) error {
	if pattern != "" {
		return fmt.Errorf("no tests matched --run %q", pattern)
	}

	if len(ws.TestFiles) == 1 {
		return fmt.Errorf("%s has no tests", ws.TestFiles[0].Path)
	}

	if ws.Manifest != nil {
		return fmt.Errorf("no test files found in workspace (manifest patterns: %s)", strings.Join(ws.Manifest.Tests, ", "))
	}

	return fmt.Errorf("no test files found in workspace")
}

// matchTasks collects every test across ws.TestFiles whose "<stem>/<name>"
// matches pattern, along with the count of distinct files with at least one
// match.
func matchTasks(ws *Workspace, pattern string) ([]runTask, int) {
	var tasks []runTask
	matchedFiles := 0

	for _, tf := range ws.TestFiles {
		stem := FileStem(tf.Path)
		fileMatched := false
		for _, tt := range tf.Tests {
			if matchRun(pattern, stem, tt.Name) {
				tasks = append(tasks, runTask{tf: tf, test: tt, name: stem + "/" + tt.Name})
				fileMatched = true
			}
		}
		if fileMatched {
			matchedFiles++
		}
	}

	return tasks, matchedFiles
}

// FileStem strips the .test.yaml/.test.yml suffix from a test file's base
// name.
func FileStem(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".test.yaml")
	base = strings.TrimSuffix(base, ".test.yml")
	return base
}

// matchRun reports whether a test identified by fileStem/testName is
// selected by pattern. An empty pattern matches everything. A pattern
// containing no "/" also matches against testName alone, so `--run
// owner-is-viewer` can select a test by name regardless of which file it
// lives in.
func matchRun(pattern, fileStem, testName string) bool {
	if pattern == "" {
		return true
	}

	if ok, _ := doublestar.Match(pattern, fileStem+"/"+testName); ok {
		return true
	}

	if !strings.Contains(pattern, "/") {
		if ok, _ := doublestar.Match(pattern, testName); ok {
			return true
		}
	}

	return false
}

// runTest loads the model, resolves fixtures, sets up a fresh store, and
// evaluates every assertion for a single test.
func runTest(ctx context.Context, ws *Workspace, tk runTask, opts Options, models *modelCache, fixtures *fixtureCache) (TestResult, error) {
	start := time.Now()

	// Bound a single test's engine work so a pathological/cyclic model or a hung
	// server can't wedge the whole run indefinitely (0 = no timeout).
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	modelPath, err := resolveModelPath(ws, tk.tf)
	if err != nil {
		return TestResult{}, fmt.Errorf("test %s: %w", tk.name, err)
	}

	lm, err := models.load(modelPath)
	if err != nil {
		return TestResult{}, fmt.Errorf("test %s: %w", tk.name, err)
	}

	tuples, err := resolveFixtures(ws, tk.tf, tk.test, opts.Dedupe, fixtures)
	if err != nil {
		return TestResult{}, fmt.Errorf("test %s: %w", tk.name, err)
	}

	protoTuples, err := toProtoTuples(tuples)
	if err != nil {
		return TestResult{}, fmt.Errorf("test %s: %w", tk.name, err)
	}

	storeID, modelID, err := opts.Engine.Setup(ctx, lm.Proto, protoTuples)
	if err != nil {
		return TestResult{}, fmt.Errorf("test %s: setup: %w", tk.name, err)
	}
	// Free this test's store once it finishes so a large suite's memory doesn't
	// grow with the sum of every test's tuples. Best-effort on a fresh, bounded
	// context (the test's own ctx may be timed out/cancelled), and cleanup errors
	// never fail an otherwise-passing test.
	defer func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = opts.Engine.DeleteStore(dctx, storeID)
	}()
	scope := Scope{StoreID: storeID, ModelID: modelID}

	assertions, err := evaluateAssertions(ctx, lm, opts, scope, tk.test)
	if err != nil {
		return TestResult{}, fmt.Errorf("test %s: %w", tk.name, err)
	}

	passed := true
	for _, a := range assertions {
		if !a.Passed {
			passed = false
			break
		}
	}

	return TestResult{Name: tk.name, Description: tk.test.Description, Passed: passed, Assertions: assertions, DurationMs: time.Since(start).Milliseconds()}, nil
}

// resolveModelPath chooses the model path for a test file: the test file's
// own `model`, resolved relative to the test file's directory, or else the
// manifest's `model`, resolved relative to the manifest's directory.
func resolveModelPath(ws *Workspace, tf *TestFile) (string, error) {
	if tf.Model != "" {
		return filepath.Join(filepath.Dir(tf.Path), tf.Model), nil
	}

	if ws.Manifest != nil && ws.Manifest.Model != "" {
		return filepath.Join(filepath.Dir(ws.Manifest.path), ws.Manifest.Model), nil
	}

	return "", fmt.Errorf("no model specified for %s (set 'model' in the test file or manifest)", tf.Path)
}
