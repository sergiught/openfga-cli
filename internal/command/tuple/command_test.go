package tuple

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tuples.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBulkTuples(t *testing.T) {
	cmd := &cobra.Command{}

	// Bare array.
	p := writeTemp(t, `[{"user":"user:anne","relation":"viewer","object":"doc:1"},{"user":"user:bob","relation":"editor","object":"doc:2"}]`)
	keys, err := bulkTuples(cmd, p, nil, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].User != "user:anne" || keys[1].Object != "doc:2" {
		t.Fatalf("unexpected keys: %+v", keys)
	}

	// Wrapper object.
	p = writeTemp(t, `{"tuples":[{"user":"user:anne","relation":"viewer","object":"doc:1"}]}`)
	if keys, err = bulkTuples(cmd, p, nil, "", "", ""); err != nil || len(keys) != 1 {
		t.Fatalf("wrapper form: keys=%v err=%v", keys, err)
	}

	// --file is mutually exclusive with positional args / field flags.
	if _, err := bulkTuples(cmd, p, []string{"user:anne"}, "", "", ""); err == nil {
		t.Error("--file with positional args should error")
	}
	if _, err := bulkTuples(cmd, p, nil, "user:anne", "", ""); err == nil {
		t.Error("--file with --user should error")
	}

	// A malformed triple is rejected.
	p = writeTemp(t, `[{"user":"anne","relation":"viewer","object":"doc:1"}]`)
	if _, err := bulkTuples(cmd, p, nil, "", "", ""); err == nil {
		t.Error("malformed user should be rejected")
	}

	// Empty file.
	p = writeTemp(t, `[]`)
	if _, err := bulkTuples(cmd, p, nil, "", "", ""); err == nil {
		t.Error("empty tuple list should error")
	}
}
