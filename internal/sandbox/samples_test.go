package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func TestSampleNames_Order(t *testing.T) {
	if !reflect.DeepEqual(SampleNames(), []string{
		"github", "gdrive", "slack", "custom-roles",
		"multitenant-rbac", "temporal-access", "expenses",
	}) {
		t.Fatalf("got %v, want %v", SampleNames(), []string{
			"github", "gdrive", "slack", "custom-roles",
			"multitenant-rbac", "temporal-access", "expenses",
		})
	}
}

func TestExtractSamples_AllLoadAndPass(t *testing.T) {
	want := map[string]int{
		"github": 4, "gdrive": 4, "slack": 3, "custom-roles": 3,
		"multitenant-rbac": 3, "temporal-access": 4, "expenses": 3,
	}
	dir := t.TempDir()
	roots, err := ExtractSamples(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(roots); got != len(want) {
		t.Fatalf("len = %d, want %d", got, len(want))
	}

	for _, name := range SampleNames() {
		t.Run(name, func(t *testing.T) {
			root := roots[name]
			if root != filepath.Join(dir, name) {
				t.Fatalf("got %v, want %v", root, filepath.Join(dir, name))
			}

			ws, err := modeltest.LoadWorkspace(root)
			if err != nil {
				t.Fatal(err)
			}

			eng, err := modeltest.NewEmbeddedEngine(nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = eng.Close() })

			res, err := modeltest.Run(context.Background(), ws, modeltest.Options{
				Engine: eng, Parallel: 2, Timeout: 30 * time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			if res.Summary.Total != want[name] {
				t.Fatalf("got %v, want %v", res.Summary.Total, want[name])
			}
			if res.Summary.Failed != 0 {
				t.Fatalf("got %v, want 0", res.Summary.Failed)
			}
		})
	}
}

func TestExtractSamples_MatchesEmbed(t *testing.T) {
	dir := t.TempDir()
	_, err := ExtractSamples(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(SampleNames()) {
		t.Fatalf("len = %d, want %d", len(entries), len(SampleNames()))
	}
	for _, name := range SampleNames() {
		if _, err := os.Stat(filepath.Join(dir, name, "model.fga")); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, name, "ofga.yaml")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "LICENSE")); err == nil {
		t.Fatalf("%s exists, want it absent", filepath.Join(dir, "LICENSE"))
	}
}
