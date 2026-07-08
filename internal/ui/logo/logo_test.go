package logo

import (
	"strings"
	"testing"
)

func TestWordSlabDimensions(t *testing.T) {
	rows := strings.Split(Word("ofga"), "\n")
	if len(rows) != 4 || Height != 4 {
		t.Fatalf("slab wordmark must be 4 rows, got %d (Height=%d)", len(rows), Height)
	}
	for i, r := range rows {
		if w := len([]rune(r)); w != 22 {
			t.Fatalf("row %d width = %d, want 22 (must fit the narrowest sidebar)", i, w)
		}
	}
}
