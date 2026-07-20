package model

import (
	"context"
	"io"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// TestTestInitScaffoldsRunnableWorkspace verifies `model test init` writes a
// workspace that loads and passes out of the box, and refuses to clobber
// existing files without --force.
func TestTestInitScaffoldsRunnableWorkspace(t *testing.T) {
	dir := t.TempDir()
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)

	if err := scaffoldWorkspace(cmd, dir, false); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	// A second scaffold without --force must refuse rather than overwrite.
	if err := scaffoldWorkspace(cmd, dir, false); err == nil {
		t.Fatal("want an error scaffolding over existing files without --force")
	}
	// ...but --force overwrites cleanly.
	if err := scaffoldWorkspace(cmd, dir, true); err != nil {
		t.Fatalf("scaffold --force: %v", err)
	}

	ws, err := modeltest.LoadWorkspace(dir)
	if err != nil {
		t.Fatalf("load scaffolded workspace: %v", err)
	}
	eng, err := modeltest.NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res, err := modeltest.Run(context.Background(), ws, modeltest.Options{Engine: eng})
	if err != nil {
		t.Fatalf("run scaffolded workspace: %v", err)
	}
	if res.Summary.Total == 0 || res.Summary.Failed != 0 {
		t.Fatalf("scaffolded workspace should pass; got %+v", res.Summary)
	}
}
