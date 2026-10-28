package mapping

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

func TestTargetPathDefaultsToMappingYAML(t *testing.T) {
	if got := targetPath(nil); got != "mapping.yaml" {
		t.Fatalf("targetPath(nil) = %q", got)
	}
	if got := targetPath([]string{"other.yaml"}); got != "other.yaml" {
		t.Fatalf("targetPath = %q", got)
	}
}

// The command a hint suggests must run as shown: a path with a space is quoted.
func TestHintsQuoteThePathTheyRun(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "my map.yaml")
	_, err := run(t, newCLI(), "validate", missing)
	if want := "a person runs 'ofga mappings '" + missing + "'' in a terminal"; err == nil || !strings.Contains(clierr.Friendly(err), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}

	noTests := writeFile(t, dir, "no tests.yaml", "version: \"1\"\nrules:\n  - name: r\n    when: input.type == \"x\"\n"+
		"    tuples:\n      - {user: \"user:1\", relation: member, object: \"t:1\"}\n")
	_, err = run(t, newCLI(), "test", noTests)
	if want := "open it in 'ofga mappings '" + noTests + "''"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}
}

// A missing file is a not_found envelope with a hint that is true for agents
// (the bare command is interactive), exits as before, and still shows the
// suggestion to a human.
func TestMissingFilesAreNotFoundWithAHint(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.yaml")
	events := writeFile(t, dir, "events.jsonl", "{}\n")
	for name, args := range map[string][]string{
		"validate": {"validate", missing},
		"test":     {"test", missing},
		"apply":    {"apply", missing, "--events", events, "--dry-run"},
	} {
		_, err := run(t, newCLI(), args...)
		env := clierr.NewEnvelope(err).Error
		if err == nil || clierr.Code(err) != clierr.CodeError || env.Code != clierr.KindNotFound ||
			!strings.Contains(env.Hint, "pass an existing mapping file") || strings.Contains(env.Message, "create one") {
			t.Errorf("%s: err=%v code=%d envelope=%+v", name, err, clierr.Code(err), env)
		}
		if got := clierr.Friendly(err); !strings.Contains(got, "nope.yaml not found") || !strings.Contains(got, "ofga mappings") {
			t.Errorf("%s: human output lost the suggestion: %q", name, got)
		}
	}

	mp, _ := applyFiles(t, membership, "")
	_, err := run(t, newCLI(), "apply", mp, "--events", filepath.Join(dir, "nope.jsonl"), "--dry-run")
	env := clierr.NewEnvelope(err).Error
	if err == nil || clierr.Code(err) != clierr.CodeError || env.Code != clierr.KindNotFound || !strings.Contains(env.Hint, "stdin") {
		t.Errorf("events: err=%v code=%d envelope=%+v", err, clierr.Code(err), env)
	}
	if got := clierr.Friendly(err); !strings.Contains(got, "events file") || !strings.Contains(got, "stdin") {
		t.Errorf("events: human output lost the suggestion: %q", got)
	}
}
