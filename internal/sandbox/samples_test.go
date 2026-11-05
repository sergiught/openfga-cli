package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func TestSampleNames_Order(t *testing.T) {
	require.Equal(t, []string{
		"github", "gdrive", "slack", "custom-roles",
		"multitenant-rbac", "temporal-access", "expenses",
	}, SampleNames())
}

func TestExtractSamples_AllLoadAndPass(t *testing.T) {
	want := map[string]int{
		"github": 4, "gdrive": 4, "slack": 3, "custom-roles": 3,
		"multitenant-rbac": 3, "temporal-access": 4, "expenses": 3,
	}
	dir := t.TempDir()
	roots, err := ExtractSamples(dir)
	require.NoError(t, err)
	require.Len(t, roots, len(want))

	for _, name := range SampleNames() {
		t.Run(name, func(t *testing.T) {
			root := roots[name]
			require.Equal(t, filepath.Join(dir, name), root)

			ws, err := modeltest.LoadWorkspace(root)
			require.NoError(t, err)

			eng, err := modeltest.NewEmbeddedEngine(nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = eng.Close() })

			res, err := modeltest.Run(context.Background(), ws, modeltest.Options{
				Engine: eng, Parallel: 2, Timeout: 30 * time.Second,
			})
			require.NoError(t, err)
			require.Equal(t, want[name], res.Summary.Total)
			require.Zero(t, res.Summary.Failed)
		})
	}
}

func TestExtractSamples_MatchesEmbed(t *testing.T) {
	dir := t.TempDir()
	_, err := ExtractSamples(dir)
	require.NoError(t, err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, len(SampleNames()))
	for _, name := range SampleNames() {
		require.FileExists(t, filepath.Join(dir, name, "model.fga"))
		require.FileExists(t, filepath.Join(dir, name, "ofga.yaml"))
	}
	require.NoFileExists(t, filepath.Join(dir, "LICENSE"))
}
