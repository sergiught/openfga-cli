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
	"strings"
	"testing"

	"charm.land/log/v2"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
	"github.com/sergiught/openfga-cli/internal/optimize/bench"
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
		"Safe rewrites (5)",
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

func TestOptimize_JSONHasFiveApplied(t *testing.T) {
	out, _, err := runOptimize(t, "", true, optimizeReference)
	if err != nil {
		t.Fatal(err)
	}
	var got optimizeOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.SchemaVersion != 1 || got.Model.Format != "dsl" || got.Rewrite == nil || len(got.Rewrite.Applied) != 5 {
		t.Fatalf("unexpected output: %+v", got)
	}
	if got.Bench != nil {
		t.Errorf("bench must be nil until requested")
	}
}

func TestOptimize_WriteRewritesFile(t *testing.T) {
	p := copyReference(t)
	_, stderr, err := runOptimize(t, "", false, p, "--write")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "reformats the file and drops comments") {
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
	if got.Rewrite == nil || len(got.Rewrite.Applied) != 5 {
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
	for _, want := range []string{"reads/dispatches/p50/p95", "in-process, indicative only", "deny reads "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	lastDeny, firstAllow := -1, -1
	for i, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || !strings.Contains(l, " / ") {
			continue
		}
		switch f[0] {
		case "deny":
			lastDeny = i
		case "allow":
			if firstAllow < 0 {
				firstAllow = i
			}
		}
	}
	if lastDeny < 0 || (firstAllow >= 0 && firstAllow < lastDeny) {
		t.Errorf("deny rows must come first (lastDeny=%d firstAllow=%d):\n%s", lastDeny, firstAllow, out)
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
		if !strings.Contains(err.Error(), "benchmark found 1 checks whose result changed; rewrite was NOT written") {
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
