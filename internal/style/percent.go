package style

import lipgloss "charm.land/lipgloss/v2"

// PercentColor picks the coverage-percentage style by threshold: full
// coverage reads success/green, zero reads failure/red, and any partial
// amount in between reads warn/amber (the closest existing primitive to a
// dedicated "attention" color).
func PercentColor(pct float64) lipgloss.Style {
	switch {
	case pct >= 100:
		return Success
	case pct <= 0:
		return Failure
	default:
		return Warn
	}
}
