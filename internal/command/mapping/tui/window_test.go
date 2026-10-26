package tui

import (
	"fmt"
	"strings"
	"testing"
)

func numbered(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("line %d", i)
	}
	return out
}

func TestWindowAround(t *testing.T) {
	tests := []struct {
		name   string
		lines  []string
		cursor int
		h      int
		want   []string
	}{
		{"fits", numbered(3), 1, 5, []string{"line 0", "line 1", "line 2"}},
		{"cursor top", numbered(10), 0, 5, []string{"line 0", "line 1", "line 2", "line 3", "↓ 6 more"}},
		{"cursor bottom", numbered(10), 9, 5, []string{"↑ 6 more", "line 6", "line 7", "line 8", "line 9"}},
		{"middle", numbered(10), 5, 5, []string{"↑ 4 more", "line 4", "line 5", "line 6", "↓ 3 more"}},
		{"h=1", numbered(10), 5, 1, []string{"line 5"}},
		{"h=2", numbered(10), 5, 2, []string{"line 5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Split(plain(windowAround(tt.lines, tt.cursor, tt.h)), "\n")
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The cursor must stay visible whatever the height, and never be the line an
// indicator replaced.
func TestWindowAroundKeepsTheCursorVisible(t *testing.T) {
	lines := numbered(30)
	for h := 1; h <= 12; h++ {
		for c := range lines {
			out := strings.Split(plain(windowAround(lines, c, h)), "\n")
			if len(out) > h {
				t.Fatalf("h=%d cursor=%d: %d lines", h, c, len(out))
			}
			if !strings.Contains(strings.Join(out, "\n"), lines[c]) {
				t.Fatalf("h=%d cursor=%d: cursor line missing in %q", h, c, out)
			}
		}
	}
}
