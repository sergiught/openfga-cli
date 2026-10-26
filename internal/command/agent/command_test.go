package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/agentskill"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/prompt"
)

// run executes `agent <args>` and returns stdout, stderr and the error.
func run(t *testing.T, asJSON bool, args ...string) (string, string, error) {
	t.Helper()
	configtest.Isolate(t)
	c := cli.New(log.New(io.Discard), config.New(), "test")
	c.JSON = asJSON
	output.Plain, output.Quiet = false, false
	g := New(c)
	// The real root owns --no-input; the group alone does not.
	g.cmd.PersistentFlags().Bool("no-input", false, "")
	g.cmd.SetIn(strings.NewReader(""))
	var out, errb bytes.Buffer
	g.cmd.SetOut(&out)
	g.cmd.SetErr(&errb)
	g.cmd.SetArgs(args)
	err := g.cmd.Execute()
	return out.String(), errb.String(), err
}

func skillAt(base string) string { return filepath.Join(base, "ofga", "SKILL.md") }

func TestSkillShowPrintsEmbedded(t *testing.T) {
	out, _, err := run(t, false, "skill", "show")
	if err != nil {
		t.Fatal(err)
	}
	if out != string(agentskill.Content()) {
		t.Error("stdout differs from the embedded SKILL.md")
	}
}

func TestSkillShowJSON(t *testing.T) {
	out, _, err := run(t, true, "skill", "show")
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Content string }
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Content != string(agentskill.Content()) {
		t.Error("content differs from the embedded SKILL.md")
	}
}

func TestSkillInstallDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	out, errb, err := run(t, false, "skill", "install")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, ".claude", "skills", "ofga", "SKILL.md")
	if strings.TrimSpace(out) != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if !strings.Contains(errb, "installed the ofga skill to "+want) {
		t.Errorf("stderr missing success line:\n%s", errb)
	}
	b, err := os.ReadFile(want)
	if err != nil || !bytes.Equal(b, agentskill.Content()) {
		t.Fatalf("installed file wrong: %v", err)
	}
	if fi, _ := os.Stat(want); fi.Mode().Perm() != 0o644 {
		t.Errorf("file mode = %v, want 0644", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(want)); fi.Mode().Perm() != 0o755 {
		t.Errorf("dir mode = %v, want 0755", fi.Mode().Perm())
	}
}

func TestSkillInstallProject(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	out, _, err := run(t, false, "skill", "install", "--project")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(tmp, ".claude", "skills", "ofga", "SKILL.md"))
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(out))
	if got != want {
		t.Errorf("stdout path = %q, want %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Error(err)
	}
}

func TestSkillInstallDir(t *testing.T) {
	tmp := t.TempDir()
	out, _, err := run(t, false, "skill", "install", "--dir", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != skillAt(tmp) {
		t.Errorf("stdout = %q, want %q", out, skillAt(tmp))
	}
	if _, err := os.Stat(skillAt(tmp)); err != nil {
		t.Error(err)
	}
}

func TestSkillInstallProjectAndDirConflict(t *testing.T) {
	_, _, err := run(t, false, "skill", "install", "--project", "--dir", t.TempDir())
	if err == nil {
		t.Fatal("want an error")
	}
	if got := clierr.Code(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
}

func TestSkillInstallIdempotent(t *testing.T) {
	tmp := t.TempDir()
	type res struct {
		Path    string `json:"path"`
		Changed bool   `json:"changed"`
	}
	var first, second res
	out, _, err := run(t, true, "skill", "install", "--dir", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &first); err != nil {
		t.Fatal(err)
	}
	out, errb, err := run(t, true, "skill", "install", "--dir", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &second); err != nil {
		t.Fatal(err)
	}
	if !first.Changed || second.Changed || second.Path != skillAt(tmp) {
		t.Errorf("first=%+v second=%+v", first, second)
	}
	_ = errb

	// Human mode on an unchanged file says so on stderr and still prints the path.
	out, errb, err = run(t, false, "skill", "install", "--dir", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != skillAt(tmp) || !strings.Contains(errb, "is already up to date") {
		t.Errorf("stdout=%q stderr=%q", out, errb)
	}
}

func TestSkillInstallRefusesDifferingWithoutForce(t *testing.T) {
	tmp := t.TempDir()
	path := skillAt(tmp)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, false, "skill", "install", "--dir", tmp, "--no-input")
	if !errors.Is(err, prompt.ErrNeedsForce) {
		t.Fatalf("err = %v, want ErrNeedsForce", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "x" {
		t.Error("file was modified without --force")
	}
	if _, _, err := run(t, false, "skill", "install", "--dir", tmp, "--force"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, agentskill.Content()) {
		t.Error("file not replaced with --force")
	}
}
