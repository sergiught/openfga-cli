package configtest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNoTestIsolatesConfigViaXDG fails when a test isolates the CLI's config by
// setting XDG_CONFIG_HOME instead of calling configtest.Isolate.
//
// That idiom is a no-op on macOS: go-app-paths puts the config under
// ~/Library/Application Support there and ignores XDG_CONFIG_HOME entirely, so
// the test falls through to the developer's real config — which the TUI writes
// on exit. It was swept out of the tree once already and came straight back in
// two PRs that had branched before the sweep landed, each green on its own and
// only red once merged together. A guard catches the next one at the point it
// is written rather than on a macOS runner.
func TestNoTestIsolatesConfigViaXDG(t *testing.T) {
	root := repoRoot(t)
	// This file names the forbidden idiom in order to search for it, so it is
	// the one legitimate match.
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine this file's path")
	}
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") || sameFile(path, self) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		// Two forms reach the same dead end: setting the variable for this
		// process, and building a child process's env with it. #62's SIGTERM
		// e2e used the second and slipped past a check for only the first.
		if !strings.Contains(src, `t.Setenv("XDG_CONFIG_HOME"`) &&
			!strings.Contains(src, `"XDG_CONFIG_HOME="`) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		offenders = append(offenders, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("these tests isolate config via XDG_CONFIG_HOME, which macOS ignores; "+
			"call configtest.Isolate(t) instead:\n\t%s", strings.Join(offenders, "\n\t"))
	}
}

// sameFile reports whether two paths name the same file, comparing on identity
// rather than text so a symlinked checkout (macOS /var, say) still matches.
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}
