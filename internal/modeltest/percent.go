package modeltest

import (
	"fmt"
	"math"
)

// Percent returns covered/total as a percentage, 0 when total is 0.
func Percent(covered, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(covered) / float64(total) * 100
}

// FormatPercent renders a percentage without a trailing ".0" for whole
// numbers (e.g. "80%") while keeping one decimal place otherwise (e.g.
// "77.3%"). A value strictly below 100 that would round up to "100.0%" (e.g.
// 99.96) is floored to "99.9%" instead, so only genuine full coverage ever
// reads as 100.
func FormatPercent(p float64) string {
	if p == math.Trunc(p) {
		return fmt.Sprintf("%.0f%%", p)
	}
	s := fmt.Sprintf("%.1f", p)
	if p < 100 && s == "100.0" {
		s = "99.9"
	}
	return s + "%"
}
