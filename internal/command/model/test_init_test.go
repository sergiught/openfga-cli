package model

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
	schema, err := os.ReadFile(filepath.Join(dir, "workspace.schema.json"))
	if err != nil {
		t.Fatalf("read scaffolded editor schema: %v", err)
	}
	if !json.Valid(schema) {
		t.Fatal("scaffolded workspace.schema.json is not valid JSON")
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

func TestTestInitPreflightsAllDestinations(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "model.fga")
	if err := os.WriteFile(modelPath, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)

	if err := scaffoldWorkspace(cmd, dir, false); err == nil {
		t.Fatal("scaffold should reject an existing destination")
	}
	if _, err := os.Stat(filepath.Join(dir, "ofga.yaml")); !os.IsNotExist(err) {
		t.Fatalf("preflight failure left a partial manifest: %v", err)
	}
	data, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep me" {
		t.Fatalf("existing model was changed: %q", data)
	}
}
