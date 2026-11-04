package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/charmbracelet/x/ansi"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
	"github.com/sergiught/openfga-cli/internal/optimize/bench"
	"github.com/sergiught/openfga-cli/internal/output"
)

const optimizeReference = "../../optimize/testdata/reference.fga"

// runOptimize executes `model optimize` and returns stdout, stderr and the error.
func runOptimize(t *testing.T, stdin string, asJSON bool, args ...string) (string, string, error) {
	t.Helper()
	configtest.Isolate(t)
	a := cli.New(log.New(io.Discard), config.New(), "test")
	a.JSON = asJSON
	cmd := New(a).optimizeCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func copyReference(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(optimizeReference)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "model.fga")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func wantUsage(t *testing.T, err error) {
	t.Helper()
	if err == nil || clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("want usage error, got %v", err)
	}
}

func TestOptimize_ReferenceHumanOutput(t *testing.T) {
	out, _, err := runOptimize(t, "", false, optimizeReference)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Safe rewrites (4)",
		"folder#can_delete",
		"reason:",
		"proof:",
		"-    define can_delete: owner and editor",
		"+    define can_delete: owner",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "Safe rewrites") > strings.Index(out, "Diff") {
		t.Errorf("findings must precede the diff:\n%s", out)
	}
}

func TestOptimize_JSONHasFourApplied(t *testing.T) {
	out, _, err := runOptimize(t, "", true, optimizeReference)
	if err != nil {
		t.Fatal(err)
	}
	var got optimizeOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.SchemaVersion != 1 || got.Model.Format != "dsl" || got.Rewrite == nil || len(got.Rewrite.Applied) != 4 {
		t.Fatalf("unexpected output: %+v", got)
	}
	if got.Bench != nil {
		t.Errorf("bench must be nil until requested")
	}
}

func TestOptimize_JSONAppliedMatchesFindingKeys(t *testing.T) {
	out, _, err := runOptimize(t, "", true, optimizeReference)
	if err != nil {
		t.Fatal(err)
	}
	var got optimizeOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.Rewrite == nil || len(got.Rewrite.Applied) != 4 {
		t.Fatalf("unexpected rewrite: %+v", got.Rewrite)
	}
	for i, k := range got.Rewrite.Applied {
		if k != got.Findings[i].Key {
			t.Errorf("applied[%d] = %q, want findings[%d].key %q", i, k, i, got.Findings[i].Key)
		}
	}
	if k := got.Findings[0].Key; !strings.Contains(k, "#") || !strings.Contains(k, ":S") {
		t.Errorf("findings[0].key = %q, want it to contain # and :S", k)
	}
}

func TestOptimize_WriteRewritesFile(t *testing.T) {
	p := copyReference(t)
	_, stderr, err := runOptimize(t, "", false, p, "--write")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "rewrite reformats the model") {
		t.Errorf("missing reformat notice: %q", stderr)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lm, err := modeltest.LoadModelBytes(data)
	if err != nil {
		t.Fatalf("rewritten file does not parse: %v", err)
	}
	rep, err := optimize.Analyze(lm.Proto)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rewrite != nil {
		t.Errorf("rewritten file still has %d safe rewrites", len(rep.Rewrite.Applied))
	}
}

func TestOptimize_WriteOnStdinIsUsage(t *testing.T) {
	_, _, err := runOptimize(t, "model\n  schema 1.1\n\ntype user\n", false, "-", "--write")
	wantUsage(t, err)
	if h := clierr.NewEnvelope(err).Error.Hint; !strings.Contains(h, "--output-file") {
		t.Errorf("hint = %q", h)
	}
}

func TestOptimize_WriteOnJSONInputIsUsage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "model.json")
	js := `{"schema_version":"1.1","type_definitions":[{"type":"user"}]}`
	if err := os.WriteFile(p, []byte(js), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := runOptimize(t, "", false, p, "--write")
	wantUsage(t, err)
	if h := clierr.NewEnvelope(err).Error.Hint; !strings.Contains(h, "--output-file") {
		t.Errorf("hint = %q", h)
	}
}

func TestOptimize_OutputFileWritesWithoutWriteFlag(t *testing.T) {
	src := copyReference(t)
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out.fga")
	if _, _, err := runOptimize(t, "", false, src, "--output-file", dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "define can_delete: owner\n") {
		t.Errorf("output file not rewritten:\n%s", got)
	}
	after, _ := os.ReadFile(src)
	if !bytes.Equal(before, after) {
		t.Error("source file must be untouched without --write")
	}
}

func TestOptimize_TierFiltersReportOnly(t *testing.T) {
	p := copyReference(t)
	out, _, err := runOptimize(t, "", false, p, "--tier", "tuple", "--write")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Safe rewrites") {
		t.Errorf("safe findings printed despite --tier tuple:\n%s", out)
	}
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), "define can_delete: owner and editor") {
		t.Errorf("--write must still apply the safe rewrite:\n%s", data)
	}
	jsonOut, _, err := runOptimize(t, "", true, optimizeReference, "--tier", "tuple")
	if err != nil {
		t.Fatal(err)
	}
	var got optimizeOutput
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatal(err)
	}
	for _, f := range got.Findings {
		if f.Tier != optimize.TierTuple {
			t.Errorf("finding %s has tier %s", f.ID, f.Tier)
		}
	}
	if got.Rewrite == nil || len(got.Rewrite.Applied) != 4 {
		t.Errorf("rewrite must be reported regardless of --tier: %+v", got.Rewrite)
	}
}

func TestOptimize_UnknownTierIsUsage(t *testing.T) {
	_, _, err := runOptimize(t, "", false, optimizeReference, "--tier", "bogus")
	wantUsage(t, err)
}

func TestOptimize_NothingToDoExitsZero(t *testing.T) {
	const dsl = "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n"
	out, stderr, err := runOptimize(t, dsl, false, "-")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "no safe rewrites; 0 recommendations") {
		t.Errorf("stderr = %q", stderr)
	}
	if strings.Contains(out, "Diff") {
		t.Errorf("unexpected diff:\n%s", out)
	}
	jsonOut, _, err := runOptimize(t, dsl, true, "-")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &m); err != nil {
		t.Fatal(err)
	}
	if m["rewrite"] != nil || !reflect.DeepEqual(m["findings"], []any{}) {
		t.Errorf("rewrite/findings = %v / %v", m["rewrite"], m["findings"])
	}
	if v, ok := m["safeRewritesBlocked"]; ok {
		t.Errorf("safeRewritesBlocked = %v, want it omitted", v)
	}
}

// recursiveExclusionModel has a recursive exclusion (doc#can_read) and an AND
// on a tuple cycle in an unrelated type; the gate must block every rewrite.
const recursiveExclusionModel = `model
  schema 1.1

type user

type folder
  relations
    define parent: [folder]
    define viewer: [user] or viewer from parent

type doc
  relations
    define parent: [folder]
    define can_read: [user] but not viewer from parent

type team
  relations
    define parent: [team]
    define member: [user] or both from parent
    define wide: [user] or member
    define both: member and wide
`

func TestOptimize_RecursiveExclusionBlocksRewrites(t *testing.T) {
	const reason = "doc#can_read subtracts a recursive relation"
	out, stderr, err := runOptimize(t, recursiveExclusionModel, false, "-")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "safe rewrites disabled: "+reason) {
		t.Errorf("stderr = %q, want the blocked notice", stderr)
	}
	if strings.Contains(out, "Diff") {
		t.Errorf("unexpected diff:\n%s", out)
	}
	jsonOut, _, err := runOptimize(t, recursiveExclusionModel, true, "-")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &m); err != nil {
		t.Fatal(err)
	}
	if got, _ := m["safeRewritesBlocked"].(string); !strings.HasPrefix(got, reason) {
		t.Errorf("safeRewritesBlocked = %q, want it to start with %q", got, reason)
	}
	if m["rewrite"] != nil {
		t.Errorf("rewrite = %v, want null", m["rewrite"])
	}
}

func TestOptimize_InvalidModelIsUsage(t *testing.T) {
	_, _, err := runOptimize(t, "this is not a model", false, "-")
	wantUsage(t, err)
	if err != nil && errors.Unwrap(err) == nil {
		t.Errorf("error should wrap its cause: %v", err)
	}
}

func TestOptimize_WriteFollowsSymlink(t *testing.T) {
	target := copyReference(t)
	link := filepath.Join(t.TempDir(), "link.fga")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := runOptimize(t, "", false, link, "--write"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was replaced: %v %v", st, err)
	}
	data, _ := os.ReadFile(target)
	if strings.Contains(string(data), "define can_delete: owner and editor") {
		t.Errorf("target not rewritten:\n%s", data)
	}
}

func TestOptimize_WriteAndOutputFileExclusive(t *testing.T) {
	p := copyReference(t)
	_, _, err := runOptimize(t, "", false, p, "--write", "--output-file", filepath.Join(t.TempDir(), "o.fga"))
	wantUsage(t, err)
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %v", err)
	}
}

func TestOptimize_JSONWriteRefusalLeavesFileUntouched(t *testing.T) {
	p := filepath.Join(t.TempDir(), "model.json")
	js := []byte(`{"schema_version":"1.1","type_definitions":[{"type":"user"}]}`)
	if err := os.WriteFile(p, js, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runOptimize(t, "", false, p, "--write"); err == nil {
		t.Fatal("want error")
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, js) {
		t.Errorf("file changed: %s", got)
	}
}

func TestOptimize_WriteWithNoRewriteLeavesFileUntouched(t *testing.T) {
	p := filepath.Join(t.TempDir(), "model.fga")
	dsl := []byte("model\n  schema 1.1\n\n# keep me\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n")
	if err := os.WriteFile(p, dsl, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runOptimize(t, "", false, p, "--write"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, dsl) {
		t.Errorf("file changed: %s", got)
	}
}

func TestOptimize_WritePreservesMode(t *testing.T) {
	p := copyReference(t)
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runOptimize(t, "", false, p, "--write"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, %v", st.Mode().Perm(), err)
	}
}

func benchJSON(t *testing.T, args ...string) optimizeOutput {
	t.Helper()
	out, _, err := runOptimize(t, "", true, args...)
	if err != nil {
		t.Fatal(err)
	}
	var got optimizeOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.Bench == nil {
		t.Fatalf("bench missing:\n%s", out)
	}
	return got
}

func copyWorkspace(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := "../../modeltest/testdata/access-control"
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o750)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestOptimize_BenchSynthesisTable(t *testing.T) {
	out, _, err := runOptimize(t, "", false, optimizeReference, "--bench", "--iterations", "3")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Benchmark · synthesis", "Deny reads", "Answers", "in-process, indicative only"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestOptimize_BenchUsesWorkspaceWhenPresent(t *testing.T) {
	dir := copyWorkspace(t)
	got := benchJSON(t, filepath.Join(dir, "model.fga"), "--bench", "--iterations", "2")
	if got.Bench.Source != "workspace" {
		t.Errorf("source = %q", got.Bench.Source)
	}
	got = benchJSON(t, filepath.Join(dir, "model.fga"), "--bench", "--iterations", "2", "--workspace", dir)
	if got.Bench.Source != "workspace" {
		t.Errorf("explicit source = %q", got.Bench.Source)
	}
}

func TestOptimize_SynthOverridesWorkspace(t *testing.T) {
	dir := copyWorkspace(t)
	got := benchJSON(t, filepath.Join(dir, "model.fga"), "--bench", "--synth", "--iterations", "2")
	if got.Bench.Source != "synthesis" {
		t.Errorf("source = %q", got.Bench.Source)
	}
}

func TestOptimize_BenchProfilesWhenNothingToRewrite(t *testing.T) {
	const dsl = "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n"
	out, _, err := runOptimize(t, dsl, true, "-", "--bench", "--iterations", "2")
	if err != nil {
		t.Fatal(err)
	}
	var got optimizeOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Rewrite != nil || got.Bench == nil || len(got.Bench.Checks) == 0 {
		t.Fatalf("unexpected: %+v", got)
	}
	for _, c := range got.Bench.Checks {
		if c.After != nil {
			t.Errorf("%s: after must be nil", c.Name)
		}
	}
}

func TestOptimize_BenchMismatchExits3AndDoesNotWrite(t *testing.T) {
	orig := benchRunFn
	t.Cleanup(func() { benchRunFn = orig })
	benchRunFn = func(ctx context.Context, b, a *openfgav1.AuthorizationModel, src *bench.Source, o bench.Options) (*bench.Result, error) {
		return &bench.Result{Source: "synthesis", Checks: []bench.CheckResult{}, Mismatches: []bench.Mismatch{{
			Check: bench.Check{Name: "x"}, AllowedBefore: true,
		}}}, nil
	}
	p := copyReference(t)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range [][]string{{"--write"}, {"--output-file", filepath.Join(t.TempDir(), "o.fga")}} {
		out, _, err := runOptimize(t, "", true, append([]string{p, "--bench"}, mode...)...)
		if clierr.Code(err) != clierr.CodeTestFailed {
			t.Fatalf("code = %d, err = %v", clierr.Code(err), err)
		}
		if !strings.Contains(err.Error(), "benchmark found 1 check whose answer changed; rewrite was NOT written") {
			t.Errorf("message = %v", err)
		}
		var got optimizeOutput
		if jerr := json.Unmarshal([]byte(out), &got); jerr != nil || got.Bench == nil || len(got.Bench.Mismatches) != 1 {
			t.Fatalf("json bench not emitted (%v):\n%s", jerr, out)
		}
		after, _ := os.ReadFile(p)
		if !bytes.Equal(before, after) {
			t.Error("model file changed")
		}
		if len(mode) == 2 {
			if _, err := os.Stat(mode[1]); err == nil {
				t.Error("output file written")
			}
		}
	}
}

func TestOptimize_SeedIsReproducible(t *testing.T) {
	sig := func(seed string) string {
		got := benchJSON(t, optimizeReference, "--bench", "--iterations", "1", "--seed", seed)
		b, err := json.Marshal(struct {
			T []bench.Tuple
			C []bench.Check
		}{got.Bench.Tuples, checksOf(got.Bench)})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	a, b := sig("7"), sig("7")
	if a != b {
		t.Error("same seed produced different tuples/checks")
	}
	if a == sig("8") {
		t.Error("different seeds produced identical tuples")
	}
}

func checksOf(r *bench.Result) []bench.Check {
	var cs []bench.Check
	for _, c := range r.Checks {
		cs = append(cs, c.Check)
	}
	return cs
}

func TestOptimize_BenchFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--bench", "--depth", "0"},
		{"--bench", "--iterations", "0"},
		{"--bench", "--synth", "--workspace", "."},
		{"--seed", "3"},
		{"--synth"},
	} {
		_, _, err := runOptimize(t, "", false, append([]string{optimizeReference}, args...)...)
		wantUsage(t, err)
	}
}

func TestOptimize_WorkspaceMismatchedModelIsUsageWithSynthHint(t *testing.T) {
	dir := copyWorkspace(t)
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(sub, "other.fga")
	const dsl = "model\n  schema 1.1\n\ntype user\n\ntype widget\n  relations\n    define owner: [user]\n"
	if err := os.WriteFile(other, []byte(dsl), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := runOptimize(t, "", false, other, "--bench", "--iterations", "1")
	if clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("code = %d, err = %v", clierr.Code(err), err)
	}
	if h := clierr.NewEnvelope(err).Error.Hint; !strings.Contains(h, "--synth") {
		t.Errorf("hint = %q", h)
	}
	if !strings.Contains(stderr, "workspace model is") || !strings.Contains(stderr, other) {
		t.Errorf("missing notice naming both paths: %q", stderr)
	}
}

func TestOptimize_UnstableAnswersAreNoticedNotFailed(t *testing.T) {
	orig := benchRunFn
	t.Cleanup(func() { benchRunFn = orig })
	benchRunFn = func(ctx context.Context, b, a *openfgav1.AuthorizationModel, src *bench.Source, o bench.Options) (*bench.Result, error) {
		return &bench.Result{Source: "synthesis", Checks: []bench.CheckResult{}, Mismatches: []bench.Mismatch{}, Unstable: []bench.Mismatch{{
			Check: bench.Check{Name: "probe u", User: "user:a", Relation: "viewer", Object: "document:t1"}, AllowedAfter: true,
		}}}, nil
	}
	out, _, err := runOptimize(t, "", false, optimizeReference, "--bench")
	if err != nil {
		t.Fatalf("unstable answers must not fail the run: %v", err)
	}
	for _, want := range []string{"Unstable answers (1)", "probe u: user:a viewer document:t1  deny"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Changed answers") {
		t.Errorf("unstable answers listed as changed:\n%s", out)
	}
}

func TestOptimize_AnalyzesWithoutWeightedGraph(t *testing.T) {
	// OpenFGA accepts a tuple cycle through `and`, but the weighted graph the
	// optimizer reads weights from cannot be built for it; the advisories
	// must still be reported.
	const dsl = "model\n  schema 1.1\n\ntype user\n\ntype folder\n  relations\n    define parent: [folder]\n    define allowed: [user]\n    define direct: [user]\n    define viewer: allowed and (direct or viewer from parent)\n"
	out, _, err := runOptimize(t, dsl, false, "-")
	if err != nil {
		t.Fatalf("want success, got code %d: %v", clierr.Code(err), err)
	}
	if !strings.Contains(out, "Caller recommendations") {
		t.Errorf("output lacks the caller recommendations:\n%s", out)
	}
}

func TestOptimize_NothingToDoCountHonorsTier(t *testing.T) {
	const dsl = "model\n  schema 1.1\n\ntype user\n\ntype folder\n  relations\n    define parent: [folder]\n    define viewer: [user] or viewer from parent\n"
	_, errAll, err := runOptimize(t, dsl, false, "-")
	if err != nil {
		t.Fatal(err)
	}
	_, errCaller, err := runOptimize(t, dsl, false, "-", "--tier", "caller")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errAll, "no safe rewrites; 0 recommendations") {
		t.Fatalf("unfiltered stderr = %q, want a nonzero count", errAll)
	}
	if !strings.Contains(errCaller, "no safe rewrites; 0 caller recommendations") {
		t.Errorf("--tier caller stderr = %q", errCaller)
	}
	_, errMulti, err := runOptimize(t, "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n", false, "-", "--tier", "Caller,tuple,CALLER")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errMulti, "no safe rewrites; 0 caller/tuple recommendations") {
		t.Errorf("--tier caller,tuple stderr = %q", errMulti)
	}
}

func TestOptimize_TierIsCaseInsensitiveAndAcceptsAll(t *testing.T) {
	if _, _, err := runOptimize(t, "", false, optimizeReference, "--tier", "SAFE,Tuple"); err != nil {
		t.Fatalf("--tier SAFE,Tuple: %v", err)
	}
	def, _, err := runOptimize(t, "", false, optimizeReference)
	if err != nil {
		t.Fatal(err)
	}
	all, _, err := runOptimize(t, "", false, optimizeReference, "--tier", "all")
	if err != nil {
		t.Fatal(err)
	}
	if all != def {
		t.Errorf("--tier all differs from default:\n%s\nvs\n%s", all, def)
	}
	const none = "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n    define viewer: [user]\n"
	for _, tier := range []string{"all", "caller,ALL"} {
		_, errAll, err := runOptimize(t, none, false, "-", "--tier", tier)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(errAll, "no safe rewrites; 0 recommendations") || strings.Contains(errAll, "0 all") {
			t.Errorf("--tier %s stderr = %q", tier, errAll)
		}
	}
	cmd := New(cli.New(log.New(io.Discard), config.New(), "test")).optimizeCmd()
	fn, ok := cmd.GetFlagCompletionFunc("tier")
	if !ok {
		t.Fatal("no tier completion")
	}
	got, _ := fn(cmd, nil, "")
	if !slices.Contains(got, "all") {
		t.Errorf("completions = %v, want all", got)
	}
}

func TestOptimize_EmptyFileIsUsage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.fga")
	if err := os.WriteFile(p, []byte("  \n\t"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := runOptimize(t, "", false, p)
	wantUsage(t, err)
	if want := "model file is empty: " + p; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want %q", err, want)
	}
	_, _, err = runOptimize(t, "  \n", false, "-")
	wantUsage(t, err)
	if err == nil || !strings.Contains(err.Error(), "model from stdin is empty") {
		t.Errorf("err = %v", err)
	}
}

func TestStdinHint(t *testing.T) {
	var b bytes.Buffer
	stdinHint(&b, false)
	if b.Len() != 0 {
		t.Errorf("non-TTY printed %q", b.String())
	}
	stdinHint(&b, true)
	if !strings.Contains(b.String(), "reading model from stdin (press Ctrl-D to finish)") {
		t.Errorf("hint = %q", b.String())
	}
}

func TestOptimize_SynthWithoutWeightedGraphIsUsage(t *testing.T) {
	_, _, err := runOptimize(t, vacuousModel(t), false, "-", "--bench", "--synth", "--iterations", "1")
	if clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("code = %d, err = %v", clierr.Code(err), err)
	}
	if !errors.Is(err, optimize.ErrNoWeightedGraph) {
		t.Errorf("err = %v, want it to wrap optimize.ErrNoWeightedGraph", err)
	}
	h := clierr.NewEnvelope(err).Error.Hint
	const prefix = "this model's weighted graph cannot be built ("
	const suffix = "), so tuples cannot be synthesized; benchmark with --workspace instead"
	if !strings.HasPrefix(h, prefix) || !strings.HasSuffix(h, suffix) || len(h) == len(prefix)+len(suffix) {
		t.Errorf("hint = %q, want %q<builder message>%q", h, prefix, suffix)
	}
	builder := strings.TrimSuffix(strings.TrimPrefix(h, prefix), suffix)
	if strings.Contains(err.Error(), builder) {
		t.Errorf("error text repeats the builder message already in the hint: %q", err)
	}
	if strings.Contains(h, "an AND or BUT NOT sits on a tuple cycle") {
		t.Errorf("hint still carries the guessed parenthetical: %q", h)
	}
}

// vacuousModel is a model whose only safe rewrite (S4) is gated because it
// changes whether OpenFGA can build the weighted graph.
func vacuousModel(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../optimize/testdata/ttu_multi_parent_vacuous.fga")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestOptimize_GatedRewriteIsListed(t *testing.T) {
	out, _, err := runOptimize(t, vacuousModel(t), false, "-")
	if err != nil {
		t.Fatal(err)
	}
	out = ansi.Strip(out)
	for _, want := range []string{"Not applied (1)", "[S4]", "\n    - ", "\n    + ", "not applied: changes whether OpenFGA can build the weighted graph"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if i, j := strings.Index(out, "Not applied"), strings.Index(out, "Caller recommendations"); j >= 0 && i > j {
		t.Errorf("Not applied should precede the advisories:\n%s", out)
	}
	callerOnly, _, err := runOptimize(t, vacuousModel(t), false, "-", "--tier", "caller")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(callerOnly, "Not applied") {
		t.Errorf("--tier caller still prints Not applied:\n%s", callerOnly)
	}
	jout, _, err := runOptimize(t, vacuousModel(t), true, "-")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Rewrite *json.RawMessage `json:"rewrite"`
		Gated   []struct {
			ID, Key, Gate, Reason string
		} `json:"gated"`
	}
	if err := json.Unmarshal([]byte(jout), &got); err != nil {
		t.Fatal(err)
	}
	if got.Rewrite != nil || len(got.Gated) != 1 || got.Gated[0].ID != "S4" || got.Gated[0].Key == "" ||
		got.Gated[0].Gate != "changes whether OpenFGA can build the weighted graph" || got.Gated[0].Reason == "" || got.Gated[0].Reason == got.Gated[0].Gate {
		t.Errorf("json gated = %+v rewrite=%v\n%s", got.Gated, got.Rewrite, jout)
	}
}

// commentedUnsorted returns the reference model with a leading comment and two
// relations swapped out of alphabetical order.
func commentedUnsorted(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(optimizeReference)
	if err != nil {
		t.Fatal(err)
	}
	s := "# my model\n" + string(data)
	a := "    define owner: [user]\n    define editor: [user, group#member] or owner\n"
	b := "    define editor: [user, group#member] or owner\n    define owner: [user]\n"
	if !strings.Contains(s, a) {
		t.Fatal("fixture shape changed")
	}
	return strings.Replace(s, a, b, 1)
}

// jsonDiff runs optimize with --json and returns rewrite.diff (the human
// renderer colours diff lines, so the JSON field is the byte-exact source).
func jsonDiff(t *testing.T, stdin string, args ...string) (diff, stderr string) {
	t.Helper()
	stdout, jsonStderr, err := runOptimize(t, stdin, true, args...)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Rewrite struct{ Diff string } `json:"rewrite"`
	}
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatal(err)
	}
	return v.Rewrite.Diff, jsonStderr
}

func TestOptimize_DiffAppliesToOriginalFile(t *testing.T) {
	original := commentedUnsorted(t)
	p := filepath.Join(t.TempDir(), "model.fga")
	if err := os.WriteFile(p, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := runOptimize(t, "", false, p)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(stderr, "rewrite reformats the model: comments are dropped and relations are sorted"); n != 1 {
		t.Fatalf("want the reformat warning once, got %d:\n%s", n, stderr)
	}
	diff, jsonStderr := jsonDiff(t, "", p)
	if !strings.Contains(jsonStderr, reformatWarning) {
		t.Errorf("--json must still warn on stderr:\n%s", jsonStderr)
	}
	if !strings.HasPrefix(diff, "--- "+p+"\n") {
		t.Errorf("diff should be labelled with the file path:\n%s", diff)
	}
	got, err := applyUnified(diff, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runOptimize(t, "", false, p, "--write"); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(written) {
		t.Errorf("diff applied to the original != file after --write\n--- got\n%s\n--- written\n%s", got, written)
	}
}

func TestOptimize_StdinAndJSONDiffStayNormalized(t *testing.T) {
	data, err := os.ReadFile(optimizeReference)
	if err != nil {
		t.Fatal(err)
	}
	d, warn := jsonDiff(t, string(data), "-")
	if !strings.HasPrefix(d, "--- model\n+++ model (optimized)\n") {
		t.Errorf("stdin labels wrong:\n%s", d)
	}
	if strings.Contains(warn, "rewrite reformats") {
		t.Errorf("stdin input must not warn:\n%s", warn)
	}

	lm, err := modeltest.LoadModelBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	js, err := protojson.Marshal(lm.Proto)
	if err != nil {
		t.Fatal(err)
	}
	jp := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(jp, js, 0o600); err != nil {
		t.Fatal(err)
	}
	d, warn = jsonDiff(t, "", jp)
	if !strings.HasPrefix(d, "--- "+jp+" (normalized)\n") {
		t.Errorf("json label wrong:\n%s", d)
	}
	if strings.Contains(warn, "rewrite reformats") {
		t.Errorf("JSON model input must not warn:\n%s", warn)
	}
}

func TestOptimize_OutputFileSameAsInputWarns(t *testing.T) {
	p := copyReference(t)
	_, stderr, err := runOptimize(t, "", false, p, "--output-file", p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "rewrite reformats the model") {
		t.Errorf("want reformat warning, got:\n%s", stderr)
	}
}

const noRewriteModel = "model\n  schema 1.1\n\ntype user\n\ntype document\n  relations\n    define viewer: [user]\n"

func TestOptimize_OutputFileStdoutWritesOnlyDSL(t *testing.T) {
	jsonOut, _, err := runOptimize(t, "", true, optimizeReference)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Rewrite struct {
			DSL string `json:"dsl"`
		} `json:"rewrite"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := runOptimize(t, "", false, optimizeReference, "--output-file", "-")
	if err != nil {
		t.Fatal(err)
	}
	if out != got.Rewrite.DSL {
		t.Errorf("stdout is not exactly the rewritten DSL:\n%s", out)
	}
	if strings.Contains(out, "Safe rewrites") {
		t.Errorf("findings leaked to stdout:\n%s", out)
	}
	if !strings.Contains(errOut, "wrote rewritten model to stdout") {
		t.Errorf("stderr lacks success line:\n%s", errOut)
	}
}

func TestOptimize_OutputFileStdoutWithBench(t *testing.T) {
	jsonOut, _, err := runOptimize(t, "", true, optimizeReference)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Rewrite struct {
			DSL string `json:"dsl"`
		} `json:"rewrite"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatal(err)
	}
	out, _, err := runOptimize(t, "", false, optimizeReference, "--output-file", "-", "--bench", "--synth", "--iterations", "1")
	if err != nil {
		t.Fatal(err)
	}
	if out != got.Rewrite.DSL {
		t.Errorf("stdout is not exactly the rewritten DSL:\n%s", out)
	}
}

func TestOptimize_OutputFileStdoutWithJSONIsUsage(t *testing.T) {
	_, _, err := runOptimize(t, "", true, optimizeReference, "--output-file", "-")
	wantUsage(t, err)
	if !strings.Contains(err.Error(), "--output-file - and --json both write to stdout; pick one") {
		t.Errorf("message = %q", err)
	}
}

func TestOptimize_OutputFileWithoutRewriteWritesUnchangedModel(t *testing.T) {
	// --output-file resolves symlinks; macOS's TempDir is under a symlink.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out.fga")
	if err := os.WriteFile(dest, []byte("junk from an earlier run"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := runOptimize(t, noRewriteModel, false, "-", "--output-file", dest)
	if err != nil {
		t.Fatal(err)
	}
	if want := "no safe rewrites; wrote the unchanged model to " + dest; !strings.Contains(errOut, want) {
		t.Errorf("stderr lacks %q:\n%s", want, errOut)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != noRewriteModel {
		t.Errorf("out file = %q, %v; want the input bytes", got, err)
	}

	out, errOut, err := runOptimize(t, noRewriteModel, false, "-", "--output-file", "-")
	if err != nil {
		t.Fatal(err)
	}
	if out != noRewriteModel {
		t.Errorf("stdout = %q, want the input bytes", out)
	}
	if want := "no safe rewrites; wrote the unchanged model to stdout"; !strings.Contains(errOut, want) {
		t.Errorf("stderr lacks %q:\n%s", want, errOut)
	}
}

func TestOptimize_OutputFileStdoutWithYAMLIsUsage(t *testing.T) {
	configtest.Isolate(t)
	a := cli.New(log.New(io.Discard), config.New(), "test")
	a.YAML = true
	cmd := New(a).optimizeCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{optimizeReference, "--output-file", "-"})
	err := cmd.Execute()
	wantUsage(t, err)
	if !strings.Contains(err.Error(), "--output-file - and --yaml both write to stdout; pick one") {
		t.Errorf("message = %q", err)
	}
}

func TestOptimize_WriteNothingWrittenLeavesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.fga")
	if err := os.WriteFile(p, []byte(noRewriteModel), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	_, errOut, err := runOptimize(t, "", false, p, "--write")
	if err != nil {
		t.Fatal(err)
	}
	if want := "no safe rewrites; nothing written to " + resolved; !strings.Contains(errOut, want) {
		t.Errorf("stderr lacks %q:\n%s", want, errOut)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != noRewriteModel {
		t.Errorf("file changed: %q, %v", got, err)
	}
}

func TestOptimize_OutputFileDirectoryIsRuntimeError(t *testing.T) {
	_, _, err := runOptimize(t, "", false, optimizeReference, "--output-file", t.TempDir())
	if err == nil {
		t.Fatal("want an error writing to a directory")
	}
	if clierr.Code(err) != clierr.CodeError {
		t.Fatalf("code = %d, want %d: %v", clierr.Code(err), clierr.CodeError, err)
	}
	if strings.Contains(clierr.NewEnvelope(err).Error.Message, "OpenFGA server") {
		t.Fatalf("message mentions the server: %v", err)
	}
}

func TestOptimize_JSONSyntaxErrorHasLineAndColumn(t *testing.T) {
	dsl := "model\n  schema 1.1\ntype user\ntype group\n  relations\n    define member: [user\n"
	_, _, err := runOptimize(t, dsl, true, "-")
	if err == nil {
		t.Fatal("want a parse error")
	}
	e := clierr.NewEnvelope(err).Error
	if len(e.Details) == 0 || e.Details[0]["line"] != 5 {
		t.Fatalf("details = %#v (code %q, message %q)", e.Details, e.Code, e.Message)
	}
}

func TestOptimize_BenchReportsUnexercised(t *testing.T) {
	t.Setenv("OPENFGA_AGENT_MODE", "0")
	const model = "../../optimize/testdata/cond_tupleset.fga"
	const rewritten = "doc#viewer"
	args := []string{model, "--bench", "--synth", "--iterations", "1"}
	got := benchJSON(t, args...)
	if !slices.Contains(got.Bench.Unexercised, rewritten) {
		t.Fatalf("bench.unexercised = %v, want it to contain %q (skipped=%v)", got.Bench.Unexercised, rewritten, got.Bench.Skipped)
	}
	out, _, err := runOptimize(t, "", false, args...)
	if err != nil {
		t.Fatal(err)
	}
	out = ansi.Strip(out)
	for _, want := range []string{"rewritten relation was only partly exercised (a skipped restriction reaches it)", rewritten, "benchmark with --workspace to cover them"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output lacks %q:\n%s", want, out)
		}
	}
}

func TestOptimize_HumanPrintsFindingsBeforeBench(t *testing.T) {
	t.Setenv("OPENFGA_AGENT_MODE", "0")
	out, errOut, err := runOptimize(t, "", false, optimizeReference, "--bench", "--iterations", "1")
	if err != nil {
		t.Fatal(err)
	}
	out = ansi.Strip(out)
	if !regexp.MustCompile(`benchmarking \d+ checks × 1 runs…`).MatchString(ansi.Strip(errOut)) || strings.Contains(errOut, "benchmarking 1 checks") {
		t.Errorf("progress line missing on stderr:\n%s", errOut)
	}
	f, b := strings.Index(out, "Safe rewrites"), strings.Index(out, "Benchmark ·")
	if f < 0 || b < 0 || f >= b {
		t.Errorf("Safe rewrites (%d) should print before Benchmark (%d):\n%s", f, b, out)
	}
	_, errOut, err = runOptimize(t, "", true, optimizeReference, "--bench", "--iterations", "1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "benchmarking") {
		t.Errorf("progress line printed under --json:\n%s", errOut)
	}
}

func TestOptimize_WorkspaceBenchReportsUnexercised(t *testing.T) {
	t.Setenv("OPENFGA_AGENT_MODE", "0")
	dir := t.TempDir()
	model, err := os.ReadFile("../../../examples/model-optimize/roles.fga")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"model.fga":         string(model),
		"ofga.yaml":         "version: 1\nmodel: ./model.fga\ntuples:\n  - \"tuples.yaml\"\ntests:\n  - \"tests/**/*.test.yaml\"\n",
		"tuples.yaml":       "- user:anne owner project:1\n",
		"tests/a.test.yaml": "tests:\n  - name: owner\n    check:\n      - {user: user:anne, object: project:1, assertions: {owner: true}}\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{filepath.Join(dir, "model.fga"), "--bench", "--iterations", "1"}
	got := benchJSON(t, args...)
	want := []string{"project#can_read", "project#can_release", "project#contributor"}
	if got.Bench.Source != "workspace" || !slices.Equal(got.Bench.Unexercised, want) {
		t.Fatalf("source=%q unexercised=%v, want workspace %v", got.Bench.Source, got.Bench.Unexercised, want)
	}
	out, _, err := runOptimize(t, "", false, args...)
	if err != nil {
		t.Fatal(err)
	}
	out = ansi.Strip(out)
	for _, w := range []string{"3 rewritten relations were not exercised by any workspace check: ", "project#can_read", "add workspace checks that reach them"} {
		if !strings.Contains(out, w) {
			t.Errorf("human output lacks %q:\n%s", w, out)
		}
	}
}

func TestOptimize_PlainKeepsDisabledReason(t *testing.T) {
	output.Plain = true
	t.Cleanup(func() { output.Plain = false })
	_, stderr, err := runOptimize(t, "", false, "../../../examples/model-optimize/exclusion.fga")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "safe rewrites disabled:") {
		t.Errorf("plain mode dropped the reason:\n%s", stderr)
	}
	output.Quiet = true
	t.Cleanup(func() { output.Quiet = false })
	_, stderr, _ = runOptimize(t, "", false, "../../../examples/model-optimize/exclusion.fga")
	if strings.Contains(stderr, "safe rewrites disabled") {
		t.Errorf("--quiet must suppress it:\n%s", stderr)
	}
}

func TestOptimize_OutputFileWritesThroughSymlink(t *testing.T) {
	target := copyReference(t)
	link := filepath.Join(t.TempDir(), "link.fga")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := runOptimize(t, "", false, optimizeReference, "--output-file", link); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was replaced: %v %v", st, err)
	}
	data, _ := os.ReadFile(target)
	if strings.Contains(string(data), "define can_delete: owner and editor") {
		t.Errorf("target not rewritten:\n%s", data)
	}
}

func TestOptimize_TierWithoutSafeHidesDiff(t *testing.T) {
	out, stderr, err := runOptimize(t, "", false, optimizeReference, "--tier", "caller")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Diff") || strings.Contains(out, "Safe rewrites") {
		t.Errorf("diff or safe section shown despite --tier caller:\n%s", out)
	}
	if !strings.Contains(stderr, "safe rewrites hidden by --tier; --write and --output-file still apply them") {
		t.Errorf("missing hidden note:\n%s", stderr)
	}
}

func TestOptimize_HelpShowsBenchDefaults(t *testing.T) {
	cmd := New(cli.New(log.New(io.Discard), config.New(), "test")).optimizeCmd()
	for name, def := range map[string]string{"depth": "(default 6)", "iterations": "(default 100)", "seed": "(default 1)"} {
		if u := cmd.Flags().Lookup(name).Usage; !strings.Contains(u, def) {
			t.Errorf("--%s usage %q lacks %s", name, u, def)
		}
	}
}

func TestOptimize_TierNoteHiddenInAgentModeEvenWithPlain(t *testing.T) {
	t.Setenv("OPENFGA_AGENT_MODE", "1")
	output.Plain = true
	output.Terse = true
	t.Cleanup(func() { output.Plain = false; output.Terse = false })
	_, stderr, err := runOptimize(t, "", false, optimizeReference, "--tier", "caller")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "hidden by --tier") {
		t.Errorf("--plain must not change whether the note shows in agent mode:\n%s", stderr)
	}
}

func TestOptimize_TierNoteSurvivesPlainButNotQuiet(t *testing.T) {
	const note = "safe rewrites hidden by --tier; --write and --output-file still apply them"
	output.Plain = true
	t.Cleanup(func() { output.Plain = false; output.Quiet = false })
	_, stderr, err := runOptimize(t, "", false, optimizeReference, "--tier", "caller")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, note) {
		t.Errorf("plain mode dropped the note:\n%s", stderr)
	}
	output.Quiet = true
	_, stderr, _ = runOptimize(t, "", false, optimizeReference, "--tier", "caller")
	if strings.Contains(stderr, note) {
		t.Errorf("--quiet must suppress it:\n%s", stderr)
	}
}
