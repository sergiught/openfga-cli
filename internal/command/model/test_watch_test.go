package model

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func TestResolveWatchRootUsesDiscoveredWorkspaceEvenWhenManifestIsMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ofga.yaml"), []byte("version: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "nested", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := resolveWatchRoot(nested, modeltest.WorkspaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if root != dir {
		t.Fatalf("watch root = %q, want discovered workspace %q", root, dir)
	}
}
