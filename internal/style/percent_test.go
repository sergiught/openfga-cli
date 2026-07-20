package style

import (
	"testing"

	"github.com/sergiught/openfga-cli/internal/theme"
)

func TestPercentColorDistinctByThreshold(t *testing.T) {
	// Pin a colored theme: another test may have left mono active, under which
	// all three thresholds render identically and this test would flake.
	defer Apply(Active)
	Apply(theme.Default())

	full := PercentColor(100).Render("x")
	partial := PercentColor(50).Render("x")
	zero := PercentColor(0).Render("x")

	if full == partial {
		t.Errorf("PercentColor(100) and PercentColor(50) rendered the same: %q", full)
	}
	if full == zero {
		t.Errorf("PercentColor(100) and PercentColor(0) rendered the same: %q", full)
	}
	if partial == zero {
		t.Errorf("PercentColor(50) and PercentColor(0) rendered the same: %q", partial)
	}
}
