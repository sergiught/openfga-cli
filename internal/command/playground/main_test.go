package playground

import (
	"os"
	"testing"
)

// TestMain shortens the playground benchmark: tests assert on answers and
// rendering, not on timings, and each bench otherwise dominates the package's
// runtime under -race.
func TestMain(m *testing.M) {
	benchIterations = 2
	os.Exit(m.Run())
}
