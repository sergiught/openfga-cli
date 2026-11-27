package mapping

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points the cache directory at a temporary one for the whole package,
// so no test writes a work file under the real user cache directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mapping-cache-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cacheDir = func() (string, error) { return dir, nil }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
