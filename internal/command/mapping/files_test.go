package mapping

import (
	"path/filepath"
	"strings"
	"testing"

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
	if want := "create one with 'ofga mappings '" + missing + "''"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}

	noTests := writeFile(t, dir, "no tests.yaml", "version: \"1\"\nrules:\n  - name: r\n    when: input.type == \"x\"\n"+
		"    tuples:\n      - {user: \"user:1\", relation: member, object: \"t:1\"}\n")
	_, err = run(t, newCLI(), "test", noTests)
	if want := "open it in 'ofga mappings '" + noTests + "''"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}
}
