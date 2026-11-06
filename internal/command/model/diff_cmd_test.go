package model

import (
	"bytes"
	"encoding/json"
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
