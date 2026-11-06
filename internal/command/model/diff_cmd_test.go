package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/modeltest"
)

const (
	diffBase = "model\n  schema 1.1\ntype user\ntype folder\n  relations\n    define viewer: [user]\ntype document\n  relations\n    define parent: [folder]\n    define viewer: [user]\n"
	// diffWider adds a relation (not breaking) and widens viewer (cost up).
	diffWider = "model\n  schema 1.1\ntype user\ntype folder\n  relations\n    define viewer: [user]\ntype document\n  relations\n    define parent: [folder]\n    define viewer: [user] or viewer from parent\n"
	// diffAdded only adds a relation.
	diffAdded = diffBase + "    define editor: [user]\n"
	// diffBroken removes viewer.
	diffBroken = "model\n  schema 1.1\ntype user\ntype folder\n  relations\n    define viewer: [user]\ntype document\n  relations\n    define parent: [folder]\n"
)

func runDiff(t *testing.T, oldDSL, newDSL string, asJSON bool, flags ...string) (stdout, stderr string, err error) {
	t.Helper()
	c := diffCommand(t, "http://127.0.0.1:1")
	c.cli.JSON = asJSON
	cmd := c.diffCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(append([]string{writeDiffFile(t, "old.fga", oldDSL), writeDiffFile(t, "new.fga", newDSL)}, flags...))
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

func TestDiffCmdIdentical(t *testing.T) {
	out, _, err := runDiff(t, diffBase, diffBase, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no changes") {
		t.Errorf("got:\n%s", out)
	}
}

func TestDiffCmdBreakingExit3(t *testing.T) {
	out, _, err := runDiff(t, diffBase, diffBroken, false)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("got %v, want exit code %d", err, clierr.CodeTestFailed)
	}
	if !strings.Contains(out, "Breaking") {
		t.Errorf("got:\n%s", out)
	}
}

func TestDiffCmdAddedOnlyExit0(t *testing.T) {
	if _, _, err := runDiff(t, diffBase, diffAdded, false); err != nil {
		t.Fatal(err)
	}
}

func TestDiffCmdCostWarnsByDefault(t *testing.T) {
	_, stderr, err := runDiff(t, diffBase, diffWider, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "cost") {
		t.Errorf("stderr should mention cost: %q", stderr)
	}
}

func TestDiffCmdFailOnCost(t *testing.T) {
	_, _, err := runDiff(t, diffBase, diffWider, false, "--fail-on-cost")
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("got %v, want exit code %d", err, clierr.CodeTestFailed)
	}
}

func TestDiffCmdJSON(t *testing.T) {
	out, _, err := runDiff(t, diffBase, diffBroken, true)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("got %v", err)
	}
	var doc struct {
		SchemaVersion int              `json:"schemaVersion"`
		Breaking      []map[string]any `json:"breaking"`
		Summary       struct {
			Verdict string `json:"verdict"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if doc.SchemaVersion != 1 || len(doc.Breaking) == 0 || doc.Summary.Verdict != "breaking" {
		t.Errorf("unexpected document: %+v", doc)
	}
}

func TestDiffCmdAlias(t *testing.T) {
	if !slices.Contains(diffCommand(t, "http://127.0.0.1:1").diffCmd().Aliases, "compare") {
		t.Error("missing compare alias")
	}
}

func TestDiffCmdReportGitHub(t *testing.T) {
	c := diffCommand(t, "http://127.0.0.1:1")
	t.Chdir(t.TempDir())
	newPath := "new.fga"
	for name, body := range map[string]string{"old.fga": diffBase, newPath: diffBroken} {
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := c.diffCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"old.fga", newPath, "--report", "github"})
	err := cmd.Execute()
	out := buf.String()
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("got %v, want exit code %d", err, clierr.CodeTestFailed)
	}
	if !strings.Contains(out, "\n::error file=") && !strings.HasPrefix(out, "::error file=") {
		t.Errorf("want an ::error file= line:\n%s", out)
	}
	if want := "::error file=" + modeltest.RepoPath(newPath) + ","; !strings.Contains(out, "\n"+want) {
		t.Errorf("want file= value %q:\n%s", want, out)
	}
	if n := strings.Count(out, "::error "); n != 1 {
		t.Errorf("got %d ::error lines, want 1 (one breaking entry):\n%s", n, out)
	}
	out, _, err = runDiff(t, diffBase, diffWider, false, "--report", "github")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\n::warning ") && !strings.HasPrefix(out, "::warning ") {
		t.Errorf("want a ::warning line:\n%s", out)
	}
	if strings.Contains(out, "::error") {
		t.Errorf("cost increase must not be an error:\n%s", out)
	}
}

func TestDiffCmdReportFileWrittenBeforeExit3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r", "diff.txt")
	out, _, err := runDiff(t, diffBase, diffBroken, false, "--report", "github", "--report-file", path)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("got %v, want exit code %d", err, clierr.CodeTestFailed)
	}
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(b), "::error") {
		t.Errorf("report file: %q", b)
	}
	if strings.Contains(out, "::error") {
		t.Errorf("annotations leaked to stdout:\n%s", out)
	}
}

func TestDiffCmdReportFileRequiresReport(t *testing.T) {
	_, _, err := runDiff(t, diffBase, diffBase, false, "--report-file", filepath.Join(t.TempDir(), "x"))
	if clierr.Code(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "--report-file requires --report") {
		t.Fatalf("got %v", err)
	}
}

func TestDiffCmdReportRejectsJunit(t *testing.T) {
	_, _, err := runDiff(t, diffBase, diffBase, false, "--report", "junit")
	if clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("got %v", err)
	}
}

func TestDiffCmdReportStdoutConflictsWithJSON(t *testing.T) {
	_, _, err := runDiff(t, diffBase, diffBase, true, "--report", "github")
	if clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("got %v", err)
	}
}

func TestDiffCmdReportStoreSidesNoFile(t *testing.T) {
	// Old side is the store's model, new side is stdin: neither is a file.
	c := diffCommand(t, storeServer(t, nil, false))
	cmd := c.diffCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader("model\n  schema 1.1\ntype user\ntype doc\n"))
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"store", "-", "--report", "github"})
	err := cmd.Execute()
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("got %v, want exit code %d", err, clierr.CodeTestFailed)
	}
	if !strings.Contains(out.String(), "::error title=") || strings.Contains(out.String(), "file=") {
		t.Errorf("got:\n%s", out.String())
	}
}

const (
	diffCycleFolder = "model\n  schema 1.1\ntype user\ntype folder\n  relations\n    define parent: [folder]\n    define allowed: [user]\n    define direct: [user]\n    define viewer: allowed and (direct or viewer from parent)\n"
	// diffPlainFolder has no tuple cycle, so its weighted graph builds.
	diffPlainFolder = "model\n  schema 1.1\ntype user\ntype folder\n  relations\n    define parent: [folder]\n    define allowed: [user]\n    define direct: [user]\n    define editor: [user]\n    define viewer: allowed and direct\n"
)

func TestDiffCmdCostSkippedReasonPrefixedOnce(t *testing.T) {
	for _, tc := range []struct {
		name, old, new, side string
	}{
		{"new side", diffPlainFolder, diffCycleFolder, "new model: "},
		{"old side", diffCycleFolder, diffPlainFolder, "old model: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, _ := runDiff(t, tc.old, tc.new, false)
			if got := strings.Count(stderr, "cost comparison skipped"); got != 1 {
				t.Errorf("stderr has the prefix %d times, want once: %q", got, stderr)
			}
			if !strings.Contains(stderr, "cost comparison skipped: "+tc.side) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, "cost comparison skipped: "+tc.side)
			}
			out, jsonStderr, _ := runDiff(t, tc.old, tc.new, true)
			if strings.Contains(jsonStderr, "cost comparison skipped") {
				t.Errorf("structured mode must not write the human warning to stderr: %q", jsonStderr)
			}
			var doc struct {
				CostSkipped string           `json:"costSkipped"`
				Breaking    []map[string]any `json:"breaking"`
				Changed     []map[string]any `json:"changed"`
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, out)
			}
			if !strings.HasPrefix(doc.CostSkipped, tc.side) || strings.Contains(doc.CostSkipped, "cost comparison skipped") {
				t.Errorf("costSkipped = %q, want an unprefixed %q reason", doc.CostSkipped, tc.side)
			}
			if len(doc.Breaking)+len(doc.Changed) == 0 {
				t.Errorf("shape sections should still be reported: %s", out)
			}
		})
	}
}

func TestDiffCmdJSONFailOnCost(t *testing.T) {
	out, _, err := runDiff(t, diffBase, diffWider, true, "--fail-on-cost")
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("got %v, want exit code %d", err, clierr.CodeTestFailed)
	}
	var doc struct {
		Summary struct {
			Verdict string `json:"verdict"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
	}
	if doc.Summary.Verdict != "cost_increased" {
		t.Errorf("verdict = %q", doc.Summary.Verdict)
	}
}

func TestDiffCmdRejectsExplicitModelID(t *testing.T) {
	c := diffCommand(t, "http://127.0.0.1:1")
	cmd := c.diffCmd()
	cmd.Flags().String("model-id", "", "") // the root registers it persistently
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{writeDiffFile(t, "m.fga", diffBase), "--model-id", diffTestModelID})
	err := cmd.Execute()
	if clierr.Code(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "--model-id is not used by model diff; pass the model ID as an argument instead") {
		t.Fatalf("code = %v, err = %v", clierr.Code(err), err)
	}
}

func TestDiffCmdFileNamedStoreWinsOverKeyword(t *testing.T) {
	c := diffCommand(t, "http://127.0.0.1:1") // unreachable: the store must not be contacted
	dir := t.TempDir()
	t.Chdir(dir)
	for name, body := range map[string]string{"store": diffBase, "other.fga": diffBase} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c.cli.JSON = true
	cmd := c.diffCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"store", "other.fga"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Old string `json:"old"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Old != "store" {
		t.Errorf("old = %q, want the local file %q", doc.Old, "store")
	}
}

func TestDiffCmdBadFileReportedBeforeStore(t *testing.T) {
	configtest.Isolate(t)
	c := New(cli.New(log.New(io.Discard), config.New(), "test")) // no store configured
	cmd := c.diffCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{writeDiffFile(t, "bad.fga", "this is not a model")})
	err := cmd.Execute()
	if err == nil || !strings.HasPrefix(err.Error(), "new model:") || strings.Contains(err.Error(), "store") {
		t.Fatalf("err = %v, want the new-side file error", err)
	}
	if clierr.Code(err) != clierr.CodeUsage {
		t.Errorf("code = %v", clierr.Code(err))
	}
}

func TestDiffCmdHelpListsNetworkExitCode(t *testing.T) {
	if !strings.Contains(diffCommand(t, "http://127.0.0.1:1").diffCmd().Long, "4 ") {
		t.Error("Long should document exit code 4")
	}
}

func TestDiffCmdReportEnumAndCompletion(t *testing.T) {
	cmd := diffCommand(t, "http://127.0.0.1:1").diffCmd()
	got, _ := cmd.GetFlagCompletionFunc("report")
	if got == nil {
		t.Fatal("no --report completion registered")
	}
	vals, _ := got(cmd, nil, "")
	if !slices.Equal(vals, []string{"github"}) {
		t.Errorf("completions = %v", vals)
	}
}

func TestDiffCmdImplicitStoreSideNotShadowedByFile(t *testing.T) {
	c := diffCommand(t, storeServer(t, nil, false))
	dir := t.TempDir()
	t.Chdir(dir)
	for name, body := range map[string]string{"store": "model\n  schema 1.1\ntype other\n", "new.fga": diffTestDSL} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c.cli.JSON = true
	cmd := c.diffCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"new.fga"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Old string `json:"old"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if want := "store latest " + diffTestModelID; doc.Old != want {
		t.Errorf("old = %q, want %q", doc.Old, want)
	}
}

func TestDiffCmdStatErrorIsLoadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	c := diffCommand(t, "http://127.0.0.1:1")
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	cmd := c.diffCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{a})
	err := cmd.Execute()
	if err == nil || clierr.Code(err) == clierr.CodeUsage || !strings.Contains(err.Error(), a) {
		t.Fatalf("err = %v (code %v), want a plain load error naming the path", err, clierr.Code(err))
	}
}

// epipeWriter simulates stdout closed early (`| head`).
type epipeWriter struct{}

func (epipeWriter) Write([]byte) (int, error) { return 0, syscall.EPIPE }

func runDiffEPIPE(t *testing.T, oldDSL, newDSL string, asJSON bool, flags ...string) error {
	t.Helper()
	c := diffCommand(t, "http://127.0.0.1:1")
	c.cli.JSON = asJSON
	cmd := c.diffCmd()
	cmd.SetOut(epipeWriter{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(append([]string{writeDiffFile(t, "old.fga", oldDSL), writeDiffFile(t, "new.fga", newDSL)}, flags...))
	return cmd.Execute()
}

func TestDiffCmdBrokenPipeKeepsGateExit(t *testing.T) {
	exitCode := func(err error) int {
		var coded *clierr.Coded
		if errors.As(err, &coded) {
			return coded.C
		}
		return 0
	}
	t.Run("breaking human output", func(t *testing.T) {
		err := runDiffEPIPE(t, diffBase, diffBroken, false)
		if exitCode(err) != clierr.CodeTestFailed || clierr.IsIgnorableBrokenPipe(err) {
			t.Fatalf("want exit 3 not ignorable, got %v", err)
		}
	})
	t.Run("json fail-on-cost", func(t *testing.T) {
		err := runDiffEPIPE(t, diffBase, diffWider, true, "--fail-on-cost")
		if exitCode(err) != clierr.CodeTestFailed {
			t.Fatalf("want exit 3, got %v", err)
		}
	})
	t.Run("clean diff stays ignorable", func(t *testing.T) {
		err := runDiffEPIPE(t, diffBase, diffBase, false)
		if !clierr.IsIgnorableBrokenPipe(err) {
			t.Fatalf("want ignorable broken pipe, got %v", err)
		}
	})
}

// TestDiffCmdExampleModels pins what examples/model-diff/README.md and the
// model-diff demo recording show, so neither drifts from the real output.
func TestDiffCmdExampleModels(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("../../../examples/model-diff", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	out, _, err := runDiff(t, read("old.fga"), read("new.fga"), true)
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("want exit 3, got %v", err)
	}
	var doc struct {
		Summary struct {
			Breaking      int    `json:"breaking"`
			Changed       int    `json:"changed"`
			Added         int    `json:"added"`
			CostIncreased int    `json:"costIncreased"`
			Verdict       string `json:"verdict"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	s := doc.Summary
	if s.Breaking != 1 || s.Changed != 1 || s.Added != 2 || s.CostIncreased != 1 || s.Verdict != "breaking" {
		t.Errorf("example summary changed; update examples/model-diff/README.md and re-record the tape: %+v", s)
	}
}
