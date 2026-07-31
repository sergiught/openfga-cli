package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The release pipeline copies these exact paths into archives, deb/rpm/apk and
// the Homebrew formula. If a filename moves, the packages silently ship without
// completions or man pages, so pin the contract here rather than finding out
// from a release.
func TestRunWritesCompletionsAndManPages(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir, "v9.9.9"); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	for _, name := range []string{"ofga.bash", "ofga.zsh", "ofga.fish"} {
		path := filepath.Join(dir, "completions", name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("missing completion %s: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("completion %s is empty", name)
		}
	}

	// The root page plus one per command; the exact count moves with the
	// command tree, so assert on the entries that must exist and on there
	// being a realistic number of them.
	manDir := filepath.Join(dir, "man")
	entries, err := os.ReadDir(manDir)
	if err != nil {
		t.Fatalf("read man dir: %v", err)
	}
	if len(entries) < 20 {
		t.Errorf("got %d man pages, want the full command tree", len(entries))
	}
	for _, name := range []string{"ofga.1", "ofga-model-test.1", "ofga-query-check.1"} {
		if _, err := os.Stat(filepath.Join(manDir, name)); err != nil {
			t.Errorf("missing man page %s: %v", name, err)
		}
	}

	// Every page must be section 1 and carry the version through to the footer,
	// and none may be named after the root command's usage string ("ofga
	// [flags].1") — the failure mode when root.Use is left unnormalized.
	root, err := os.ReadFile(filepath.Join(manDir, "ofga.1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(root), `"1"`) {
		t.Errorf("ofga.1 is not marked as a section 1 page:\n%s", firstLines(string(root), 3))
	}
	if !strings.Contains(string(root), "v9.9.9") {
		t.Errorf("ofga.1 does not carry the version:\n%s", firstLines(string(root), 3))
	}
	for _, e := range entries {
		if strings.ContainsAny(e.Name(), " []") {
			t.Errorf("man page %q is named after a usage string, not a command", e.Name())
		}
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
