package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sergiught/openfga-cli/internal/configtest"
)

// recentFiles makes n files and returns their paths.
func recentFiles(t *testing.T, n int) []string {
	t.Helper()
	dir := t.TempDir()
	var out []string
	for i := range n {
		p := filepath.Join(dir, fmt.Sprintf("m%d.yaml", i))
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestRecentRoundTripAndCap(t *testing.T) {
	file := filepath.Join(filepath.Dir(configtest.Isolate(t)), "sub", "mappings-recent.json")
	paths := recentFiles(t, 10)
	for _, p := range paths {
		if err := rememberRecent(file, p); err != nil {
			t.Fatal(err)
		}
	}
	// Remembering a file again moves it to the front instead of repeating it.
	if err := rememberRecent(file, paths[5]); err != nil {
		t.Fatal(err)
	}
	got := loadRecent(file)
	want := []string{paths[5], paths[9], paths[8], paths[7], paths[6], paths[4], paths[3], paths[2]}
	if !slices.Equal(got, want) {
		t.Fatalf("recent = %v, want %v", got, want)
	}
	if fi, err := os.Stat(filepath.Dir(file)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("directory = %v, %v; want mode 0700", fi, err)
	}
}

func TestRecentDropsMissingFiles(t *testing.T) {
	file := filepath.Join(filepath.Dir(configtest.Isolate(t)), "mappings-recent.json")
	paths := recentFiles(t, 3)
	for _, p := range paths {
		if err := rememberRecent(file, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(paths[1]); err != nil {
		t.Fatal(err)
	}
	if got, want := loadRecent(file), []string{paths[2], paths[0]}; !slices.Equal(got, want) {
		t.Fatalf("recent = %v, want %v", got, want)
	}
	if err := rememberRecent("", paths[0]); err != nil || loadRecent("") != nil {
		t.Fatal("an empty recent file must keep nothing and fail nothing")
	}
}
