package mapping

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/output"
)

// run executes `mapping init` with args against a fresh CLI, returning stderr.
func run(t *testing.T, c *cli.CLI, args ...string) (string, error) {
	t.Helper()
	root := New(c).Command()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	root.SilenceUsage = true
	root.SilenceErrors = true
	err := root.Execute()
	return errb.String() + out.String(), err
}

func codeOf(t *testing.T, err error) int {
	t.Helper()
	var coded *clierr.Coded
	if !errors.As(err, &coded) {
		t.Fatalf("error is not coded: %v", err)
	}
	return coded.C
}

func TestGroupHasInitAndAlias(t *testing.T) {
	cmd := New(cli.New(log.New(io.Discard), config.New(), "test")).Command()
	if cmd.Use != "mapping" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	if len(cmd.Aliases) != 1 || cmd.Aliases[0] != "mappings" {
		t.Fatalf("Aliases = %v", cmd.Aliases)
	}
	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	if strings.Join(names, ",") != "init" {
		t.Fatalf("subcommands = %v", names)
	}
}

// The bare group prints help and succeeds, matching cli.GroupRunE's
// documented behavior and every sibling group (tuples, query, store, model,
// assertions, configcmd) that shares the same RunE.
func TestBareGroupPrintsHelp(t *testing.T) {
	c := cli.New(log.New(io.Discard), config.New(), "test")
	c.NoInput = true
	out, err := run(t, c)
	if err != nil {
		t.Fatalf("bare group should print help, not error: %v", err)
	}
	if !strings.Contains(out, "mapping") {
		t.Fatalf("expected help output, got %q", out)
	}
}

func TestNoInputIsAUsageError(t *testing.T) {
	c := cli.New(log.New(io.Discard), config.New(), "test")
	c.NoInput = true
	out, err := run(t, c, "init")
	if err == nil {
		t.Fatal("expected a usage error")
	}
	if got := codeOf(t, err); got != clierr.CodeUsage {
		t.Fatalf("exit code = %d, want %d", got, clierr.CodeUsage)
	}
	if !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("error = %q", err.Error())
	}
	if strings.Contains(out, "wrote") {
		t.Fatalf("nothing should have been written: %q", out)
	}
}

func TestJSONOutputIsAUsageError(t *testing.T) {
	c := cli.New(log.New(io.Discard), config.New(), "test")
	c.JSON = true
	_, err := run(t, c, "init")
	if err == nil || codeOf(t, err) != clierr.CodeUsage {
		t.Fatalf("err = %v", err)
	}
}

func TestExistingFileWithoutForceIsAUsageError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mapping.yaml")
	if err := os.WriteFile(path, []byte("version: \"1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := cli.New(log.New(io.Discard), config.New(), "test")
	c.NoInput = true

	_, err := run(t, c, "init", path)
	if err == nil {
		t.Fatal("expected a usage error")
	}
	if got := codeOf(t, err); got != clierr.CodeUsage {
		t.Fatalf("exit code = %d", got)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error should point at --force: %q", err.Error())
	}
	// The existence check must run before the TTY check, so the message a user
	// most likely needs is the one they get.
	if strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("existence should be reported first: %q", err.Error())
	}
}

func TestForceSkipsTheExistenceCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mapping.yaml")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := cli.New(log.New(io.Discard), config.New(), "test")
	c.NoInput = true

	_, err := run(t, c, "init", path, "--force")
	if err == nil {
		t.Fatal("expected the TTY check to fail next")
	}
	if !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("error = %q", err.Error())
	}
	// --force must not have truncated the file just by being parsed.
	if b, _ := os.ReadFile(path); string(b) != "old\n" {
		t.Fatalf("file was touched: %q", b)
	}
}

func TestTargetPathDefaultsToMappingYAML(t *testing.T) {
	if got := targetPath(nil); got != "mapping.yaml" {
		t.Fatalf("targetPath(nil) = %q", got)
	}
	if got := targetPath([]string{"other.yaml"}); got != "other.yaml" {
		t.Fatalf("targetPath = %q", got)
	}
}

func TestTooManyArgs(t *testing.T) {
	c := cli.New(log.New(io.Discard), config.New(), "test")
	c.NoInput = true
	if _, err := run(t, c, "init", "a.yaml", "b.yaml"); err == nil {
		t.Fatal("expected an argument-count error")
	}
}

func TestSaveMappingCreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "mapping.yaml")
	if err := saveMapping(path, []byte("version: \"1\"\n")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "version: \"1\"\n" {
		t.Fatalf("contents = %q", b)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("perm = %o, want 644", perm)
	}
}

func TestSaveMappingOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapping.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveMapping(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Fatalf("contents = %q", b)
	}
}

// wizardEligible must reject a non-file stdin outright rather than assuming a
// terminal; cobra hands tests a bytes.Buffer.
func TestWizardEligibleRejectsNonFileStdin(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetIn(bytes.NewBufferString(""))
	if wizardEligible(cmd, cli.New(log.New(io.Discard), config.New(), "test")) {
		t.Fatal("a buffer stdin should not be eligible")
	}
}

func TestSuccessOutputNamesTheFileAndCounts(t *testing.T) {
	r := &wizardResult{rules: 2, tuples: 3, tests: 2}
	if got := r.summary(); got != "2 rules, 3 tuples, 2 embedded tests" {
		t.Fatalf("summary = %q", got)
	}
	one := &wizardResult{rules: 1, tuples: 1, tests: 1}
	if got := one.summary(); got != "1 rule, 1 tuple, 1 embedded test" {
		t.Fatalf("summary = %q", got)
	}
}

// The wizard's output lives only in memory until saveMapping writes it, and by
// then the TUI is gone. A failing write used to return the error and drop the
// mapping with it — the one moment in the flow where the user can lose work they
// cannot get back by pressing esc.
func TestAFailedWriteHandsTheMappingBack(t *testing.T) {
	data := []byte("version: \"1\"\nrules:\n  - name: joined\n")

	cmd := &cobra.Command{}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)

	rescue(cmd, data)

	// stdout, so `ofga mapping init > mapping.yaml` catches it verbatim.
	if !strings.Contains(out.String(), string(data)) {
		t.Fatalf("the mapping was not printed to stdout:\n%s", out.String())
	}
	if strings.Contains(errb.String(), string(data)) {
		t.Fatalf("the mapping went to stderr, where a redirect would not catch it:\n%s", errb.String())
	}
	// and an explanation beside the error, or the YAML reads as success.
	if !strings.Contains(errb.String(), "could not be written") {
		t.Fatalf("stderr does not say why the mapping was printed:\n%s", errb.String())
	}
}

// `ofga mapping` stops at init. Checking the file and running its tests live in
// openfga's own CLI, and a user who has never seen those commands has no reason
// to guess they exist — least of all that both work offline.
func TestNextStepsNamesWhatThisCommandCannotDoItself(t *testing.T) {
	var b bytes.Buffer
	nextSteps(&b, "auth0.yaml", 0)
	got := b.String()

	for _, want := range []string{
		"fga mapping validate auth0.yaml",
		"--model-file",
		"offline",
		languageSpecURL,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("next steps do not mention %q:\n%s", want, got)
		}
	}
}

// The wizard writes a test per sample, so a user who worked through several
// events leaves with a suite. Pointing at the runner is only worth two lines
// when there is actually something to run.
func TestNextStepsOnlyPointsAtTheTestRunnerWhenThereAreTests(t *testing.T) {
	var with, without bytes.Buffer
	nextSteps(&with, "auth0.yaml", 2)
	nextSteps(&without, "auth0.yaml", 0)

	if !strings.Contains(with.String(), "fga mapping test auth0.yaml") {
		t.Fatalf("a mapping with tests should name the runner:\n%s", with.String())
	}
	if strings.Contains(without.String(), "fga mapping test") {
		t.Fatalf("a mapping with no tests has no runner to name:\n%s", without.String())
	}
}

// This CLI is not Auth0's and must not read as though it ships their hosted
// sync product: no walking the user through someone else's dashboard, and no
// speaking for the release status of a service we do not run.
func TestNextStepsDoesNotSpeakForAnyoneElsesProduct(t *testing.T) {
	var b bytes.Buffer
	nextSteps(&b, "auth0.yaml", 2)
	got := strings.ToLower(b.String())

	for _, banned := range []string{"relationship sync", "dashboard", "pipeline", "beta"} {
		if strings.Contains(got, banned) {
			t.Fatalf("next steps mention %q:\n%s", banned, b.String())
		}
	}
}

// Hintf has no Quiet guard of its own — its usual job is remediation after an
// error, which --quiet keeps. A pointer after a success is the other case, so
// nextSteps has to guard itself or --quiet prints commands while suppressing
// the success line they follow.
func TestNextStepsIsSilentUnderQuietAndPlain(t *testing.T) {
	defer func(p, q bool) { output.Plain, output.Quiet = p, q }(output.Plain, output.Quiet)

	for _, tc := range []struct {
		name         string
		plain, quiet bool
	}{
		{"quiet", false, true},
		{"plain", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output.Plain, output.Quiet = tc.plain, tc.quiet
			var b bytes.Buffer
			nextSteps(&b, "auth0.yaml", 2)
			if b.Len() != 0 {
				t.Fatalf("expected silence, got:\n%s", b.String())
			}
		})
	}
}
