package mapping

import (
	"fmt"
	"time"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// parseSince turns --since into a time: a positive Go duration counts back
// from now, otherwise RFC 3339. An empty value gives the zero time.
func parseSince(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	bad := clierr.WithCode(clierr.CodeUsage, fmt.Errorf(
		"--since must be a duration like 2h or an RFC 3339 timestamp like 2026-01-31T15:04:05Z (got %q)", value))
	at := now()
	var since time.Time
	if d, err := time.ParseDuration(value); err == nil {
		if d <= 0 {
			return time.Time{}, bad
		}
		since = at.Add(-d)
	} else if t, err := time.Parse(time.RFC3339, value); err == nil {
		since = t
	} else {
		return time.Time{}, bad
	}
	if since.After(at) {
		return time.Time{}, clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--since is in the future (%s)", since.UTC().Format(time.RFC3339)))
	}
	return since, nil
}
