package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
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
