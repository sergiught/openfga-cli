package status

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// num formats n with thousands separators.
func num(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// dur formats d as 42s, 3m 16s or 2h 05m.
func dur(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// size formats b as 812 MB or 1.1 GB.
func size(b uint64) string {
	const mb, gb = 1 << 20, 1 << 30
	if b >= gb {
		return fmt.Sprintf("%.1f GB", float64(b)/gb)
	}
	return fmt.Sprintf("%d MB", b/mb)
}

// typeName turns a snapshot type such as "snapshot.organization_member" into
// "organization members" ("organization member" when n is 1).
func typeName(typ string, n int) string {
	name := strings.NewReplacer("_", " ", ".", " ").Replace(strings.TrimPrefix(typ, "snapshot."))
	if n == 1 {
		return name
	}
	return name + "s"
}

// eta estimates the time left for a step that has run for elapsed and
// finished done of total. It is 0 until the step has run 3s with done > 0.
func eta(elapsed time.Duration, done, total int) time.Duration {
	if elapsed < 3*time.Second || done <= 0 || done >= total {
		return 0
	}
	return time.Duration(float64(elapsed) * float64(total-done) / float64(done))
}

// sample is a counter value at a moment.
type sample struct {
	at time.Time
	n  int
}

// window keeps the samples of the last 5 seconds, one per second.
type window []sample

const windowSpan = 5 * time.Second

// add records n at now, at most once a second, and drops old samples.
func (w *window) add(now time.Time, n int) {
	if len(*w) > 0 && now.Sub((*w)[len(*w)-1].at) < time.Second {
		return
	}
	*w = append(*w, sample{now, n})
	for len(*w) > 2 && now.Sub((*w)[0].at) > windowSpan {
		*w = (*w)[1:]
	}
}

// rate is the change per second between the oldest and newest sample, 0
// until there are two.
func (w window) rate() float64 {
	if len(w) < 2 {
		return 0
	}
	first, last := w[0], w[len(w)-1]
	dt := last.at.Sub(first.at).Seconds()
	if dt <= 0 {
		return 0
	}
	return float64(last.n-first.n) / dt
}
