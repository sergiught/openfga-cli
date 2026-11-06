package model

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/clierr"
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
	out, _, err := runDiff(t, diffBase, diffBroken, false, "--report", "github")
	if clierr.Code(err) != clierr.CodeTestFailed {
		t.Fatalf("got %v, want exit code %d", err, clierr.CodeTestFailed)
	}
	if !strings.Contains(out, "\n::error file=") && !strings.HasPrefix(out, "::error file=") {
		t.Errorf("want an ::error file= line:\n%s", out)
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
