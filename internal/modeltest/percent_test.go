package modeltest

import "testing"

func TestPercent(t *testing.T) {
	cases := []struct {
		covered, total int
		want           float64
	}{
		{0, 0, 0},
		{5, 0, 0},
		{0, 10, 0},
		{10, 10, 100},
		{1, 4, 25},
	}
	for _, c := range cases {
		if got := Percent(c.covered, c.total); got != c.want {
			t.Errorf("Percent(%d, %d) = %v, want %v", c.covered, c.total, got, c.want)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{
		{80, "80%"},
		{100, "100%"},
		{0, "0%"},
		{77.333333, "77.3%"},
		{99.96, "99.9%"}, // below 100 but rounds up: must not read as full
		{99.94, "99.9%"}, // ordinary one-decimal rounding, unchanged
	}
	for _, c := range cases {
		if got := FormatPercent(c.pct); got != c.want {
			t.Errorf("FormatPercent(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}
